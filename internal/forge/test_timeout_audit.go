// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"go.yaml.in/yaml/v3"
)

// testTimeoutGate names the check in its lines.
const testTimeoutGate = "test-timeout"

// testTimeoutBoundMinutes is the go test timeout every workflow keeps per package (#1381).
const testTimeoutBoundMinutes = 30

var (
	// goTestCommand matches a go command invoking the test subcommand.
	goTestCommand = regexp.MustCompile(`\bgo(?:\.exe)?\s+[^;&|]*\btest\b`)
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

// AuditTestTimeouts is the test-timeout gate: a workflow line that sets a timeout above the
// per-package bound fails, naming the workflow, the line and the value.
func AuditTestTimeouts(ctx context.Context, opts TestTimeoutOptions) (string, error) {
	files, err := readWorkflowFiles(ctx, opts.Root)
	if err != nil {
		return "", err
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleTestTimeout)
	used := make([]bool, len(entries))
	var findings, excepted []string
	for _, file := range files {
		rel := ghworkflow.Dir + "/" + file.Name
		long := longTimeouts(rel, string(file.Data), timeoutInputDefaults(file.Data))
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
			findings = append(findings, fmt.Sprintf("[FAIL] %s: the exceptions entry for %s excuses nothing, because the workflow keeps the bound; remove it", testTimeoutGate, entry.Target()))
		}
	}
	if len(findings) > 0 {
		return "", errors.New(strings.Join(findings, "\n"))
	}
	return strings.Join(append(excepted, "[PASS] test-timeout"), "\n"), nil
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
