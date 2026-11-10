// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"go.yaml.in/yaml/v3"
)

// testTimeoutGate names the check in its lines.
const testTimeoutGate = "test-timeout"

// testTimeoutBoundMinutes is the go test timeout workflows and Makefile keep per package (#1381).
const testTimeoutBoundMinutes = 30

var (
	// goTestCommand also recognizes the Makefile's sandboxed GO_TEST command.
	goTestCommand = regexp.MustCompile(`\bgo(?:\.exe)?\s+[^;&|]*\btest\b|\$\(GO_TEST\)`)
	// goTimeoutFlag matches a -timeout or --timeout flag on a command line; its value is a whole
	// ${{ ... }} expression, spaces included, or a word.
	goTimeoutFlag = regexp.MustCompile(`(?:^|[\s;&|])--?timeout(?:=|\s+)["']?(\$\{\{[^}]*\}\}|[^"'\s;&|]+)["']?`)
	// inputExpression matches a value that is exactly ${{ inputs.<id> }}.
	inputExpression = regexp.MustCompile(`^\$\{\{\s*inputs\.([A-Za-z_][A-Za-z0-9_-]*)\s*\}\}$`)
)

const (
	maxTimeoutWorkflowLines = 20000
	maxMatchesPerLine       = 16
)

// TestTimeoutOptions is what AuditTestTimeouts reads: the repository root, the manifest's
// exceptions list (the check reads the entries of config.ExceptionRuleTestTimeout) and the day
// those entries' expiry is judged against.
type TestTimeoutOptions struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

// AuditTestTimeouts checks workflow and root Makefile lines against the per-package timeout
// bound, naming the file, line and value of each timeout without a live exception.
func AuditTestTimeouts(ctx context.Context, opts TestTimeoutOptions) (string, error) {
	files, err := readTestTimeoutFiles(ctx, opts.Root)
	if err != nil {
		return "", err
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleTestTimeout)
	used := make([]bool, len(entries))
	var findings, excepted []string
	for _, file := range files {
		rel := file.Name
		long, err := fileTimeouts(file)
		if err != nil {
			return "", err
		}
		if len(long) == 0 {
			continue
		}
		live, expired := config.ExceptionFor(entries, rel, opts.Today, used)
		if live != nil {
			excepted = append(excepted, fmt.Sprintf("[PASS] %s: %s excepted until %s: %s", testTimeoutGate, rel, live.Expires, live.Reason))
			continue
		}
		if expired != nil {
			findings = append(findings, fmt.Sprintf("[FAIL] %s: the exceptions entry for %s expired %s; bring the timeout back to the bound or renew the entry with a reason", testTimeoutGate, rel, expired.Expires))
		}
		findings = append(findings, long...)
	}
	for i, entry := range entries {
		if !used[i] {
			findings = append(findings, fmt.Sprintf("[FAIL] %s: the exceptions entry for %s excuses nothing, because the file keeps the bound; remove it", testTimeoutGate, entry.Target()))
		}
	}
	if len(findings) > 0 {
		return "", errors.New(strings.Join(findings, "\n"))
	}
	return strings.Join(append(excepted, "[PASS] test-timeout"), "\n"), nil
}

// fileTimeouts returns the long go test timeouts of one audited file: the root Makefile by joined
// logical lines, a workflow line by line with its input defaults.
func fileTimeouts(file workflowFile) ([]string, error) {
	if file.Name == "Makefile" {
		return makefileTimeouts(string(file.Data))
	}
	return longTimeouts(file.Name, string(file.Data), timeoutInputDefaults(file.Data)), nil
}

