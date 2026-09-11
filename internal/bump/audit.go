// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"fmt"
	"os/exec"
)

// AuditCodebaseVersions performs a comprehensive audit across all dependencies, actions, and tools.
func AuditCodebaseVersions(ctx context.Context, repoPath string, includePrerelease bool) (*VersionAuditReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("bump audit: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("bump audit cancelled: %w", err)
	}

	opts := ScanOptions{
		IncludePrerelease: includePrerelease,
		MaxCandidates:     1000,
	}

	pending, langScanned := scanLangDeps(ctx, repoPath, opts)
	actions, actionDeps, _ := ScanWorkflowActions(repoPath)
	toolDeps := auditToolchains(ctx)

	deprecations := append(actionDeps, toolDeps...)
	totalScanned := langScanned + len(actions)
	upToDate, score := calculateAuditScore(totalScanned, len(pending), len(deprecations))

	return &VersionAuditReport{
		TotalScanned:       totalScanned,
		UpToDate:           upToDate,
		PendingUpgrades:    pending,
		Actions:            actions,
		Deprecations:       deprecations,
		ModernizationScore: score,
		Passed:             len(deprecations) == 0,
	}, nil
}

func scanLangDeps(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, int) {
	var pending []UpgradeCandidate
	total := 0

	if goC, err := ScanGoDependencies(ctx, repoPath, opts); err == nil {
		total += len(goC)
		for _, c := range goC {
			if c.CurrentVersion != c.TargetVersion {
				pending = append(pending, c)
			}
		}
	}
	if nodeC, err := ScanNodeDependencies(ctx, repoPath, opts); err == nil {
		total += len(nodeC)
		for _, c := range nodeC {
			if c.CurrentVersion != c.TargetVersion {
				pending = append(pending, c)
			}
		}
	}
	return pending, total
}

func calculateAuditScore(total, pending, deps int) (int, float64) {
	upToDate := total - pending - deps
	if upToDate < 0 {
		upToDate = 0
	}
	var score float64 = 100.0
	if total > 0 {
		score = (float64(upToDate) / float64(total)) * 100.0
		if score < 0 {
			score = 0
		}
	}
	return upToDate, score
}

func auditToolchains(ctx context.Context) []DeprecationWarning {
	var warnings []DeprecationWarning
	tools := []string{"govulncheck", "gosec", "reuse", "lefthook"}

	for _, tool := range tools {
		cmd := exec.CommandContext(ctx, tool, "--version")
		if err := cmd.Run(); err != nil {
			// Check if binary is in go/bin
			goCmd := exec.CommandContext(ctx, "go", "env", "GOPATH")
			if _, goErr := goCmd.Output(); goErr != nil {
				warnings = append(warnings, DeprecationWarning{
					Component: tool,
					Kind:      "toolchain-missing",
					Details:   fmt.Sprintf("Tool %s is not available on PATH", tool),
				})
			}
		}
	}

	return warnings
}
