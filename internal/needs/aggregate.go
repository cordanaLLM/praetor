package needs

import (
	"context"
	"errors"
	"fmt"
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
func AggregateFleet(ctx context.Context, fleetRoot string, framework FrameworkSource, registry *AnalyzerRegistry) (*FleetDemandReport, error) {
	return AggregateFleetWithHarvest(ctx, fleetRoot, framework, "", registry)
}

// AggregateFleetWithHarvest scans all repositories and incorporates harvested state.
//
// Repositories are reconciled against the framework the source selects, so pointing
// --framework at a checkout that does not ship a capability turns every dependency
// demanding it into a gap. Repositories present both on disk and in the harvest bundle
// are folded into a single leaderboard row. registry supplies the analyzers and the
// framework targets; nil selects DefaultRegistry.
func AggregateFleetWithHarvest(ctx context.Context, fleetRoot string, framework FrameworkSource, harvestPath string,
	registry *AnalyzerRegistry) (*FleetDemandReport, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	fwIndex, err := InspectFramework(ctx, framework)
	if err != nil {
		return nil, fmt.Errorf("failed to inspect framework: %w", err)
	}

	layout, err := discoverFleet(ctx, fleetRoot)
	if err != nil {
		return nil, fmt.Errorf("failed to discover fleet repositories: %w", err)
	}

	agg := newFleetAggregation(fleetRoot, fwIndex, len(layout.repos))
	agg.registry = registryOrDefault(registry)
	agg.report.DuplicateCheckouts = layout.duplicates
	for _, repo := range layout.repos {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("fleet scan aborted after %d of %d repositories: %w",
				agg.report.ScannedRepositories, len(layout.repos), ctxErr)
		}
		if err := agg.scanRepo(ctx, repo); err != nil {
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
	registry    *AnalyzerRegistry
	report      *FleetDemandReport
	framework   *FrameworkIndex
	gapPackages map[CapabilityKey]map[string]struct{}
	seen        map[string]struct{}
	// names maps each repository name on the leaderboard to the path of its first row.
	names map[string]string
}

// newFleetAggregation prepares an aggregation over discovered repositories.
func newFleetAggregation(fleetRoot string, fwIndex *FrameworkIndex, discovered int) *fleetAggregation {
	return &fleetAggregation{
		report: &FleetDemandReport{
			GeneratedAt:         time.Now().UTC(),
			FleetRoot:           fleetRoot,
			Framework:           fwIndex.Name,
			CoverageBasis:       fwIndex.Basis,
			TotalRepositories:   discovered,
			DemandFrequency:     make(map[CapabilityKey]int),
			CapabilityConsumers: make(map[CapabilityKey][]string),
		},
		framework:   fwIndex,
		gapPackages: make(map[CapabilityKey]map[string]struct{}),
		seen:        make(map[string]struct{}),
		names:       make(map[string]string),
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

// scanRepo scans one discovered repository through scanRepository and folds it into the
// report, recording the failure instead of dropping it silently. Cancellation aborts
// aggregation; unsupported repositories are skipped without claiming that their
// dependency coverage is known. Manifests beyond the sub-project depth bound are listed
// whatever the scan's outcome; sub-projects whose scan failed are listed with their error.
func (a *fleetAggregation) scanRepo(ctx context.Context, repo *fleetRepo) error {
	a.report.UnscannedSubprojects = append(a.report.UnscannedSubprojects, repo.unscanned...)
	repoNeeds, err := scanRepository(ctx, repo, a.registry)
	if err != nil {
		if isContextError(err) {
			return fmt.Errorf("fleet aggregation interrupted at %q: %w", repo.root, err)
		}
		if errors.Is(err, ErrNoAnalyzer) {
			a.report.SkippedRepositories = append(a.report.SkippedRepositories, repo.root)
			return nil
		}
		a.report.FailedRepositories++
		if len(a.report.ScanErrors) < maxScanErrorsReported {
			a.report.ScanErrors = append(a.report.ScanErrors, fmt.Sprintf("%s: %v", repo.root, err))
		}
		return nil
	}
	repoNeeds.Path = repo.root
	for _, failure := range repoNeeds.FailedSubprojects {
		a.report.FailedSubprojects = append(a.report.FailedSubprojects,
			SubprojectFailure{Dir: filepath.Join(repo.root, filepath.FromSlash(failure.Dir)), Error: failure.Error})
	}
	if key := repoIdentityKey(repoNeeds.Repository); key != "" {
		a.seen[key] = struct{}{}
	}
	a.add(repoNeeds)
	return nil
}

// isContextError identifies cancellation that must stop a fleet run.
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// addHarvested folds one harvested repository into the report unless a repository of the
// same identity is already on it, and reports whether it was new. A harvested repository
// is counted as discovered; the on-disk scan counted its repositories through the walk.
func (a *fleetAggregation) addHarvested(repoNeeds *RepoNeeds) bool {
	key := repoIdentityKey(repoNeeds.Repository)
	if key != "" {
		if _, dup := a.seen[key]; dup {
			return false
		}
		a.seen[key] = struct{}{}
	}
	a.report.TotalRepositories++
	a.add(repoNeeds)
	return true
}

// add scores one repository against the framework and puts it on the leaderboard. Rows
// discovered on disk are distinct repositories whatever they are named, so none is ever
// merged away; a name two rows share is qualified by location in the consumer lists.
func (a *fleetAggregation) add(repoNeeds *RepoNeeds) {
	applyFrameworkCoverage(a.framework, repoNeeds, a.registry)
	a.report.ScannedRepositories++
	a.report.Leaderboard = append(a.report.Leaderboard, *repoNeeds)

	consumer := a.consumerLabel(repoNeeds)
	for _, dep := range repoNeeds.Dependencies {
		a.recordDependency(consumer, dep)
	}
}

// consumerLabel names a row in the capability consumer lists: its repository name, or,
// when an earlier row at another location already carries that name, the name qualified
// by the row's location below the fleet root.
func (a *fleetAggregation) consumerLabel(repoNeeds *RepoNeeds) string {
	name := repoNeeds.Repository
	first, taken := a.names[name]
	if !taken {
		a.names[name] = repoNeeds.Path
		return name
	}
	if first == repoNeeds.Path {
		return name
	}
	return fmt.Sprintf("%s (%s)", name, rowLocation(a.report.FleetRoot, repoNeeds.Path))
}

// rowLocation renders a row's path relative to the fleet root, "." for the root itself
// and "harvest" for a row that came from a harvest bundle.
func rowLocation(fleetRoot, path string) string {
	if path == "" {
		return "harvest"
	}
	rel, err := filepath.Rel(fleetRoot, path)
	if err != nil {
		return path
	}
	return filepath.ToSlash(rel)
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

	harvested, err := CodifyHarvestedInventory(ctx, harvestPath, a.registry.Targets())
	if err != nil {
		return fmt.Errorf("failed to codify harvested inventory %q: %w", harvestPath, err)
	}

	for i := range harvested {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return fmt.Errorf("harvest merge aborted: %w", ctxErr)
		}
		if harvested[i].Language == LanguageUnsupported {
			a.skipUnsupported(harvested[i].Repository)
			continue
		}
		a.addHarvested(&harvested[i])
	}
	return nil
}

// skipUnsupported counts a harvested repository no language signal identified as
// discovered and lists it as skipped, the way an on-disk repository with no matching
// analyzer is reported. An identity already seen is not counted twice.
func (a *fleetAggregation) skipUnsupported(repository string) {
	if key := repoIdentityKey(repository); key != "" {
		if _, dup := a.seen[key]; dup {
			return
		}
		a.seen[key] = struct{}{}
	}
	a.report.TotalRepositories++
	a.report.SkippedRepositories = append(a.report.SkippedRepositories, repository)
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

// applyFrameworkCoverage reconciles every demand against the framework the operator
// selected: a capability that framework does not declare is a gap. Without this step
// --framework would be inert and the fleet coverage number would say nothing about it.
//
// A demand of a language whose target declares a contract (LoadRegistry) is reconciled
// against that contract instead; idx is the go framework. Selected standard-library imports
// take the retained role the framework declares for them, and never count as demand.
func applyFrameworkCoverage(idx *FrameworkIndex, repoNeeds *RepoNeeds, registry *AnalyzerRegistry) {
	if idx == nil || repoNeeds == nil {
		return
	}
	registry = registryOrDefault(registry)
	for i := range repoNeeds.Dependencies {
		reconcileDependency(registry.frameworkFor(repoNeeds.Dependencies[i].Language, idx), &repoNeeds.Dependencies[i])
	}
	reconcileStandardImports(idx, repoNeeds)
	calculateReadiness(repoNeeds)
	repoNeeds.Framework = idx.Name
	repoNeeds.Readiness.Basis = idx.Basis
}

// reconcileStandardImports applies the foundation, wrapper or tooling role idx declares for
// each selected standard-library import.
func reconcileStandardImports(idx *FrameworkIndex, repoNeeds *RepoNeeds) {
	if idx == nil {
		return
	}
	for i := range repoNeeds.StandardLibraryImports {
		applyContractRelationship(idx, &repoNeeds.StandardLibraryImports[i])
	}
}

// reconcileDependency classifies one demand against the selected framework: the
// framework's own modules are native, a declared relationship keeps its retained role, a
// capability contract maps replacements and adapters, and a demand an earlier framework
// mapped keeps its package only when the selected framework lists it. Anything else is a
// gap.
func reconcileDependency(idx *FrameworkIndex, dep *DependencyDemand) {
	if isFrameworkModule(idx, dep.Package) {
		markFrameworkNative(idx, dep)
		return
	}
	if reconcileLibraryRelationship(idx, dep) || reconcileContractDemand(idx, dep) {
		return
	}
	if dep.Status == StatusGap {
		return
	}
	if _, listed := idx.Packages[dep.FrameworkReplacement]; listed {
		return
	}
	dep.Status, dep.FrameworkReplacement = StatusGap, ""
	dep.Notes = undeclaredNote(idx.Name, "package", dep.Capability)
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
	sb.WriteString(renderDemandDiscovery(report))
	return sb.String()
}

// renderDemandHeader renders the report preamble, including the failure and coverage state.
// A fleet scored against no framework says so instead of a coverage percentage.
func renderDemandHeader(report *FleetDemandReport) string {
	coverage := fmt.Sprintf("%.1f%%", report.OverallFleetCoverage)
	framework := "`" + report.Framework + "`"
	switch {
	case !report.CoverageKnown:
		coverage = "unknown (no repository could be scanned)"
	case report.CoverageBasis == FrameworkNotConfigured:
		coverage = mappingNotConfigured
	}
	if report.CoverageBasis == FrameworkNotConfigured {
		framework = FrameworkNotConfiguredText
	}

	return fmt.Sprintf("# Framework Demand & Capability Report\n\n"+
		"**Target Framework**: %s  \n"+
		"**Coverage Basis**: %s; builds and tests not run  \n"+
		"**Generated At**: %s  \n"+
		"**Repositories Scanned**: %d / %d  \n"+
		"%s"+
		"**Overall Fleet Target Framework Coverage**: %s\n\n",
		framework, report.CoverageBasis, report.GeneratedAt.Format(time.RFC3339),
		report.ScannedRepositories, report.TotalRepositories, renderDemandHeaderCounts(report), coverage)
}

// renderDemandHeaderCounts renders the preamble lines for everything discovered but not
// scored: failed and skipped repositories, collapsed worktrees, and unscanned and failed
// sub-projects.
func renderDemandHeaderCounts(report *FleetDemandReport) string {
	var sb strings.Builder
	if report.FailedRepositories > 0 {
		writef(&sb, "**Repositories Failed**: %d  \n", report.FailedRepositories)
	}
	if len(report.SkippedRepositories) > 0 {
		writef(&sb, "**Repositories Skipped (no language analyzer matched)**: %d  \n", len(report.SkippedRepositories))
	}
	if len(report.DuplicateCheckouts) > 0 {
		writef(&sb, "**Linked Worktrees Collapsed**: %d  \n", len(report.DuplicateCheckouts))
	}
	if len(report.UnscannedSubprojects) > 0 {
		writef(&sb, "**Sub-projects Not Scanned (more than %d directories below their repository root)**: %d  \n",
			maxSubprojectDepth, len(report.UnscannedSubprojects))
	}
	if len(report.FailedSubprojects) > 0 {
		writef(&sb, "**%s**: %d  \n", failedSubprojectsTitle, len(report.FailedSubprojects))
	}
	return sb.String()
}

// failedSubprojectsTitle heads the report lines for sub-projects whose scan failed.
const failedSubprojectsTitle = "Sub-projects Failed (scan error; the rest of each repository was scored)"

// renderDemandDiscovery lists every discovered directory the leaderboard does not rank,
// each with why, so that no repository or sub-project leaves the report without a trace.
func renderDemandDiscovery(report *FleetDemandReport) string {
	var sb strings.Builder
	writeLocations := func(title string, paths []string) {
		if len(paths) == 0 {
			return
		}
		writef(&sb, "\n## %s\n\n", title)
		for _, path := range paths {
			writef(&sb, "- `%s`\n", rowLocation(report.FleetRoot, path))
		}
	}
	writeLocations("Skipped Repositories (no language analyzer matched)", report.SkippedRepositories)
	writeLocations(fmt.Sprintf("Sub-projects Not Scanned (more than %d directories below their repository root)",
		maxSubprojectDepth), report.UnscannedSubprojects)
	if len(report.FailedSubprojects) > 0 {
		writef(&sb, "\n## %s\n\n", failedSubprojectsTitle)
		for _, failure := range report.FailedSubprojects {
			writef(&sb, "- `%s`: %s\n", rowLocation(report.FleetRoot, failure.Dir), strings.ReplaceAll(failure.Error, "\n", " "))
		}
	}
	if len(report.DuplicateCheckouts) > 0 {
		sb.WriteString("\n## Linked Worktrees Collapsed\n\n")
		for _, dup := range report.DuplicateCheckouts {
			writef(&sb, "- `%s` is a linked-worktree checkout of `%s`\n",
				rowLocation(report.FleetRoot, dup.Dir), rowLocation(report.FleetRoot, dup.Of))
		}
	}
	return sb.String()
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
		sb.WriteString("Zero gaps identified! All downstream dependencies are covered by the target framework.\n")
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
	sb.WriteString("| Rank | Repository | Location | Readiness Score | Covered Deps | Gaps |\n")
	sb.WriteString("| :--- | :--- | :--- | :--- | :--- | :--- |\n")
	for i, repo := range report.Leaderboard {
		writef(&sb, "| #%d | `%s` | `%s` | %.1f%% | %d | %d |\n",
			i+1, repo.Repository, rowLocation(report.FleetRoot, repo.Path),
			repo.Readiness.Score, repo.Readiness.CoveredDeps, repo.Readiness.GapDeps)
	}
	return sb.String()
}
