package needs

import (
	"fmt"
	"strings"
)

func cloneRelationship(relationship *LibraryRelationship) *LibraryRelationship {
	if relationship == nil {
		return nil
	}
	copy := *relationship
	return &copy
}

func catalogFrameworkPackage(entry CatalogEntry) string {
	if entry.Relationship != nil {
		return entry.Relationship.FrameworkPackage
	}
	return entry.GolusorisReplacement
}

func isSelectedStandardImport(path string) bool {
	if isThirdPartyImport(path, "") {
		return false
	}
	entry, found := MatchPackage(path)
	return found && entry.Package == path && entry.Relationship != nil && entry.Relationship.Kind == RelationshipFoundation
}

// availableFrameworkPackage retains legacy declared-catalog behavior while exact
// selected source packages remain required for source-observed availability.
func availableFrameworkPackage(index *FrameworkIndex, capability CapabilityKey, canonical string) (string, bool) {
	if !index.ProvidesCapability(capability) {
		return "", false
	}
	if index.Basis != FrameworkSourceObserved {
		return canonical, true
	}
	relative, ok := strings.CutPrefix(canonical, defaultFrameworkModule+"/")
	if !ok {
		return "", false
	}
	selected := index.Name + "/" + relative
	_, available := index.Packages[selected]
	return selected, available
}

// reconcileLibraryRelationship refreshes retained/related roles from the same
// catalog, including older harvested reports that still propose replacing fx.
// Unrecognized caller-supplied relationships cannot bypass source reconciliation.
func reconcileLibraryRelationship(index *FrameworkIndex, demand *DependencyDemand) bool {
	entry, found := MatchPackage(demand.Package)
	if !found || entry.Relationship == nil {
		return rejectUnknownRelationship(demand)
	}
	demand.Relationship = cloneRelationship(entry.Relationship)
	demand.Capability, demand.Status = entry.Capability, entry.Status
	demand.GolusorisReplacement, demand.Notes = "", entry.Notes
	if demand.Relationship.Kind == RelationshipFoundation {
		return true
	}
	canonical := demand.Relationship.FrameworkPackage
	selected, available := availableFrameworkPackage(index, demand.Capability, canonical)
	if selected == "" {
		relative := strings.TrimPrefix(canonical, defaultFrameworkModule+"/")
		selected = index.Name + "/" + relative
	}
	demand.Relationship.FrameworkPackage, demand.Relationship.Basis = selected, index.Basis
	if !available {
		demand.Status = StatusGap
		demand.Notes = fmt.Sprintf("%s Related package availability was not established in %s.", entry.Notes, index.Name)
	}
	return true
}

func rejectUnknownRelationship(demand *DependencyDemand) bool {
	if demand.Relationship == nil {
		return false
	}
	demand.Relationship = cloneRelationship(demand.Relationship)
	demand.Relationship.Basis = "unverified"
	demand.Status, demand.GolusorisReplacement = StatusGap, ""
	demand.Notes = "Library relationship is not recognized by the selected catalog."
	return true
}
