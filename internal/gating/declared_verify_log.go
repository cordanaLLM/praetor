// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	declaredVerifyTailLines      = 60
	maxDeclaredVerifyLogBytes    = 64 << 10
	maxDeclaredVerifyOutputBytes = 2*util.MaxCommandOutputBytes + 1
)

// makeSignalDescription lists fatal signal descriptions used by POSIX implementations.
const makeSignalDescription = `(?:Hangup|Interrupt|Quit|Illegal instruction|Trace/breakpoint trap|Aborted|Abort trap|EMT trap|Floating point exception|Killed|Bus error|Segmentation fault|Bad system call|Broken pipe|Alarm clock|Terminated|User defined signal [12]|User signal [12]|Virtual timer expired|Profiling timer expired|CPU time limit exceeded|File size limit exceeded|Power failure|Real-time signal [0-9]+|Signal [0-9]+)(?: \(core dumped\))?`

// GNU Make Appendix B requires *** for fatal failures, including recipe signals:
// https://www.gnu.org/software/make/manual/html_node/Error-Messages.html
// Make 4 names file, line and target; Make 3.81 names only the target.
var makeFailureLines = [...]*regexp.Regexp{
	regexp.MustCompile(`^make(?:\[([0-9]+)\])?: \*\*\* \[(?:.*:[0-9]+: )?(.+)\] (?:Error [0-9]+(?: .*)?|` + makeSignalDescription + `)$`),
	regexp.MustCompile("^make(?:\\[([0-9]+)\\])?: \\*\\*\\* No rule to make target ['`](.+?)'(?:, needed by ['`].*')?\\.(?: +Stop\\.)?$"),
}

// declaredVerifyDiagnostic combines bounded stdout then stderr, collapses progress updates,
// and retains the innermost failing target even when it precedes the last sixty lines.
func declaredVerifyDiagnostic(output util.CommandBytes) (string, string) {
	text := string(output.Stdout)
	if len(output.Stderr) > 0 {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += string(output.Stderr)
	}
	text = strings.ReplaceAll(text, "\r\n", "\n")
	var lines [declaredVerifyTailLines]string
	target, bestDepth, count := "make named no failing target", -1, 0
	for remaining := text; remaining != "" && count < maxDeclaredVerifyOutputBytes; count++ {
		line, rest, _ := strings.Cut(remaining, "\n")
		line = line[strings.LastIndexByte(line, '\r')+1:]
		lines[count%declaredVerifyTailLines] = line
		if name, depth := declaredVerifyMakeTarget(line); depth > bestDepth {
			target, bestDepth = "failing make target: "+util.UTF8Tail(name, maxDeclaredVerifyDiagnostic), depth
		}
		remaining = rest
	}
	start := count % declaredVerifyTailLines
	if count < declaredVerifyTailLines {
		return target, strings.Join(lines[:count], "\n")
	}
	ordered := append(lines[start:], lines[:start]...)
	return target, strings.Join(ordered, "\n")
}

// declaredVerifyMakeTarget extracts a valid make failure's target and nesting depth.
func declaredVerifyMakeTarget(line string) (string, int) {
	if !strings.HasPrefix(line, "make") {
		return "", -1
	}
	var match []string
	for _, pattern := range makeFailureLines {
		if match = pattern.FindStringSubmatch(line); match != nil {
			break
		}
	}
	if match == nil {
		return "", -1
	}
	depth := 0
	if match[1] != "" {
		parsed, err := strconv.Atoi(match[1])
		if err != nil {
			return "", -1 // An overflowing recursion depth is not a valid make diagnostic.
		}
		depth = parsed
	}
	return match[2], depth
}

// writeDeclaredVerifyLog uses the checkout, never the disposable stage worktree. A separate
// short context permits diagnostics after the stage deadline without extending verification.
func writeDeclaredVerifyLog(ctx context.Context, repoDir, target, diagnostic string) (string, error) {
	logCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), util.GitProbeTimeout)
	defer cancel()
	return writeDeclaredVerifyLogWithIO(logCtx, repoDir, target, diagnostic, declaredVerifyLogIO{
		mkdir: util.MkdirIn, write: util.WriteFileExclusiveIn,
	})
}

