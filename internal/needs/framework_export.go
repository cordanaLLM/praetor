package needs

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"
)

// ContractExport is a framework snapshotted as a version-1 capability contract.
type ContractExport struct {
	// Data is the contract YAML; it parses back to the contract it was rendered from.
	Data []byte
	// Framework is the module the contract declares.
	Framework string
	// Packages counts the declared packages.
	Packages int
	// Skipped lists entries the contract grammar cannot carry, each with the reason.
	Skipped []string
}

// ExportFrameworkContract snapshots the framework a language's target resolves to as a
// version-1 capability contract (`needs contract export`). The go framework is the index
// source selects (SelectFrameworkSource); another language's framework is its target's
// declared contract, else the built-in catalog entries inside its target's module. A
// framework without a contract of its own carries the built-in catalog's claims for it, so
// the export can be configured as framework.targets.<language>.contract in place of the
// built-in data.
func ExportFrameworkContract(ctx context.Context, language string, source FrameworkSource, targets Targets) (*ContractExport, error) {
	index, err := exportIndex(ctx, language, source, targets.For(language))
	if err != nil {
		return nil, err
	}
	if index.Name == "" {
		return nil, fmt.Errorf("no %s framework is configured: set framework.targets.%s.module", language, language)
	}
	export := &ContractExport{Framework: index.Name}
	contract := contractFromIndex(index, languageEcosystem(language), &export.Skipped)
	data, err := yaml.Marshal(contract)
	if err != nil {
		return nil, fmt.Errorf("render framework contract: %w", err)
	}
	if _, err := parseFrameworkContract(data, index.Name); err != nil {
		return nil, fmt.Errorf("exported contract does not parse: %w", err)
	}
	export.Data, export.Packages = data, len(contract.Packages)
	return export, nil
}

// exportIndex builds the framework index an export renders.
func exportIndex(ctx context.Context, language string, source FrameworkSource, target Target) (*FrameworkIndex, error) {
	if language == "go" {
		index, err := InspectFramework(ctx, source)
		if err != nil {
			return nil, err
		}
		addGoCatalogClaims(index)
		return index, nil
	}
	if target.Contract != "" {
		return InspectFramework(ctx, FrameworkSource{Contract: target.Contract, Module: target.Module})
	}
	catalog, ok := languageCatalog(language)
	if !ok {
		return nil, fmt.Errorf("unknown framework language %q", language)
	}
	return catalogFrameworkIndex(target.Module, languageEcosystem(language), catalog), nil
}

// beginCatalogClaims prepares the claim maps of an index that has no contract of its own.
// It reports false for an index a contract already filled, whose claims are authoritative.
func beginCatalogClaims(index *FrameworkIndex) bool {
	if index.Contract != "" {
		return false
	}
	index.Replacements = make(map[string][]string)
	index.Adaptations = make(map[string][]string)
	index.Wrappers = make(map[string][]string)
	index.Tooling = make(map[string][]string)
	return true
}

// addGoCatalogClaims records CanonicalCatalog's claims on the go framework's packages. A
// declared index gains the packages the catalog names; an observed index keeps only the
// packages its checkout provides, since an unobserved package is unavailable.
func addGoCatalogClaims(index *FrameworkIndex) {
	if !beginCatalogClaims(index) {
		return
	}
	for _, entry := range CanonicalCatalog {
		if entry.Relationship != nil && entry.Relationship.Kind == RelationshipFoundation {
			// The catalog's foundations describe its own module only; reconciliation drops
			// them for any other module (library_relationships.go, targets.go scopeRelationship),
			// so an export for another module must not claim them either.
			if catalogDescribes(index.CatalogModule) {
				index.Foundations = appendUniqueStr(index.Foundations, entry.Package)
			}
			continue
		}
		claims := catalogClaims(index, entry.Status, entry.Relationship)
		if claims == nil {
			continue
		}
		if path, ok := catalogClaimPackage(index, catalogFrameworkPackage(entry), entry.Capability); ok {
			key := contractNameKey(contractEcosystemGo, entry.Package)
			claims[key] = appendUniqueStr(claims[key], path)
		}
	}
}

// catalogClaims selects the claim map a catalog entry belongs in: its relationship's, else
// replaces for a covered entry and adapts for an adapter; a gap claims nothing.
func catalogClaims(index *FrameworkIndex, status CapabilityStatus, relationship *LibraryRelationship) map[string][]string {
	if relationship != nil {
		if relationship.Kind == RelationshipTooling {
			return index.Tooling
		}
		return index.Wrappers
	}
	switch status {
	case StatusCovered:
		return index.Replacements
	case StatusAdapterAvailable:
		return index.Adaptations
	}
	return nil
}

// catalogClaimPackage maps a catalog path onto the framework's package, adding it to a
// declared index. It reports false for a path outside the catalog module and for a package
// an observed checkout does not provide.
func catalogClaimPackage(index *FrameworkIndex, catalogPath string, capability CapabilityKey) (string, bool) {
	relative, ok := index.catalogRelative(catalogPath)
	if !ok || relative == "" {
		return "", false
	}
	path := index.Name + "/" + relative
	if _, present := index.Packages[path]; !present && index.Basis == FrameworkSourceObserved {
		return "", false
	}
	addObservedPackage(index, relative, []CapabilityKey{capability})
	return path, true
}

