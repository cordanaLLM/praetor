// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// testTimeoutGate names the check in its lines.
const testTimeoutGate = "test-timeout"

// testTimeoutBoundMinutes is the go test timeout every workflow keeps per package (#1381).
const testTimeoutBoundMinutes = 30

var (
	// timeoutWord marks a line that sets a timeout, in any case (an environment name is upper case).
	timeoutWord = regexp.MustCompile(`(?i)timeout`)
	// minutesLiteral is a duration in minutes, such as 30m; a job's timeout-minutes carries no unit.
	minutesLiteral = regexp.MustCompile(`\b(\d+)m\b`)
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
		long := longTimeouts(rel, string(file.Data))
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

// longTimeouts returns one line per minutes value above the bound on a line that sets a timeout.
func longTimeouts(rel, text string) []string {
	var out []string
	for n, line := range strings.Split(text, "\n") {
		if !timeoutWord.MatchString(line) {
			continue
		}
		for _, match := range minutesLiteral.FindAllStringSubmatch(line, -1) {
			if minutes, err := strconv.Atoi(match[1]); err == nil && minutes > testTimeoutBoundMinutes {
				out = append(out, fmt.Sprintf("[FAIL] %s: %s:%d sets %s, above the %dm bound", testTimeoutGate, rel, n+1, match[0], testTimeoutBoundMinutes))
			}
		}
	}
	return out
}
