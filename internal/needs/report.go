package needs

import (
	"errors"
	"fmt"
	"strings"
)

// FrameworkNotConfiguredText is how reports name a framework no source selects (ADR-0014 §4).
const FrameworkNotConfiguredText = "not configured (set framework.targets.<lang>.module and .contract, or pass --framework)"

// nothingToRewrite is the migration blocker, and the refusal of an application, when no
// target framework is configured.
const nothingToRewrite = "nothing to rewrite: no target framework configured"

// ErrFrameworkNotConfigured is returned by the operations that need a target framework
// (migration application, epic publishing) when none is configured.
var ErrFrameworkNotConfigured = errors.New("needs: no target framework configured " +
	"(set framework.targets.<lang>.module and .contract, or pass --framework)")

// mappingNotConfigured is the mapping availability of a row scored against no framework: a
// percentage would read as a finding about the repository.
const mappingNotConfigured = "n/a (no target framework configured)"

// MappingAvailability renders a row's mapping availability for CLI and MCP output: the
// percentage of third-party dependencies with a mapping, or n/a when no framework is
// configured (readiness basis not-configured), never 0%.
func MappingAvailability(readiness ReadinessMetrics) string {
	if readiness.Basis == FrameworkNotConfigured {
		return mappingNotConfigured
	}
	return fmt.Sprintf("%.1f%%", readiness.Score)
}

// FormatReportHeader renders the header the CLI `needs report` and the MCP
// standards_needs_report share: the repository, the framework and its mapping availability,
// the coverage basis, the contract the inventory came from, the dependencies resolved as
// framework non-goals and any deprecated input.
func FormatReportHeader(report *RepoNeeds, index *FrameworkIndex) string {
	if report == nil || index == nil {
		return "Framework migration report unavailable.\n"
	}
	var sb strings.Builder
	writef(&sb, "=== Framework Migration Report: %s ===\n", report.Repository)
	sb.WriteString(FormatRepositoryFallback(report))
	if index.Basis == FrameworkNotConfigured {
		writef(&sb, "Framework: %s | Mapping availability: %s\n\n", FrameworkNotConfiguredText, mappingNotConfigured)
	} else {
		writef(&sb, "Framework: %s (%s) | Mapping availability: %s\n\n", index.Name, index.Version, MappingAvailability(report.Readiness))
	}
	writef(&sb, "Coverage basis: %s; builds and tests not run\n", index.Basis)
	if index.Contract != "" {
		observation := "declared packages source-observed"
		if index.Basis != FrameworkSourceObserved {
			observation = "declared packages, not source-observed"
		}
		writef(&sb, "Capability contract: %s (%s)\n", index.Contract, observation)
	}
	if count := report.Readiness.NonGoalDeps; count > 0 {
		writef(&sb, "Framework non-goals: %d dependencies resolved as declared non-goals, counted as mapped\n", count)
	}
	sb.WriteString(FormatDeprecations(report))
	sb.WriteString("\n")
	return sb.String()
}

// FormatRepositoryFallback renders the "Repository name:" line of a row named after its
// directory (RepoNeeds.RepositoryFallback), or nothing for a row a manifest or the origin
// remote names. `needs scan`, `needs report` and the MCP standards_needs_report print it
// under their header.
func FormatRepositoryFallback(report *RepoNeeds) string {
	note := repositoryFallbackNote(report)
	if note == "" {
		return ""
	}
	return "Repository name: " + note + "\n"
}

// repositoryFallbackNote says that a row is named after its directory, and why, or returns
// "" for a row a manifest or the origin remote names. The pre-migration epic's checklist
// carries the same note. The reason names no local path.
func repositoryFallbackNote(report *RepoNeeds) string {
	if report == nil || report.RepositoryFallback == "" {
		return ""
	}
	return fmt.Sprintf("`%s` is the repository directory's name, which differs between clones, worktrees and CI workspaces (%s)",
		report.Repository, report.RepositoryFallback)
}

