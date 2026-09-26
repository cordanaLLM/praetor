package needs

import (
	"fmt"
	"strings"
)

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
	if dependency.Status == StatusGap {
		marker = "✗"
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
		return fmt.Sprintf("%s; related package=%s (%s); basis=%s; no import replacement",
			relationship.Kind, relationship.FrameworkPackage, availability, relationship.Basis)
	}
	if dependency.Status == StatusGap {
		return fmt.Sprintf("UNMAPPED (Gap); capability=%s", dependency.Capability)
	}
	if dependency.Status == StatusNative {
		return "native; no replacement required"
	}
	return fmt.Sprintf("replacement candidate: %s; API compatibility unverified", dependency.GolusorisReplacement)
}
