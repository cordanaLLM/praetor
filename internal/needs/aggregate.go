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

// ErrNoRepositoryScanned is returned by the fleet aggregation when repositories were
// discovered under the fleet root but not a single one could be scanned. Without it an
// aggregation whose every scan failed would render as a perfect-coverage, zero-gap
// report and the CLI would exit 0.
var ErrNoRepositoryScanned = errors.New("needs: no discovered repository could be scanned")

// maxScanErrorsReported bounds how many per-repository scan failures are kept in the
// report, so a fleet root full of unreadable directories cannot grow the report without
// limit (HISS-02).
const maxScanErrorsReported = 64

// writef appends a formatted fragment to sb without an unchecked fmt.Fprintf error.
func writef(sb *strings.Builder, format string, args ...any) {
	fragment := fmt.Sprintf(format, args...)
	sb.WriteString(fragment)
}

// AggregateFleet scans all repositories in fleetRoot and produces a FleetDemandReport.
func AggregateFleet(ctx context.Context, fleetRoot, frameworkPath string) (*FleetDemandReport, error) {
	return AggregateFleetWithHarvest(ctx, fleetRoot, frameworkPath, "")
}

// AggregateFleetWithHarvest scans all repositories and incorporates harvested state.
//
// Repositories are classified against the framework checkout at frameworkPath, not
// against the static catalog alone, so pointing --framework at a checkout that does not
// ship a capability turns every dependency demanding it into a gap. Repositories present
// both on disk and in the harvest bundle are folded into a single leaderboard row.
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

	agg := newFleetAggregation(fleetRoot, fwIndex, len(repoDirs))
	for _, dir := range repoDirs {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("fleet scan aborted after %d of %d repositories: %w",
				agg.report.ScannedRepositories, len(repoDirs), ctxErr)
		}
		if err := agg.scanRepoDir(ctx, dir); err != nil {
			return nil, err
		}
	}

	if mErr := agg.mergeHarvest(ctx, harvestPath); mErr != nil {
		return nil, mErr
	}

	compileGapsAndLeaderboard(agg.report, agg.gapPackages)
	return agg.report, agg.result()
}

// fleetAggregation accumulates per-repository results while keeping repository identity
// unique across the on-disk scan and the harvest bundle.
type fleetAggregation struct {
	report      *FleetDemandReport
	framework   *FrameworkIndex
	gapPackages map[CapabilityKey]map[string]struct{}
	seen        map[string]struct{}
}

// newFleetAggregation prepares an aggregation over discovered repositories.
func newFleetAggregation(fleetRoot string, fwIndex *FrameworkIndex, discovered int) *fleetAggregation {
	return &fleetAggregation{
		report: &FleetDemandReport{
			GeneratedAt:         time.Now().UTC(),
			FleetRoot:           fleetRoot,
			Framework:           fwIndex.Name,
			TotalRepositories:   discovered,
			DemandFrequency:     make(map[CapabilityKey]int),
			CapabilityConsumers: make(map[CapabilityKey][]string),
		},
		framework:   fwIndex,
		gapPackages: make(map[CapabilityKey]map[string]struct{}),
		seen:        make(map[string]struct{}),
	}
}

// result reports whether the aggregation produced a usable report.
func (a *fleetAggregation) result() error {
	if a.report.TotalRepositories == 0 || a.report.ScannedRepositories > 0 {
		return nil
	}
	return fmt.Errorf("%w: %d discovered, %d skipped, %d failed: %s", ErrNoRepositoryScanned,
		a.report.TotalRepositories, len(a.report.SkippedRepositories), a.report.FailedRepositories,
		strings.Join(a.report.ScanErrors, "; "))
}

// scanRepoDir scans one repository and folds it into the report, recording the failure
// instead of dropping it silently. Cancellation aborts aggregation; unsupported
// repositories are skipped without claiming that their dependency coverage is known.
func (a *fleetAggregation) scanRepoDir(ctx context.Context, dir string) error {
	repoNeeds, err := ScanRepo(ctx, dir)
	if err != nil {
		if isContextError(err) {
			return fmt.Errorf("fleet aggregation interrupted at %q: %w", dir, err)
		}
		if errors.Is(err, ErrNoAnalyzer) {
			a.report.SkippedRepositories = append(a.report.SkippedRepositories, dir)
			return nil
		}
		a.report.FailedRepositories++
		if len(a.report.ScanErrors) < maxScanErrorsReported {
			a.report.ScanErrors = append(a.report.ScanErrors, fmt.Sprintf("%s: %v", dir, err))
		}
		return nil
	}
	a.add(repoNeeds, false)
	return nil
}