// FormatDeprecations renders one "Deprecated input:" line per deprecated input the row was
// read from, or nothing.
func FormatDeprecations(report *RepoNeeds) string {
	if report == nil {
		return ""
	}
	var sb strings.Builder
	for _, deprecation := range report.Deprecations {
		writef(&sb, "Deprecated input: %s\n", deprecation)
	}
	return sb.String()
}

// FrameworkDisplay names a framework module in CLI output, or says none is configured.
func FrameworkDisplay(module string) string {
	if module == "" {
		return "not configured"
	}
	return module
}

// FormatLibraryRelationships renders the shared CLI/MCP relationship table.
// Availability describes catalog/native roles or inspected related packages,
// never interchangeable APIs. Standard-library observations have separate counts.
func FormatLibraryRelationships(report *RepoNeeds) string {
	if report == nil {
		return "Library relationship report unavailable.\n"
	}
	var output strings.Builder
	output.WriteString("Library relationships and migration candidates:\n")
	for _, dependency := range report.Dependencies {
		writeLibraryRelationship(&output, dependency)
	}
	if len(report.StandardLibraryImports) > 0 {
		output.WriteString("\nSelected standard-library imports (excluded from third-party counts):\n")
		for _, dependency := range report.StandardLibraryImports {
			writeLibraryRelationship(&output, dependency)
		}
	}
	writeSubprojects(&output, report)
	return output.String()
}

// writeSubprojects lists the nested sub-projects merged into the report, those beyond the
// depth bound that were not scanned and those whose scan failed, so a scan never presents
// a partial repository as a complete one.
func writeSubprojects(output *strings.Builder, report *RepoNeeds) {
	if len(report.Subprojects) > 0 {
		fmt.Fprintf(output, "\nNested sub-projects scanned into this report: %s\n", strings.Join(report.Subprojects, ", "))
	}
	if len(report.UnscannedSubprojects) > 0 {
		fmt.Fprintf(output, "\nSub-projects NOT scanned (more than %d directories below the repository root): %s\n",
			maxSubprojectDepth, strings.Join(report.UnscannedSubprojects, ", "))
	}
	if len(report.FailedSubprojects) > 0 {
		fmt.Fprintf(output, "\nSub-projects that FAILED to scan (their demand is missing from this report): %s\n",
			formatSubprojectFailures(report.FailedSubprojects))
	}
}

// formatSubprojectFailures renders failed sub-projects as "dir (error)" joined by "; ".
func formatSubprojectFailures(failures []SubprojectFailure) string {
	parts := make([]string, 0, len(failures))
	for _, failure := range failures {
		parts = append(parts, failure.Dir+" ("+strings.ReplaceAll(failure.Error, "\n", " ")+")")
	}
	return strings.Join(parts, "; ")
}

func writeLibraryRelationship(output *strings.Builder, dependency DependencyDemand) {
	marker := "✓"
	switch dependency.Status {
	case StatusGap:
		marker = "✗"
	case StatusNonGoal:
		marker = "○"
	}
	fmt.Fprintf(output, "  %s %-35s -> %s\n", marker, dependency.Package, libraryRelationshipDescription(dependency))
	if dependency.Notes != "" {
		fmt.Fprintf(output, "    Note: %s\n", dependency.Notes)
	}
}

func libraryRelationshipDescription(dependency DependencyDemand) string {
	if relationship := dependency.Relationship; relationship != nil {
		if relationship.Basis == "unverified" {
			return "unrecognized library relationship; basis=unverified; no import replacement"
		}
		if relationship.Kind == RelationshipFoundation {
			return fmt.Sprintf("foundation; retain library; capability=%s; basis=%s", dependency.Capability, relationship.Basis)
		}
		availability := "available"
		if dependency.Status == StatusGap {
			availability = "unavailable"
		}
		related := relationship.FrameworkPackage
		if related == "" {
			related = "none"
		}
		return fmt.Sprintf("%s; related package=%s (%s); basis=%s; no import replacement",
			relationship.Kind, related, availability, relationship.Basis)
	}
	if dependency.Status == StatusGap {
		return fmt.Sprintf("UNMAPPED (Gap); capability=%s", dependency.Capability)
	}
	if dependency.Status == StatusNonGoal {
		return fmt.Sprintf("framework non-goal (resolved); capability=%s; no replacement planned", dependency.Capability)
	}
	if dependency.Status == StatusNative {
		return "native; no replacement required"
	}
	return fmt.Sprintf("replacement candidate: %s; API compatibility unverified", dependency.FrameworkReplacement)
}

