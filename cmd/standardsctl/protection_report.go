package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"
)

// protectionTags is the report tag of each verdict forge.EvaluateBranchProtection returns.
var protectionTags = map[forge.ProtectionVerdict]string{
	forge.ProtectionEnforced:    "[OK]",
	forge.ProtectionNotRequired: "[OK]",
	forge.ProtectionDrift:       "[DRIFT]",
	forge.ProtectionStricter:    "[STRICTER]",
}

// protectionTarget is the branch whose live protection is compared, and what it is compared
// with: the resolved branch protection policy and the status checks the ruleset requires there.
type protectionTarget struct {
	repository string
	branch     string
	policy     config.BranchProtectionPolicy
	contexts   []string
}

// reportLiveProtection reads what GitHub enforces on the target branch through gh, from its
// rulesets and its legacy protection object alike (forge.GitHubDriver.ReadBranchProtection),
// prints it against the declared policy under heading, and returns the declared properties the
// branch does not enforce. driftOnly prints only those, and the mechanisms found.
func reportLiveProtection(ctx context.Context, gh *forge.GitHubDriver, target protectionTarget, heading string, driftOnly bool) ([]string, error) {
	live, err := gh.ReadBranchProtection(ctx, target.branch)
	if err != nil {
		return nil, fmt.Errorf("read the live branch protection of %s: %w", target.branch, err)
	}
	findings, err := forge.EvaluateBranchProtection(target.policy, target.contexts, live)
	if err != nil {
		return nil, fmt.Errorf("compare the live branch protection of %s: %w", target.branch, err)
	}
	drifted := forge.ProtectionDrifts(findings)
	fmt.Printf("  [INFO] Live branch protection of %s on GitHub for %s, %s: %s\n",
		target.branch, target.repository, heading, describeMechanisms(live))
	stricter := false
	for i := 0; i < len(findings); i++ {
		finding := findings[i]
		stricter = stricter || finding.Verdict == forge.ProtectionStricter
		if !driftOnly || finding.Verdict == forge.ProtectionDrift {
			fmt.Println("    " + formatProtectionFinding(finding))
		}
	}
	if stricter && !driftOnly {
		fmt.Printf("    Settings marked [STRICTER] exceed the declared policy. sync --remote lowers each parameter it renders into ruleset %q "+
			"to the declared value and prints it as [LOWERED]; it leaves a rule it does not render, other rulesets and %s as they are, "+
			"so change those on GitHub by hand if the declared policy is intended\n", forge.RepositoryRulesetName, forge.LegacyProtectionMechanism)
	}
	return drifted, nil
}

// describeMechanisms names the mechanisms that protect the branch live describes, or says that
// none does, and says so when GitHub has no such branch yet.
func describeMechanisms(live *forge.LiveBranchProtection) string {
	mechanisms := live.Mechanisms()
	text := "protected by " + strings.Join(mechanisms, ", ")
	if len(mechanisms) == 0 {
		text = "no active ruleset rule applies to " + live.Branch + " and it has no " + forge.LegacyProtectionMechanism + " object"
	}
	if live.Missing {
		text += " (" + live.Branch + " does not exist on GitHub yet; a ruleset that targets it applies once it is pushed)"
	}
	return text
}

// printLoweredParameters names each rendered parameter a ruleset write lowered from a stricter
// live value to the declared one (forge.GitHubDriver.ReconcileProtectionReport).
func printLoweredParameters(lowered []forge.LoweredParameter) {
	for i := 0; i < len(lowered); i++ {
		fmt.Printf("  [LOWERED] Ruleset %q rule %s: %s was %v on GitHub, now %v as declared\n",
			forge.RepositoryRulesetName, lowered[i].Rule, lowered[i].Parameter, lowered[i].Live, lowered[i].Declared)
	}
}

// formatProtectionFinding renders one compared property as a report line.
func formatProtectionFinding(finding forge.ProtectionFinding) string {
	line := fmt.Sprintf("%s %s: declared %s, live %s", protectionTags[finding.Verdict], finding.Property, finding.Declared, finding.Live)
	if len(finding.EnforcedBy) > 0 {
		line += " by " + strings.Join(finding.EnforcedBy, ", ")
	}
	return line
}