// isContextError identifies cancellation that must stop a fleet run.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// add folds one repository's needs into the report and reports whether it was new.
// countAsDiscovered adds the repository to TotalRepositories; the on-disk scan has
// already counted its repositories through the discovery walk.
func (a *fleetAggregation) add(repoNeeds *RepoNeeds, countAsDiscovered bool) bool {
	key := repoIdentityKey(repoNeeds.Repository)
	if key != "" {
		if _, dup := a.seen[key]; dup {
			return false
		}
		a.seen[key] = struct{}{}
	}

	applyFrameworkCoverage(a.framework, repoNeeds)
	if countAsDiscovered {
		a.report.TotalRepositories++
	}
	a.report.ScannedRepositories++
	a.report.Leaderboard = append(a.report.Leaderboard, *repoNeeds)

	for _, dep := range repoNeeds.Dependencies {
		a.recordDependency(repoNeeds.Repository, dep)
	}
	return true
}

// recordDependency counts one dependency demand and files it as a gap when unmet.
func (a *fleetAggregation) recordDependency(repository string, dep DependencyDemand) {
	a.report.DemandFrequency[dep.Capability]++
	a.report.CapabilityConsumers[dep.Capability] =
		appendUnique(a.report.CapabilityConsumers[dep.Capability], repository)

	if dep.Status != StatusGap {
		return
	}
	if _, ok := a.gapPackages[dep.Capability]; !ok {
		a.gapPackages[dep.Capability] = make(map[string]struct{})
	}
	a.gapPackages[dep.Capability][dep.Package] = struct{}{}
}

// mergeHarvest folds a harvest bundle into the report. A bundle that cannot be read is
// an error rather than a silently empty merge.
func (a *fleetAggregation) mergeHarvest(ctx context.Context, harvestPath string) error {
	if harvestPath == "" {
		return nil
	}
	if !util.DirExists(harvestPath) {
		return fmt.Errorf("harvest path %q is not a directory", harvestPath)
	}

	harvested, err := CodifyHarvestedInventory(ctx, harvestPath)
	if err != nil {
		return fmt.Errorf("failed to codify harvested inventory %q: %w", harvestPath, err)
	}

	for i := range harvested {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("harvest merge aborted: %w", ctxErr)
		}
		a.add(&harvested[i], true)
	}
	return nil
}

// repoIdentityKey normalises a repository identity so that the same repository scanned
// from disk (identified by its go.mod module path) and harvested from an inventory
// bundle (identified by its git remote) collapse onto one leaderboard row. Only the
// final segment survives, because a module path and a remote share no owner segment.
// An identity that carries no information returns "", which disables deduplication for
// that entry rather than merging unrelated repositories.
func repoIdentityKey(repository string) string {
	cleaned := util.CleanGitURL(strings.TrimSpace(repository))
	cleaned = strings.ReplaceAll(cleaned, "\\", "/")
	if idx := strings.LastIndex(cleaned, "/"); idx >= 0 {
		cleaned = cleaned[idx+1:]
	}
	if idx := strings.LastIndex(cleaned, ":"); idx >= 0 {
		cleaned = cleaned[idx+1:]
	}
	cleaned = strings.ToLower(strings.TrimSpace(cleaned))
	if cleaned == "unknown" {
		return ""
	}
	return cleaned
}

// applyFrameworkCoverage re-classifies catalog-covered dependencies against the framework
// the operator pointed at: a capability that framework does not ship is a gap regardless
// of what the static catalog claims. Without this step --framework would be inert and the
// fleet coverage number would be a property of the catalog alone.
func applyFrameworkCoverage(idx *FrameworkIndex, repoNeeds *RepoNeeds) {
	if idx == nil || repoNeeds == nil {
		return
	}

	demoted := false
	for i := range repoNeeds.Dependencies {
		dep := &repoNeeds.Dependencies[i]
		if dep.Status == StatusGap || idx.ProvidesCapability(dep.Capability) {
			continue
		}
		dep.Status = StatusGap
		dep.GolusorisReplacement = ""
		dep.Notes = fmt.Sprintf("%s does not provide capability %s", idx.Name, dep.Capability)
		demoted = true
	}

	if demoted {
		calculateReadiness(repoNeeds)
	}
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
		return nil, fmt.Errorf("walk fleet root %q: %w", root, err)
	}

	return repoDirs, nil
}

