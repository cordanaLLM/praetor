// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package readmepresentation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// Options holds the inputs for the README presentation audit.
type Options struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

// Audit runs the README presentation gate: checks README.md against rules README-01..README-05.
func Audit(ctx context.Context, opts Options) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + GateName + ": the audit requires a context")
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleReadmePresentation)
	if err := config.ValidateExceptions(entries, opts.Today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", GateName, err)
	}

	findings, err := CheckFile(ctx, opts.Root, FileName)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", GateName, err)
	}
	return judgeFindings(findings, entries, opts.Today)
}

func judgeFindings(findings []Finding, entries []config.Exception, today time.Time) (string, error) {
	if len(findings) == 0 {
		if len(entries) > 0 {
			return "", errors.New(adopt.StaleExceptionEntries(GateName, config.ExceptionRuleReadmePresentation, "findings", entries, "README.md complies with the presentation standard"))
		}
		return fmt.Sprintf("[PASS] %s: %s complies with the presentation standard (README-01..README-05).", GateName, FileName), nil
	}

	used := make([]bool, len(entries))
	live, expired := config.ExceptionFor(entries, FileName, today, used)
	if live != nil {
		return formatExcusedGap(findings, *live), nil
	}
	if expired != nil {
		return "", errors.New(adopt.ExpiredExceptionEntry(GateName, findingsSummary(findings), "update README.md to comply", *expired))
	}
	return "", errors.New(formatFailure(findings))
}

func formatExcusedGap(findings []Finding, live config.Exception) string {
	var report strings.Builder
	cat := live.Category
	if cat == "" {
		cat = "standard"
	}
	fmt.Fprintf(&report, "[PASS] %s: declared gap (%d finding(s)), excepted until %s by the exceptions entry (rule %s, Category %s): %s\n",
		GateName, len(findings), live.Expires, live.Rule, cat, live.Reason)
	for i := 0; i < len(findings) && i < 20; i++ {
		fmt.Fprintf(&report, "  - %s\n", findings[i].String())
	}
	return strings.TrimRight(report.String(), "\n")
}

func formatFailure(findings []Finding) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[FAIL] %s: %s fails presentation standard (%d finding(s)):\n", GateName, FileName, len(findings))
	for i := 0; i < len(findings) && i < 20; i++ {
		fmt.Fprintf(&b, "  - %s\n", findings[i].String())
	}
	fmt.Fprintf(&b, "Fix the findings above or declare an exception (rule %s, path %s) in .standards.yaml.",
		config.ExceptionRuleReadmePresentation, FileName)
	return b.String()
}

func findingsSummary(findings []Finding) string {
	if len(findings) == 1 {
		return findings[0].Message
	}
	return fmt.Sprintf("%d presentation findings fail", len(findings))
}