// maxUmbrellaFilesShown bounds the importing files one umbrella finding names.
const maxUmbrellaFilesShown = 3

// FormatUmbrellaImports renders the umbrella-import findings of a report
// (ReportRepoWithFramework) the CLI `needs report` and the MCP standards_needs_report share, or
// nothing when the repository imports no umbrella of the selected framework. The findings are
// recommendations, never a gate.
func FormatUmbrellaImports(report *RepoNeeds) string {
	if report == nil || len(report.UmbrellaImports) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString("\nUmbrella imports (recommendations, not a gate):\n")
	for i := range report.UmbrellaImports {
		writeUmbrellaFinding(&sb, &report.UmbrellaImports[i])
	}
	return sb.String()
}

func writeUmbrellaFinding(sb *strings.Builder, finding *UmbrellaFinding) {
	writef(sb, "  %s imported in %s\n", finding.Umbrella, umbrellaFiles(finding.Files))
	switch finding.Status {
	case UmbrellaUnknown:
		sb.WriteString("    unknown umbrella: the capability contract describes no groupings for it; not analysed\n")
	case UmbrellaJustified:
		writef(sb, "    wires every grouping (%d); the umbrella import is justified\n", finding.TotalGroupings)
	case UmbrellaUnmapped:
		sb.WriteString("    references no grouping" + umbrellaOnly(finding.Unmapped) + "; nothing to recommend\n")
	default:
		writeUmbrellaRecommendation(sb, finding)
	}
}

func writeUmbrellaRecommendation(sb *strings.Builder, finding *UmbrellaFinding) {
	writef(sb, "    wires %d of %d groupings (%s); import instead:\n",
		len(finding.Groupings), finding.TotalGroupings, strings.Join(finding.Groupings, ", "))
	for _, recommendation := range finding.Recommendations {
		capabilities := "capabilities not observed in the selected framework"
		if len(recommendation.Capabilities) > 0 {
			capabilities = joinCapabilities(recommendation.Capabilities)
		}
		writef(sb, "    -> %s (grouping %s): %s\n", recommendation.Import, strings.Join(recommendation.Groupings, ", "), capabilities)
	}
	if len(finding.Unmapped) > 0 {
		writef(sb, "    also references %s, which no grouping describes; the umbrella import stays\n", strings.Join(finding.Unmapped, ", "))
	}
	writef(sb, "    switch effect: %s\n", formatUmbrellaMeasurement(finding.Measurement))
}

// formatUmbrellaMeasurement renders what the switch removes, or why it was not measured.
func formatUmbrellaMeasurement(measurement *UmbrellaMeasurement) string {
	switch {
	case measurement == nil:
		return "not measured"
	case !measurement.Measured:
		return "not measured (" + measurement.Reason + ")"
	default:
		return fmt.Sprintf("modules %d -> %d (-%d), packages %d -> %d (-%d); go list -deps, offline",
			measurement.ModulesBefore, measurement.ModulesAfter, measurement.ModulesBefore-measurement.ModulesAfter,
			measurement.PackagesBefore, measurement.PackagesAfter, measurement.PackagesBefore-measurement.PackagesAfter)
	}
}

// umbrellaOnly names the unmapped references of an umbrella that references no grouping.
func umbrellaOnly(unmapped []string) string {
	if len(unmapped) == 0 {
		return ""
	}
	return ", only " + strings.Join(unmapped, ", ")
}

// umbrellaFiles names at most maxUmbrellaFilesShown importing files and counts the rest.
func umbrellaFiles(files []string) string {
	if len(files) <= maxUmbrellaFilesShown {
		return strings.Join(files, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(files[:maxUmbrellaFilesShown], ", "), len(files)-maxUmbrellaFilesShown)
}

func joinCapabilities(capabilities []CapabilityKey) string {
	names := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		names = append(names, string(capability))
	}
	return strings.Join(names, ", ")
}