// declaredVerifyLogIO keeps injection local to one write; stalled fixtures never replace global functions.
type declaredVerifyLogIO struct {
	mkdir func(*os.Root, string, os.FileMode) error
	write func(*os.Root, string, []byte, os.FileMode) error
}

// writeDeclaredVerifyLogWithIO retains both roots across the ignore query and publication.
// Every blocking step, including write permissions and fsync, shares the logging deadline.
func writeDeclaredVerifyLogWithIO(ctx context.Context, repoDir, target, diagnostic string, disk declaredVerifyLogIO) (_ string, err error) {
	checkout, err := util.BoundedCall(ctx, "open checkout for gate log", func() (*os.Root, error) {
		return os.OpenRoot(repoDir)
	}, discardDeclaredLogRoot)
	if err != nil {
		return "", err
	}
	defer closeDeclaredLogRoot(checkout, &err)
	info, err := util.BoundedCall(ctx, ".workingdir unavailable", func() (os.FileInfo, error) {
		return checkout.Lstat(".workingdir")
	}, nil)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf(".workingdir is not a directory (symlinks refused)")
	}
	ledger, err := util.BoundedCall(ctx, "pin .workingdir", func() (*os.Root, error) {
		return util.OpenDirectoryIn(checkout, ".workingdir", info)
	}, discardDeclaredLogRoot)
	if err != nil {
		return "", err
	}
	defer closeDeclaredLogRoot(ledger, &err)
	if err := declaredVerifyLogAllowed(ctx, repoDir, checkout, info); err != nil {
		return "", err
	}
	if err := declaredLogStep(ctx, "create .workingdir/gate", func() error { return disk.mkdir(ledger, "gate", 0o700) }); err != nil {
		return "", err
	}
	rel := filepath.Join(".workingdir", "gate", "declared-verify-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".log")
	header := target + "\n\n"
	body := header + util.UTF8Tail(diagnostic, maxDeclaredVerifyLogBytes-len(header)-4) + "\n"
	if err := declaredLogIdentity(ctx, checkout, info); err != nil {
		return "", err
	}
	if err := declaredLogStep(ctx, "write "+rel, func() error {
		return disk.write(ledger, filepath.Join("gate", filepath.Base(rel)), []byte(body), 0o600)
	}); err != nil {
		return "", err
	}
	if err := declaredLogIdentity(ctx, checkout, info); err != nil {
		return "", err
	}
	return filepath.Join(repoDir, rel), nil
}

// declaredVerifyLogAllowed requires an ignore answer for the same directory that was pinned.
func declaredVerifyLogAllowed(ctx context.Context, repoDir string, checkout *os.Root, expected os.FileInfo) error {
	matches, err := util.GitIgnoreMatches(ctx, repoDir, []string{".workingdir/"}, false)
	if err != nil {
		return fmt.Errorf("check whether .workingdir is git-ignored: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf(".workingdir is not git-ignored")
	}
	return declaredLogIdentity(ctx, checkout, expected)
}

// declaredLogIdentity keeps the replacement check under the same deadline as the write.
func declaredLogIdentity(ctx context.Context, checkout *os.Root, expected os.FileInfo) error {
	return declaredLogStep(ctx, "check .workingdir replacement", func() error {
		return util.CheckDirectoryIdentity(checkout, ".workingdir", expected)
	})
}

// declaredLogStep adapts error-only filesystem operations to the bounded result handoff.
func declaredLogStep(ctx context.Context, step string, call func() error) error {
	_, err := util.BoundedCall(ctx, step, func() (struct{}, error) { return struct{}{}, call() }, nil)
	return err
}

// closeDeclaredLogRoot preserves close errors without replacing an existing diagnostic error.
func closeDeclaredLogRoot(root *os.Root, err *error) {
	if closeErr := root.Close(); closeErr != nil && *err == nil {
		*err = fmt.Errorf("close gate log directory: %w", closeErr)
	}
}

// discardDeclaredLogRoot releases an open completed after its caller's deadline.
func discardDeclaredLogRoot(root *os.Root) {
	if root != nil {
		//nolint:errcheck // best-effort late cleanup; the caller has already returned its deadline error
		root.Close() // #nosec G104 -- no caller remains to receive a late close error
	}
}
