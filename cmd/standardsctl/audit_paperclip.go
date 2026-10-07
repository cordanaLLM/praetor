// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/paperclip"
	"github.com/cordanaLLM/praetor/internal/util"
)

// paperclipRulesRel is the rendering of the Paperclip harness the audit compares and lints.
const paperclipRulesRel = ".paperclip/rules.md"

// auditPaperclipSynthesis compares the harness under rootDir, loaded as audit loads it, with this
// release's synthesis for the repository's facts: the ones `praetorctl paperclip harness` reads,
// so its output is what the gate expects (#321). adopt.RepositoryHISSFacts walks the repository
// under limits, the run's flags, over the manifest's verification section, which the hooks and CI
// jobs that pass no flag read, so they walk a large repository as far as adoption does. A
// synthesis whose register directive names no skill because a register skill could not be read
// is compared as it stands and the substitution printed as a warning, as `praetorctl paperclip
// harness` prints it (paperclip.SynthesizeHarnessOver, #235). Unmodified earlier output, such as
// a harness naming the `caveman` skill in a repository that does not carry it, fails with the
// adopt remedy, since it states a policy the repository no longer has; a rules.md that is not the rendering of harness.json fails with the regenerate
// remedy; rules.md then passes the caveman lint personas and skills pass
// (compiler.LintAgentText). An edited harness.json is operator-owned, which adoption keeps byte
// for byte (#502), and passes as such. It returns what the pass line states.
func auditPaperclipSynthesis(ctx context.Context, rootDir string, limits *adopt.VerificationLimits, loaded *paperclip.Harness) (string, error) {
	facts, warnings, err := adopt.RepositoryHISSFacts(ctx, rootDir, limits)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Paperclip harness facts: %w", err)
	}
	for _, warning := range warnings {
		fmt.Printf("[WARN] Paperclip harness facts: %s\n", warning)
	}
	expected, substitution, err := paperclip.SynthesizeHarness(ctx, rootDir, facts)
	if err != nil {
		return "", fmt.Errorf("[FAIL] Paperclip harness synthesis: %w", err)
	}
	if substitution != "" {
		fmt.Printf("[WARN] Paperclip harness synthesis: %s\n", substitution)
	}
	comparison, err := paperclip.CompareGenerated(ctx, rootDir, loaded, expected)
	if err != nil {
		return "", paperclipComparisonFailure(err)
	}
	if comparison.RulesExist {
		if _, err := compiler.LintAgentText(paperclipRulesRel, comparison.Rules); err != nil {
			return "", fmt.Errorf("[FAIL] Paperclip rules caveman lint: %w", err)
		}
	}
	return describePaperclipComparison(comparison), nil
}

// paperclipComparisonFailure is the audit failure for err, a failed paperclip.CompareGenerated,
// with the remedy that clears it.
func paperclipComparisonFailure(err error) error {
	switch {
	case errors.Is(err, paperclip.ErrHarnessStale):
		return fmt.Errorf("[FAIL] Paperclip harness out of date: %w; run '%s adopt', which refreshes unmodified earlier output without --force",
			err, util.PraetorCLI)
	case errors.Is(err, paperclip.ErrRulesDrift):
		return fmt.Errorf("[FAIL] Paperclip rules out of sync: %w; run '%s paperclip harness' to rewrite harness.json and rules.md "+
			"from the repository's facts (it replaces an edited harness.json too), or delete %s", err, util.PraetorCLI, paperclipRulesRel)
	}
	return fmt.Errorf("[FAIL] Paperclip harness comparison: %w", err)
}

// describePaperclipComparison states what a passing comparison verified.
func describePaperclipComparison(comparison paperclip.Comparison) string {
	harness := "synthesis for repository facts"
	if comparison.Owned {
		harness = "operator-owned harness.json, not this release's synthesis; adopt keeps it"
	}
	rules := "rules.md absent"
	if comparison.RulesExist {
		rules = "rules.md renders it, caveman lint passed"
	}
	return harness + "; " + rules
}
