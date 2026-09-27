package needs

import (
	"context"
	"errors"
	"time"
)

const (
	FrameworkCatalogDeclared = "catalog-declared"
	FrameworkSourceObserved  = "source-observed"
	// FrameworkNotConfigured is the basis of an index for which no framework is selected:
	// no checkout, no contract and no module.
	FrameworkNotConfigured = "not-configured"
	// declaredFrameworkVersion is the version of an index a contract declares.
	declaredFrameworkVersion = "declared"
	// FrameworkContractFile is the capability contract a framework checkout may publish
	// at its root; when present it is the package inventory, not directory heuristics.
	FrameworkContractFile = "capabilities.yaml"
	// FrameworkNativeCapability classifies a consumer's imports of the selected
	// framework's own modules: they are retained, never third-party demand.
	FrameworkNativeCapability CapabilityKey = "fleet.framework"
)

// InspectFramework builds the index of the framework source selects. A checkout is
// observed from source, against its own capabilities.yaml or else the configured contract;
// a contract alone declares packages without observing them; a module alone names the
// framework without declaring a package; nothing selected is not configured. It does not
// run builds or establish tested correctness.
func InspectFramework(ctx context.Context, source FrameworkSource) (*FrameworkIndex, error) {
	if ctx == nil {
		return nil, errors.New("framework inspection requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	index := &FrameworkIndex{
		Name: source.Module, RootPath: source.Checkout,
		Version: declaredFrameworkVersion, Basis: FrameworkCatalogDeclared,
		Packages:     make(map[string]FrameworkPackage),
		Capabilities: make(map[CapabilityKey][]string),
	}
	switch {
	case source.Checkout != "":
		index.Basis, index.Version = FrameworkSourceObserved, "unverified"
		if err := observeFramework(ctx, index, source.Contract); err != nil {
			return nil, err
		}
	case source.Contract != "":
		if err := declareContractFramework(ctx, index, source.Contract); err != nil {
			return nil, err
		}
	case index.Name == "":
		index.Basis, index.Version = FrameworkNotConfigured, ""
	default:
		index.Basis, index.Version = FrameworkIdentityDeclared, "unverified"
	}
	return index, nil
}

// IsCapabilityCovered reports whether the index lists at least one framework package
// for capKey verbatim.
func (idx *FrameworkIndex) IsCapabilityCovered(capKey CapabilityKey) bool {
	if idx == nil {
		return false
	}
	pkgs, ok := idx.Capabilities[capKey]
	return ok && len(pkgs) > 0
}

// ProvidesCapability requires an exact mapping. Basis distinguishes declarations
// from observed source availability; neither establishes tested correctness.
func (idx *FrameworkIndex) ProvidesCapability(capKey CapabilityKey) bool {
	return idx.IsCapabilityCovered(capKey)
}
