package needs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// writef appends a formatted fragment to sb. The intermediate variable keeps call sites
// free of the WriteString(fmt.Sprintf(...)) pattern (staticcheck QF1012) without reaching
// for fmt.Fprintf, whose error return would then go unchecked (HISS-07).
func writef(sb *strings.Builder, format string, args ...any) {
	fragment := fmt.Sprintf(format, args...)
	sb.WriteString(fragment)
}

// AggregateFleet scans all repositories in fleetRoot and produces a FleetDemandReport.
func AggregateFleet(ctx context.Context, fleetRoot, frameworkPath string) (*FleetDemandReport, error) {
	return AggregateFleetWithHarvest(ctx, fleetRoot, frameworkPath, "")
}

// AggregateFleetWithHarvest scans all repositories and incorporates harvested state.
func AggregateFleetWithHarvest(ctx context.Context, fleetRoot, frameworkPath, harvestPath string) (*FleetDemandReport, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	fwIndex, err := InspectFramework(ctx, frameworkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect framework: %w", err)
	}

	repoDirs, err := discoverFleetRepos(ctx, fleetRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to discover fleet repositories: %w", err)
	}

	report := &FleetDemandReport{
		GeneratedAt:         time.Now().UTC(),
		FleetRoot:           fleetRoot,
		Framework:           fwIndex.Name,
		TotalRepositories:   len(repoDirs),
		DemandFrequency:     make(map[CapabilityKey]int),
		CapabilityConsumers: make(map[CapabilityKey][]string),
	}

	gapPackages := make(map[CapabilityKey]map[string]struct{})
	if err := scanFleetRepos(ctx, repoDirs, report, gapPackages); err != nil {
		return nil, err
	}
	if err := foldHarvestIntoReport(ctx, harvestPath, report, gapPackages); err != nil {
		return nil, err
	}

	compileGapsAndLeaderboard(report, gapPackages)
	return report, nil
}

// scanFleetRepos scans every discovered repository, aborting on cancellation and
// recording repositories no analyzer recognises as skipped rather than as fully ready.
func scanFleetRepos(ctx context.Context, repoDirs []string, report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) error {
	for _, dir := range repoDirs {
		// Without this check every repository left after the caller's deadline would be
		// "scanned" into an empty, 100% ready leaderboard entry.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("fleet aggregation interrupted at %q: %w", dir, ctxErr)
		}
		repoNeeds, err := ScanRepo(ctx, dir)
		if err != nil {
			if isContextError(err) {
				return fmt.Errorf("fleet aggregation interrupted at %q: %w", dir, err)
			}
			report.SkippedRepositories = append(report.SkippedRepositories, dir)
			continue
		}
		recordRepoNeeds(repoNeeds, report, gapPackages)
	}
	return nil
}

// foldHarvestIntoReport adds harvested inventory manifests to the report.
func foldHarvestIntoReport(ctx context.Context, harvestPath string, report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) error {
	if harvestPath == "" || !util.DirExists(harvestPath) {
		return nil
	}
	harvestNeeds, err := CodifyHarvestedInventory(ctx, harvestPath)
	if err != nil {
		return fmt.Errorf("failed to codify harvested inventory at %q: %w", harvestPath, err)
	}
	for i := range harvestNeeds {
		incorporateNeedsIntoReport(&harvestNeeds[i], report, gapPackages)
	}
	return nil
}

