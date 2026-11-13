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

// GNU Make 4 names file, line and target; Make 3.81 names only the target.
var makeFailureLine = regexp.MustCompile(`^make(?:\[([0-9]+)\])?: \*\*\* \[(?:.*:[0-9]+: )?(.+)\] Error [0-9]+(?: .*)?$`)

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
			target, bestDepth = "failing make target: "+tail(name, maxDeclaredVerifyDiagnostic), depth
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

func declaredVerifyMakeTarget(line string) (string, int) {
	if !strings.HasPrefix(line, "make") {
		return "", -1
	}
	match := makeFailureLine.FindStringSubmatch(line)
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
	if err := declaredVerifyLogAllowed(logCtx, repoDir); err != nil {
		return "", err
	}
	relDir := filepath.Join(".workingdir", "gate")
	privateDir := filepath.Join(repoDir, ".workingdir")
	if err := util.MkdirConfined(privateDir, "gate", 0o700); err != nil {
		return "", fmt.Errorf("create .workingdir/gate: %w", err)
	}
	if err := logCtx.Err(); err != nil {
		return "", fmt.Errorf("write declared verification log: %w", err)
	}
	rel := filepath.Join(relDir, "declared-verify-"+time.Now().UTC().Format("20060102T150405.000000000Z")+".log")
	header := target + "\n\n"
	body := header + tail(diagnostic, maxDeclaredVerifyLogBytes-len(header)-4) + "\n"
	if err := util.WriteFileConfinedExclusive(privateDir, filepath.Join("gate", filepath.Base(rel)), []byte(body), 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", filepath.Join(repoDir, rel), err)
	}
	return filepath.Join(repoDir, rel), nil
}

func declaredVerifyLogAllowed(ctx context.Context, repoDir string) error {
	info, err := os.Lstat(filepath.Join(repoDir, ".workingdir"))
	if err != nil {
		return fmt.Errorf(".workingdir unavailable: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf(".workingdir is not a directory (symlinks refused)")
	}
	matches, err := util.GitIgnoreMatches(ctx, repoDir, []string{".workingdir/"}, false)
	if err != nil {
		return fmt.Errorf("check whether .workingdir is git-ignored: %w", err)
	}
	if len(matches) == 0 {
		return fmt.Errorf(".workingdir is not git-ignored")
	}
	return nil
}
