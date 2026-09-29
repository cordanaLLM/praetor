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
		target.branch, target.repository, heading, describeMechanisms(target.branch, live.Mechanisms()))
	stricter := false
	for i := 0; i < len(findings); i++ {
		finding := findings[i]
		stricter = stricter || finding.Verdict == forge.ProtectionStricter
		if !driftOnly || finding.Verdict == forge.ProtectionDrift {
			fmt.Println("    " + formatProtectionFinding(finding))
		}
	}
	if stricter && !driftOnly {
		fmt.Println("    Settings marked [STRICTER] are kept: sync --remote never lowers a live setting below the declared policy; lower it on GitHub by hand if that is intended")
	}
	return drifted, nil
}

// describeMechanisms names the mechanisms that protect branch, or says that none does.
func describeMechanisms(branch string, mechanisms []string) string {
	if len(mechanisms) == 0 {
		return "no active ruleset rule applies to " + branch + " and it has no " + forge.LegacyProtectionMechanism + " object"
	}
	return "protected by " + strings.Join(mechanisms, ", ")
}

// formatProtectionFinding renders one compared property as a report line.
func formatProtectionFinding(finding forge.ProtectionFinding) string {
	line := fmt.Sprintf("%s %s: declared %s, live %s", protectionTags[finding.Verdict], finding.Property, finding.Declared, finding.Live)
	if len(finding.EnforcedBy) > 0 {
		line += " by " + strings.Join(finding.EnforcedBy, ", ")
	}
	return line
}
