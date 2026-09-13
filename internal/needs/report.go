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
	return output.String()
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
