// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package bump

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/needs"
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

	inventory, err := scanInventory(ctx, repoPath, ScanOptions{IncludePrerelease: includePrerelease})
	if err != nil {
		return nil, fmt.Errorf("audit language dependencies: %w", err)
	}
	pending := pendingUpgrades(inventory)
	actions, actionDeps, err := ScanWorkflowActions(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("audit workflow actions: %w", err)
	}
	toolDeps := auditToolchains(ctx)
	unexamined := unexaminedManifests(repoPath)

	deprecations := slices.Concat(actionDeps, toolDeps, unexamined)
	totalScanned := len(inventory) + len(actions)
	upToDate, score := calculateAuditScore(totalScanned, len(pending), len(actionDeps)+len(toolDeps), len(unexamined))

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

// scannedLanguages are the needs analyzer languages whose declared dependencies
// languageScanners examine.
var scannedLanguages = map[string]bool{"go": true, "typescript": true}

// unexaminedManifests reports each ecosystem the repository declares dependencies for
// that bump has no scanner for. Its dependencies were not examined, so the audit must not
// read as a full-coverage pass: each one is an unsupported-manifest deprecation, which
// fails the audit and keeps the score below 100 percent.
func unexaminedManifests(repoPath string) []DeprecationWarning {
	var warnings []DeprecationWarning
	for _, analyzer := range needs.DefaultRegistry().DetectAll(repoPath) {
		if scannedLanguages[analyzer.Language()] {
			continue
		}
		warnings = append(warnings, DeprecationWarning{
			Component: analyzer.Language(),
			Kind:      "unsupported-manifest",
			Details:   fmt.Sprintf("%s dependencies are declared but were not examined: bump scans only Go and Node manifests", analyzer.Language()),
		})
	}
	return warnings
}

// calculateAuditScore scores the scanned components. deps is the count of deprecations
// among or about them, which are not up to date; unexamined is the count of ecosystems
// whose dependencies were never scanned. Each unexamined ecosystem joins the denominator
// as one component that is not up to date, so a repository whose only manifests are
// unexamined scores 0, not a vacuous 100.
func calculateAuditScore(total, pending, deps, unexamined int) (int, float64) {
	upToDate := max(total-pending-deps, 0)
	if total+unexamined == 0 {
		return upToDate, 100.0
	}
	return upToDate, float64(upToDate) / float64(total+unexamined) * 100.0
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
