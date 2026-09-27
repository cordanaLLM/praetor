package needs

import (
	"context"
	"fmt"
	"maps"
	"slices"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/config"
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
// contract, else its module alone. The export carries every package, capability and claim
// the index holds, so it can be configured as framework.targets.<language>.contract, or
// serve a CI run that has no checkout.
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

// exportIndex builds the framework index an export renders: the selected go framework, or
// another language's target contract or module.
func exportIndex(ctx context.Context, language string, source FrameworkSource, target Target) (*FrameworkIndex, error) {
	if _, err := config.ParseFrameworkLanguage(language); err != nil {
		return nil, err
	}
	if language != "go" {
		source = FrameworkSource{Contract: target.Contract, Module: target.Module}
	}
	return InspectFramework(ctx, source)
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