// isManifestFile excludes symlinks whose targets may be outside the fleet root.
func isManifestFile(info os.FileInfo) bool {
	if info == nil || info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false
	}
	name := info.Name()
	return name == "go.mod" || name == "package.json" || name == "pyproject.toml" ||
		name == "requirements.txt" || name == "Cargo.toml" || name == "meson.build" ||
		name == ".standards.yaml" || name == ".needs.yaml"
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
		pkgs := make([]string, 0, len(pkgsMap))
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

	// The capability tiebreak keeps the rendered table byte-identical across runs: the
	// gaps are collected from a map, and sort.Slice is not stable.
	sort.Slice(report.Gaps, func(i, j int) bool {
		if report.Gaps[i].ConsumerCount == report.Gaps[j].ConsumerCount {
			return report.Gaps[i].Capability < report.Gaps[j].Capability
		}
		return report.Gaps[i].ConsumerCount > report.Gaps[j].ConsumerCount
	})

	calculateFleetCoverage(report)
}

// calculateFleetCoverage computes the aggregate fleet-wide dependency coverage percentage.
// Coverage is only defined once at least one repository was scanned; an aggregation that
// scanned nothing reports CoverageKnown=false rather than a vacuous 100%.
func calculateFleetCoverage(report *FleetDemandReport) {
	if report.ScannedRepositories == 0 {
		report.CoverageKnown = false
		report.OverallFleetCoverage = 0
		return
	}

	totalDeps := 0
	coveredDeps := 0
	for _, repo := range report.Leaderboard {
		totalDeps += repo.Readiness.TotalThirdPartyDeps
		coveredDeps += repo.Readiness.CoveredDeps
	}

	report.CoverageKnown = true
	if totalDeps > 0 {
		report.OverallFleetCoverage = (float64(coveredDeps) / float64(totalDeps)) * 100.0
		return
	}
	report.OverallFleetCoverage = 100.0
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
	var sb strings.Builder
	if report == nil {
		sb.WriteString("# Framework Demand & Capability Report\n\nNo report was produced.\n")
		return sb.String()
	}

	sb.WriteString(renderDemandHeader(report))
	sb.WriteString(renderDemandTopography(report))
	sb.WriteString(renderDemandGaps(report))
	sb.WriteString(renderDemandLeaderboard(report))
	return sb.String()
}

// renderDemandHeader renders the report preamble, including the failure and coverage state.
func renderDemandHeader(report *FleetDemandReport) string {
	coverage := fmt.Sprintf("%.1f%%", report.OverallFleetCoverage)
	if !report.CoverageKnown {
		coverage = "unknown (no repository could be scanned)"
	}
	failed := ""
	if report.FailedRepositories > 0 {
		failed = fmt.Sprintf("**Repositories Failed**: %d  \n", report.FailedRepositories)
	}
	skipped := ""
	if len(report.SkippedRepositories) > 0 {
		skipped = fmt.Sprintf("**Repositories Skipped (no language analyzer matched)**: %d  \n", len(report.SkippedRepositories))
	}

	return fmt.Sprintf("# Framework Demand & Capability Report\n\n"+
		"**Target Framework**: `%s`  \n"+
		"**Generated At**: %s  \n"+
		"**Repositories Scanned**: %d / %d  \n"+
		"%s%s"+
		"**Overall Fleet Golusoris Coverage**: %s\n\n",
		report.Framework, report.GeneratedAt.Format(time.RFC3339),
		report.ScannedRepositories, report.TotalRepositories, failed, skipped, coverage)
}

// renderDemandTopography renders the capability demand-frequency table.
func renderDemandTopography(report *FleetDemandReport) string {
	var sb strings.Builder
	sb.WriteString("## Fleet Demand Topography\n\n")
	sb.WriteString("| Capability | Demand Frequency | Consumer Repositories |\n")
	sb.WriteString("| :--- | :--- | :--- |\n")

	type capFreq struct {
		key   CapabilityKey
		count int
	}
	freqs := make([]capFreq, 0, len(report.DemandFrequency))
	for k, v := range report.DemandFrequency {
		freqs = append(freqs, capFreq{k, v})
	}
	// The capability tiebreak keeps equal-frequency rows in a stable order across runs.
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

// renderDemandGaps renders the gap table, or the reason there is none to render.
func renderDemandGaps(report *FleetDemandReport) string {
	var sb strings.Builder
	sb.WriteString("\n## High-Priority Framework Gaps\n\n")
	if !report.CoverageKnown {
		sb.WriteString("No repository could be scanned; the gap analysis is unavailable.\n")
		return sb.String()
	}
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

// renderDemandLeaderboard renders the migration readiness leaderboard.
func renderDemandLeaderboard(report *FleetDemandReport) string {
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