// makefileTimeouts joins continued physical lines and retains their first line number.
func makefileTimeouts(text string) ([]string, error) {
	lines := strings.Split(text, "\n")
	var out []string
	var logical strings.Builder
	start := 0
	continued := false
	for n := 0; n < len(lines) && n < maxTimeoutWorkflowLines; n++ {
		if !continued {
			start = n
		}
		line := strings.TrimSuffix(lines[n], "\r")
		continued = strings.HasSuffix(line, "\\")
		if continued {
			logical.WriteString(strings.TrimSuffix(line, "\\"))
			logical.WriteByte(' ')
			if n+1 >= len(lines) || (n+2 == len(lines) && lines[n+1] == "") || n+1 >= maxTimeoutWorkflowLines {
				return nil, fmt.Errorf("[FAIL] %s: Makefile:%d has an unfinished continuation", testTimeoutGate, n+1)
			}
			continue
		}
		logical.WriteString(line)
		out = append(out, checkGoTestTimeouts("Makefile", start, logical.String(), nil)...)
		logical.Reset()
	}
	return out, nil
}

// readTestTimeoutFiles retains workflow paths and adds the optional root Makefile through the
// same bounded, symlink-resistant snapshot reader used for repository source files.
func readTestTimeoutFiles(ctx context.Context, root string) ([]workflowFile, error) {
	files, err := readWorkflowFiles(ctx, root)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		files[i].Name = ghworkflow.Dir + "/" + files[i].Name
	}
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, "Makefile"))
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, fmt.Errorf("test-timeout Makefile: %w", err)
	}
	return append(files, workflowFile{Name: "Makefile", Data: data}), nil
}

// timeoutInputDefaults returns the literal workflow_call input defaults of a workflow
// (workflowCallDefaults), by input id; a workflow that does not parse has none, so an input it
// names stays unreadable.
func timeoutInputDefaults(data []byte) map[string]exprValue {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil
	}
	return workflowCallDefaults(&spec)
}

// checkGoTestTimeouts checks a go test command line for -timeout flags.
func checkGoTestTimeouts(rel string, n int, line string, defaults map[string]exprValue) []string {
	if !goTestCommand.MatchString(line) {
		return nil
	}
	matches := goTimeoutFlag.FindAllStringSubmatch(line, maxMatchesPerLine)
	var out []string
	for i := 0; i < len(matches) && i < maxMatchesPerLine; i++ {
		if finding := judgeTimeout(fmt.Sprintf("%s:%d", rel, n+1), matches[i][1], defaults); finding != "" {
			out = append(out, finding)
		}
	}
	return out
}

// judgeTimeout returns the finding for one -timeout value at where, "" within the bound. A value
// ${{ inputs.<id> }} is judged by that input's workflow_call default; an input without a
// literal default the gate can read is unreadable, as any other value it cannot parse.
func judgeTimeout(where, raw string, defaults map[string]exprValue) string {
	value, shown := raw, raw
	if input := inputExpression.FindStringSubmatch(raw); input != nil {
		def, ok := defaults[input[1]]
		if !ok {
			return fmt.Sprintf("[FAIL] %s: %s sets %s, an unreadable timeout: the input has no default this gate can read", testTimeoutGate, where, raw)
		}
		value, shown = def.text, fmt.Sprintf("%s (default %s)", raw, def.text)
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Sprintf("[FAIL] %s: %s sets %s, an unreadable timeout", testTimeoutGate, where, shown)
	}
	if d > time.Duration(testTimeoutBoundMinutes)*time.Minute {
		return fmt.Sprintf("[FAIL] %s: %s sets %s, above the %dm bound", testTimeoutGate, where, shown, testTimeoutBoundMinutes)
	}
	return ""
}

// longTimeouts returns one line per go test -timeout above the bound or unreadable in rel; a job's
// timeout-minutes is not this gate's subject (decision engine-timeout-audit-scope-20261018).
// defaults are the workflow's workflow_call input defaults (timeoutInputDefaults).
func longTimeouts(rel, text string, defaults map[string]exprValue) []string {
	if !goTestCommand.MatchString(text) {
		return nil
	}
	lines := strings.Split(text, "\n")
	var out []string
	for n := 0; n < len(lines) && n < maxTimeoutWorkflowLines; n++ {
		out = append(out, checkGoTestTimeouts(rel, n, lines[n], defaults)...)
	}
	return out
}
