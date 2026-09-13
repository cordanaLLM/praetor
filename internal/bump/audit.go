// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
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

	pending, langScanned, err := scanLangDeps(ctx, repoPath, opts)
	if err != nil {
		return nil, err
	}
	actions, actionDeps, err := ScanWorkflowActions(repoPath)
	if err != nil {
		return nil, fmt.Errorf("audit workflow actions: %w", err)
	}
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

func scanLangDeps(ctx context.Context, repoPath string, opts ScanOptions) ([]UpgradeCandidate, int, error) {
	var pending []UpgradeCandidate
	total := 0
	for _, scan := range []func(context.Context, string, ScanOptions) ([]UpgradeCandidate, error){ScanGoDependencies, ScanNodeDependencies} {
		candidates, err := scan(ctx, repoPath, opts)
		if err != nil {
			return nil, 0, fmt.Errorf("audit language dependencies: %w", err)
		}
		total += len(candidates)
		for _, c := range candidates {
			if c.CurrentVersion != c.TargetVersion {
				pending = append(pending, c)
			}
		}
	}
	return pending, total, nil
}

func calculateAuditScore(total, pending, deps int) (int, float64) {
	upToDate := total - pending - deps
	if upToDate < 0 {
		upToDate = 0
	}
	score := 100.0
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
	for _, tool := range []string{"govulncheck", "gosec", "reuse", "lefthook"} {
		if err := probeToolchain(ctx, tool); err != nil {
			warnings = append(warnings, DeprecationWarning{
				Component: tool, Kind: "toolchain-missing",
				Details: fmt.Sprintf("Tool %s could not run: %v", tool, err),
			})
		}
	}
	return warnings
}

func probeToolchain(ctx context.Context, tool string) error {
	if _, err := util.RunCommand(ctx, "", tool, "--version"); err == nil {
		return nil
	}
	goPath, err := util.RunCommand(ctx, "", "go", "env", "GOPATH")
	if err != nil {
		return fmt.Errorf("resolve Go tool directory: %w", err)
	}
	for _, root := range filepath.SplitList(strings.TrimSpace(goPath)) {
		if _, err := util.RunCommand(ctx, "", filepath.Join(root, "bin", tool), "--version"); err == nil {
			return nil
		}
	}
	return fmt.Errorf("not runnable on PATH or under GOPATH/bin")
}
