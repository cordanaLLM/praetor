package needs

func cloneRelationship(relationship *LibraryRelationship) *LibraryRelationship {
	if relationship == nil {
		return nil
	}
	copy := *relationship
	return &copy
}

// isSelectedStandardImport reports whether a standard-library import is one the catalog
// classifies, which the scan records apart from third-party demand.
func isSelectedStandardImport(path string) bool {
	if isThirdPartyImport(path, "") {
		return false
	}
	entry, found := MatchPackage(path)
	return found && entry.Package == path
}

// reconcileLibraryRelationship applies the retained or related role the selected contract
// declares for a demand (applyContractRelationship). A relationship the selected framework
// does not declare, as an older harvested report may carry, cannot bypass reconciliation:
// it is marked unverified and the demand is a gap.
func reconcileLibraryRelationship(index *FrameworkIndex, demand *DependencyDemand) bool {
	if applyContractRelationship(index, demand) {
		return true
	}
	return rejectUnknownRelationship(demand)
}

func rejectUnknownRelationship(demand *DependencyDemand) bool {
	if demand.Relationship == nil {
		return false
	}
	demand.Relationship = cloneRelationship(demand.Relationship)
	demand.Relationship.Basis = "unverified"
	demand.Status, demand.FrameworkReplacement = StatusGap, ""
	demand.Notes = "Library relationship is not declared by the selected framework."
	return true
}