// isContextError reports whether err was caused by cancellation or a deadline, which must
// abort the whole fleet run rather than mark a single repository unanalysable.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// discoverFleetRepos searches up to depth 5 for repositories across all supported languages.
func discoverFleetRepos(ctx context.Context, root string) ([]string, error) {
	var repoDirs []string
	seen := make(map[string]struct{})
	maxDepth := 5
	root = filepath.Clean(root)

	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil && strings.Count(rel, string(os.PathSeparator)) > maxDepth {
			return filepath.SkipDir
		}
		if shouldSkipDir(info, path, root) {
			return filepath.SkipDir
		}
		if isManifestFile(info) {
			dir := filepath.Dir(path)
			if _, exists := seen[dir]; !exists {
				seen[dir] = struct{}{}
				repoDirs = append(repoDirs, dir)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to walk fleet root %q: %w", root, err)
	}

	return repoDirs, nil
}

func isManifestFile(info os.FileInfo) bool {
	if info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	name := info.Name()
	return name == "go.mod" || name == "package.json" || name == "pyproject.toml" ||
		name == "requirements.txt" || name == "Cargo.toml" || name == "meson.build" ||
		name == ".standards.yaml" || name == ".needs.yaml"
}

// incorporateNeedsIntoReport folds a manifest that was not part of the discovered fleet
// (a harvested repository) into the report, counting it as an extra repository.
func incorporateNeedsIntoReport(needs *RepoNeeds, report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) {
	report.TotalRepositories++
	recordRepoNeeds(needs, report, gapPackages)
}

// recordRepoNeeds updates the leaderboard, demand counters and gap index for one scanned
// repository.
func recordRepoNeeds(needs *RepoNeeds, report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) {
	report.ScannedRepositories++
	report.Leaderboard = append(report.Leaderboard, *needs)

	for _, dep := range needs.Dependencies {
		report.DemandFrequency[dep.Capability]++
		report.CapabilityConsumers[dep.Capability] = appendUnique(report.CapabilityConsumers[dep.Capability], needs.Repository)

		if dep.Status == StatusGap {
			if _, ok := gapPackages[dep.Capability]; !ok {
				gapPackages[dep.Capability] = make(map[string]struct{})
			}
			gapPackages[dep.Capability][dep.Package] = struct{}{}
		}
	}
}

// compileGapsAndLeaderboard sorts leaderboard and formats gap details.
func compileGapsAndLeaderboard(report *FleetDemandReport, gapPackages map[CapabilityKey]map[string]struct{}) {
	sort.Slice(report.Leaderboard, func(i, j int) bool {
		if report.Leaderboard[i].Readiness.Score == report.Leaderboard[j].Readiness.Score {
			return report.Leaderboard[i].Repository < report.Leaderboard[j].Repository
		}
		return report.Leaderboard[i].Readiness.Score > report.Leaderboard[j].Readiness.Score
	})

	for capKey, pkgsMap := range gapPackages {
		consumers := report.CapabilityConsumers[capKey]
		var pkgs []string
		for pkg := range pkgsMap {
			pkgs = append(pkgs, pkg)
		}
		sort.Strings(pkgs)

		report.Gaps = append(report.Gaps, GapDetail{
			Capability:    capKey,
			ConsumerCount: len(consumers),
			Consumers:     consumers,
			PackagesUsed:  pkgs,
		})
	}

	sort.Slice(report.Gaps, func(i, j int) bool {
		if report.Gaps[i].ConsumerCount == report.Gaps[j].ConsumerCount {
			return report.Gaps[i].Capability < report.Gaps[j].Capability
		}
		return report.Gaps[i].ConsumerCount > report.Gaps[j].ConsumerCount
	})

	calculateFleetCoverage(report)
}

// calculateFleetCoverage computes the aggregate fleet-wide dependency coverage percentage.
func calculateFleetCoverage(report *FleetDemandReport) {
	totalDeps := 0
	coveredDeps := 0

	for _, repo := range report.Leaderboard {
		totalDeps += repo.Readiness.TotalThirdPartyDeps
		coveredDeps += repo.Readiness.CoveredDeps
	}

	if totalDeps > 0 {
		report.OverallFleetCoverage = (float64(coveredDeps) / float64(totalDeps)) * 100.0
	} else {
		report.OverallFleetCoverage = 100.0
	}
}

// appendUnique appends a string to a slice if not already present.
func appendUnique(slice []string, val string) []string {
	for _, item := range slice {
		if item == val {
			return slice
		}
	}
	return append(slice, val)
}

// RenderFrameworkDemandMarkdown outputs the FleetDemandReport in Keep-a-Changelog compatible markdown.
func RenderFrameworkDemandMarkdown(report *FleetDemandReport) string {
	return renderReportHeader(report) +
		renderDemandTopography(report) +
		renderGapTable(report) +
		renderLeaderboard(report)
}

// renderReportHeader writes the report preamble.
func renderReportHeader(report *FleetDemandReport) string {
	skipped := ""
	if len(report.SkippedRepositories) > 0 {
		skipped = fmt.Sprintf("**Repositories Skipped (no language analyzer matched)**: %d  \n",
			len(report.SkippedRepositories))
	}
	return fmt.Sprintf("# Framework Demand & Capability Report\n\n"+
		"**Target Framework**: `%s`  \n"+
		"**Generated At**: %s  \n"+
		"**Repositories Scanned**: %d / %d  \n",
		report.Framework, report.GeneratedAt.Format(time.RFC3339),
		report.ScannedRepositories, report.TotalRepositories) +
		skipped +
		fmt.Sprintf("**Overall Fleet Golusoris Coverage**: %.1f%%\n\n", report.OverallFleetCoverage)
}

// capFreq pairs a capability with the number of repositories demanding it.
type capFreq struct {
	key   CapabilityKey
	count int
}

// renderDemandTopography writes the capability demand table, ordered by demand and then
// by capability name so the output is stable across runs.
func renderDemandTopography(report *FleetDemandReport) string {
	var sb strings.Builder
	sb.WriteString("## Fleet Demand Topography\n\n")
	sb.WriteString("| Capability | Demand Frequency | Consumer Repositories |\n")
	sb.WriteString("| :--- | :--- | :--- |\n")

	freqs := make([]capFreq, 0, len(report.DemandFrequency))
	for k, v := range report.DemandFrequency {
		freqs = append(freqs, capFreq{k, v})
	}
	sort.Slice(freqs, func(i, j int) bool {
		if freqs[i].count == freqs[j].count {
			return freqs[i].key < freqs[j].key
		}
		return freqs[i].count > freqs[j].count
	})

	for _, f := range freqs {
		consumers := report.CapabilityConsumers[f.key]
		shortConsumers := make([]string, len(consumers))
		for i, c := range consumers {
			shortConsumers[i] = util.CleanGitURL(c)
		}
		writef(&sb, "| `%s` | %d | %s |\n", f.key, f.count, strings.Join(shortConsumers, ", "))
	}
	return sb.String()
}

// renderGapTable writes the prioritised framework gap table.
func renderGapTable(report *FleetDemandReport) string {
	var sb strings.Builder
	sb.WriteString("\n## High-Priority Framework Gaps\n\n")
	if len(report.Gaps) == 0 {
		sb.WriteString("Zero gaps identified! All downstream dependencies are covered by Golusoris.\n")
		return sb.String()
	}
	sb.WriteString("| Capability Gap | Impacted Repos | Underlying Packages |\n")
	sb.WriteString("| :--- | :--- | :--- |\n")
	for _, g := range report.Gaps {
		writef(&sb, "| `%s` | %d | `%s` |\n",
			g.Capability, g.ConsumerCount, strings.Join(g.PackagesUsed, "`, `"))
	}
	return sb.String()
}

// renderLeaderboard writes the migration readiness ranking.
func renderLeaderboard(report *FleetDemandReport) string {
	var sb strings.Builder
	sb.WriteString("\n## Migration Readiness Leaderboard\n\n")
	sb.WriteString("| Rank | Repository | Readiness Score | Covered Deps | Gaps |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- |\n")
	for i, repo := range report.Leaderboard {
		writef(&sb, "| #%d | `%s` | %.1f%% | %d | %d |\n",
			i+1, repo.Repository, repo.Readiness.Score, repo.Readiness.CoveredDeps, repo.Readiness.GapDeps)
	}
	return sb.String()
}