// catalogFrameworkIndex declares a non-go framework from its language's built-in catalog:
// every catalog replacement inside module is a package with its capabilities and claims.
func catalogFrameworkIndex(module, ecosystem string, catalog map[string]CatalogMapping) *FrameworkIndex {
	index := &FrameworkIndex{Name: module, CatalogModule: module, Version: declaredFrameworkVersion,
		Basis: FrameworkCatalogDeclared, Ecosystem: ecosystem,
		Packages: make(map[string]FrameworkPackage), Capabilities: make(map[CapabilityKey][]string)}
	beginCatalogClaims(index)
	if module == "" {
		return index
	}
	for _, name := range slices.Sorted(maps.Keys(catalog)) {
		mapping := catalog[name]
		claims := catalogClaims(index, mapping.Status, nil)
		if claims == nil || !matchesModuleBoundary(mapping.Replacement, module) {
			continue
		}
		relative := strings.TrimPrefix(strings.TrimPrefix(mapping.Replacement, module), "/")
		declareCatalogPackage(index, mapping.Replacement, relative, mapping.Capability)
		key := contractNameKey(ecosystem, name)
		claims[key] = appendUniqueStr(claims[key], mapping.Replacement)
	}
	return index
}

// declareCatalogPackage adds or extends one declared package of a catalog index.
func declareCatalogPackage(index *FrameworkIndex, path, relative string, capability CapabilityKey) {
	entry, ok := index.Packages[path]
	if !ok {
		domain, _, _ := strings.Cut(relative, "/")
		entry = FrameworkPackage{ImportPath: path, Domain: domain}
	}
	entry.Capabilities = appendUniqueCap(entry.Capabilities, capability)
	index.Packages[path] = entry
	index.Capabilities[capability] = appendUniqueStr(index.Capabilities[capability], path)
}

// contractFromIndex renders an index as a version-1 contract. Packages are in import order
// and every list is sorted, so an export is deterministic; an entry the contract grammar
// cannot carry is left out and listed in skipped.
func contractFromIndex(index *FrameworkIndex, ecosystem string, skipped *[]string) *frameworkContract {
	contract := &frameworkContract{Version: contractSchemaVersion, Framework: index.Name, Ecosystem: ecosystem,
		Foundations: validNames(ecosystem, "foundation", index.Foundations, true, skipped)}
	claims := invertClaims(index)
	for _, path := range slices.Sorted(maps.Keys(index.Packages)) {
		pkg, ok := contractPackageFor(index, path, skipped)
		if !ok {
			continue
		}
		for field, byPackage := range claims {
			names := validNames(ecosystem, field+" of "+path, byPackage[path], false, skipped)
			pkg.setNames(field, names)
		}
		if pkg.Module != index.Name {
			contract.Modules = appendUniqueStr(contract.Modules, pkg.Module)
		}
		contract.Packages = append(contract.Packages, pkg)
	}
	slices.Sort(contract.Modules)
	return contract
}

// contractPackageFor renders one index package, or reports false when its import path or
// every capability key falls outside the contract grammar.
func contractPackageFor(index *FrameworkIndex, path string, skipped *[]string) (contractPackage, bool) {
	entry := index.Packages[path]
	if path != index.Name && !isNestedModule(path, index.Name) {
		*skipped = append(*skipped, fmt.Sprintf("package %s: outside %s", path, index.Name))
		return contractPackage{}, false
	}
	module := entry.Module
	if module == "" || (module != index.Name && !isNestedModule(module, index.Name)) {
		module = index.Name
	}
	pkg := contractPackage{Import: path, Module: module, Domain: entry.Domain, Description: entry.Description}
	for _, capability := range entry.Capabilities {
		if !contractKeyPattern.MatchString(string(capability)) {
			*skipped = append(*skipped, fmt.Sprintf("capability %s of %s: not a contract capability key", capability, path))
			continue
		}
		pkg.Capabilities = append(pkg.Capabilities, string(capability))
	}
	if len(pkg.Capabilities) == 0 {
		*skipped = append(*skipped, fmt.Sprintf("package %s: no contract capability key", path))
		return contractPackage{}, false
	}
	return pkg, true
}

// invertClaims turns the index's name->packages claim maps into field->package->names.
func invertClaims(index *FrameworkIndex) map[string]map[string][]string {
	sources := map[string]map[string][]string{
		"replaces": index.Replacements, "adapts": index.Adaptations, "wraps": index.Wrappers, "tooling_for": index.Tooling,
	}
	inverted := make(map[string]map[string][]string, len(sources))
	for field, claims := range sources {
		byPackage := make(map[string][]string)
		for name, paths := range claims {
			for _, path := range paths {
				byPackage[path] = append(byPackage[path], name)
			}
		}
		inverted[field] = byPackage
	}
	return inverted
}

// validNames returns the sorted names valid for the ecosystem, or nil for none, listing the
// rest in skipped.
func validNames(ecosystem, what string, names []string, foundation bool, skipped *[]string) []string {
	var valid []string
	for _, name := range names {
		if !validContractName(ecosystem, name, foundation) {
			*skipped = append(*skipped, fmt.Sprintf("%s %s: not a valid %s name", what, name, ecosystem))
			continue
		}
		valid = appendUniqueStr(valid, name)
	}
	slices.Sort(valid)
	return valid
}

// setNames stores one third-party name list under its contract field.
func (p *contractPackage) setNames(field string, names []string) {
	switch field {
	case "replaces":
		p.Replaces = names
	case "adapts":
		p.Adapts = names
	case "wraps":
		p.Wraps = names
	case "tooling_for":
		p.ToolingFor = names
	}
}
