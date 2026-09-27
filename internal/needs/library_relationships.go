package needs

import (
	"fmt"
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
	return entry.FrameworkReplacement
}

func isSelectedStandardImport(path string) bool {
	if isThirdPartyImport(path, "") {
		return false
	}
	entry, found := MatchPackage(path)
	return found && entry.Package == path && entry.Relationship != nil && entry.Relationship.Kind == RelationshipFoundation
}

// availableFrameworkPackage retains legacy declared-catalog behavior while exact
// selected source packages remain required for source-observed availability. A catalog
// path resolves relative to the catalog module (FrameworkIndex.CatalogModule), so a fork is
// observed under its own module.
func availableFrameworkPackage(index *FrameworkIndex, capability CapabilityKey, canonical string) (string, bool) {
	if !index.ProvidesCapability(capability) {
		return "", false
	}
	if index.Basis != FrameworkSourceObserved {
		return canonical, true
	}
	relative, ok := index.catalogRelative(canonical)
	if !ok {
		return "", false
	}
	selected := index.Name + "/" + relative
	_, available := index.Packages[selected]
	return selected, available
}

// reconcileLibraryRelationship refreshes retained/related roles from the selected contract,
// then from the catalog, including older harvested reports that still propose replacing
// fx. Unrecognized caller-supplied relationships cannot bypass source reconciliation.
func reconcileLibraryRelationship(index *FrameworkIndex, demand *DependencyDemand) bool {
	if applyContractRelationship(index, demand) {
		return true
	}
	entry, found := MatchPackage(demand.Package)
	if !found || entry.Relationship == nil {
		return rejectUnknownRelationship(demand)
	}
	if entry.Relationship.Kind == RelationshipFoundation && !catalogDescribes(index.CatalogModule) {
		dropFoundation(demand, index.Name)
		return true
	}
	demand.Relationship = cloneRelationship(entry.Relationship)
	demand.Capability, demand.Status = entry.Capability, entry.Status
	demand.FrameworkReplacement, demand.Notes = "", entry.Notes
	if demand.Relationship.Kind == RelationshipFoundation {
		return true
	}
	selected, available := relatedFrameworkPackage(index, demand.Capability, demand.Relationship.FrameworkPackage)
	demand.Relationship.FrameworkPackage, demand.Relationship.Basis = selected, index.Basis
	if !available {
		demand.Status = StatusGap
		demand.Notes = fmt.Sprintf("%s Related package availability was not established in %s.", entry.Notes, index.Name)
	}
	if selected == "" {
		demand.Notes = undeclaredNote(index.Name, "related package", demand.Capability)
	}
	return true
}

// relatedFrameworkPackage maps a catalog relationship's package onto the selected framework.
// A package inside the catalog module resolves as availableFrameworkPackage does; a package
// of another framework is replaced by one the selected framework declares for the
// capability, or by none.
func relatedFrameworkPackage(index *FrameworkIndex, capability CapabilityKey, canonical string) (string, bool) {
	relative, inCatalog := index.catalogRelative(canonical)
	if !inCatalog {
		if paths := index.Capabilities[capability]; len(paths) > 0 {
			return paths[0], true
		}
		return "", false
	}
	selected, available := availableFrameworkPackage(index, capability, canonical)
	if selected == "" {
		selected = index.Name + "/" + relative
	}
	return selected, available
}

func rejectUnknownRelationship(demand *DependencyDemand) bool {
	if demand.Relationship == nil {
		return false
	}
	demand.Relationship = cloneRelationship(demand.Relationship)
	demand.Relationship.Basis = "unverified"
	demand.Status, demand.FrameworkReplacement = StatusGap, ""
	demand.Notes = "Library relationship is not recognized by the selected catalog."
	return true
}
