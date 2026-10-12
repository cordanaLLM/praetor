// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// declaredLintersGate is the title used on every audit verdict line.
const declaredLintersGate = "Declared linters"

// DeclaredLintersOptions is what AuditDeclaredLinters reads: the repository root,
// the effective policy, the manifest's exceptions list (rule "declared-linters"),
// and the day entries' expiry is judged against.
type DeclaredLintersOptions struct {
	RootDir    string
	Policy     *config.EffectivePolicy
	Exceptions []config.Exception
	Today      time.Time
}

// AuditDeclaredLinters verifies that every linter declared by the profiles or facets in policy
// is run by at least one CI workflow under .github/workflows in rootDir (#1289). Each missing
// linter is reported as one finding naming the linter and where it was declared, unless excused
// by a live exception entry.
func AuditDeclaredLinters(ctx context.Context, opts DeclaredLintersOptions) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + declaredLintersGate + ": the audit requires a context")
	}
	if opts.Policy == nil {
		return "", errors.New("[FAIL] " + declaredLintersGate + ": policy is required")
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleDeclaredLinters)
	if err := config.ValidateExceptions(entries, today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	declared, err := opts.Policy.DeclaredLinters(ctx)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", declaredLintersGate, err)
	}
	if len(declared) == 0 {
		if len(entries) > 0 {
			return "", errors.New(staleExceptionEntries(declaredLintersGate, config.ExceptionRuleDeclaredLinters, "linter", entries, "policy declares no linters"))
		}
		return "[PASS] " + declaredLintersGate + ": policy declares no linters; no workflow is required.", nil
	}

	names := make([]string, 0, len(declared))
	for _, l := range declared {
		names = append(names, l.Name)
	}

	present, err := forge.MeasureWorkflowLinters(ctx, opts.RootDir, names)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot inspect workflows: %w", declaredLintersGate, err)
	}

	var missing []config.DeclaredLinter
	for _, l := range declared {
		if !present[l.Name] {
			missing = append(missing, l)
		}
	}

	judged := judgeDeclaredLinters(missing, entries, today)
	if len(judged.unexcused) > 0 || len(judged.stale) > 0 {
		return "", errors.New(judged.failure())
	}
	return judged.pass(len(declared)), nil
}

type declaredLintersJudgement struct {
	excused   []config.DeclaredLinter
	unexcused []config.DeclaredLinter
	live      map[string]config.Exception
	expired   map[string]config.Exception
	stale     []config.Exception
}

func judgeDeclaredLinters(missing []config.DeclaredLinter, entries []config.Exception, today time.Time) declaredLintersJudgement {
	judged := declaredLintersJudgement{
		live:    make(map[string]config.Exception),
		expired: make(map[string]config.Exception),
	}
	missingMap := make(map[string]bool, len(missing))
	for _, m := range missing {
		missingMap[m.Name] = true
	}
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		entry := entries[i]
		switch {
		case !missingMap[entry.Tool]:
			judged.stale = append(judged.stale, entry)
		case entry.Expired(today):
			judged.expired[entry.Tool] = entry
		default:
			judged.live[entry.Tool] = entry
		}
	}
	for _, m := range missing {
		if _, ok := judged.live[m.Name]; ok {
			judged.excused = append(judged.excused, m)
		} else {
			judged.unexcused = append(judged.unexcused, m)
		}
	}
	return judged
}

func (j declaredLintersJudgement) failure() string {
	lines := make([]string, 0, len(j.unexcused)*2+2)
	if len(j.unexcused) > 0 {
		lines = append(lines, fmt.Sprintf("[FAIL] %s: %d declared linter(s) not run by any workflow:", declaredLintersGate, len(j.unexcused)))
		for _, u := range j.unexcused {
			lines = append(lines, fmt.Sprintf("  - %s: declared by %s, not run by any workflow", u.Name, u.Source))
		}
		for _, u := range j.unexcused {
			if exp, ok := j.expired[u.Name]; ok {
				lines = append(lines, expiredExceptionEntry(declaredLintersGate, "the linter "+u.Name+" is missing", "run it in a CI workflow", exp))
			}
		}
	}
	if len(j.stale) > 0 {
		lines = append(lines, staleExceptionEntries(declaredLintersGate, config.ExceptionRuleDeclaredLinters, "linter", j.stale, "the linters they name are run or not declared"))
	}
	return strings.Join(lines, "\n")
}

func (j declaredLintersJudgement) pass(totalDeclared int) string {
	if len(j.excused) == 0 {
		return fmt.Sprintf("[PASS] %s: every declared linter is run by a CI workflow (%d verified).", declaredLintersGate, totalDeclared)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: %d declared linters run; %d excepted:", declaredLintersGate, totalDeclared-len(j.excused), len(j.excused))
	for _, e := range j.excused {
		live := j.live[e.Name]
		fmt.Fprintf(&b, "\n  - %s, excepted until %s by the exceptions entry (rule %s, %s): %s",
			e.Name, live.Expires, live.Rule, live.Target(), live.Reason)
	}
	return b.String()
}
