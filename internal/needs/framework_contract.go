package needs

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	// contractSchemaVersion is the only capability contract schema this reader accepts.
	contractSchemaVersion = 1
	maxContractBytes      = 1 << 20
	maxContractModules    = 64
	maxContractKeys       = 32
	maxContractReplaces   = 64
)

// contractKeyPattern is the capability key grammar shared with the framework's contract
// package: `domain.name[.sub]`, lowercase; later elements may start with a digit.
var contractKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9][a-z0-9_]*)+$`)

// frameworkContract mirrors the version-1 capabilities.yaml a framework checkout publishes
// at its root (golusoris core/capabilities): every importable package, the Go module that
// contains it, the capability keys it satisfies and the third-party modules it replaces.
// Unknown fields are ignored so a newer contract stays readable; unknown versions fail.
type frameworkContract struct {
	Version   int               `yaml:"version"`
	Framework string            `yaml:"framework"`
	Modules   []string          `yaml:"modules"`
	Packages  []contractPackage `yaml:"packages"`
}

type contractPackage struct {
	Import       string   `yaml:"import"`
	Module       string   `yaml:"module"`
	Domain       string   `yaml:"domain"`
	Capabilities []string `yaml:"capabilities"`
	Description  string   `yaml:"description"`
	Replaces     []string `yaml:"replaces"`
}

// loadFrameworkContract reads the contract at the checkout root. A missing file is not an
// error: the caller falls back to catalog candidates. A present file must validate against
// the module identity the checkout's go.mod declares.
func loadFrameworkContract(ctx context.Context, root *os.Root, module string) (contract *frameworkContract, found bool, err error) {
	raw, err := contextopt.ReadRootSnapshot(ctx, root, FrameworkContractFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read framework contract: %w", err)
	}
	contract, err = parseFrameworkContract(raw, module)
	if err != nil {
		return nil, false, err
	}
	return contract, true, nil
}

func parseFrameworkContract(raw []byte, module string) (*frameworkContract, error) {
	if len(raw) > maxContractBytes {
		return nil, errors.New("framework contract exceeds 1 MiB")
	}
	var contract frameworkContract
	if err := yaml.Unmarshal(raw, &contract); err != nil {
		return nil, fmt.Errorf("parse framework contract: %w", err)
	}
	if err := contract.validate(module); err != nil {
		return nil, fmt.Errorf("framework contract: %w", err)
	}
	return &contract, nil
}

func (c *frameworkContract) validate(module string) error {
	if c.Version != contractSchemaVersion {
		return fmt.Errorf("unsupported schema version %d (want %d)", c.Version, contractSchemaVersion)
	}
	if c.Framework == "" || c.Framework != module {
		return fmt.Errorf("framework %q does not match the checkout module %q", c.Framework, module)
	}
	if len(c.Modules) > maxContractModules || len(c.Packages) > maxFrameworkPackages {
		return fmt.Errorf("exceeds %d modules or %d packages", maxContractModules, maxFrameworkPackages)
	}
	modules := map[string]bool{c.Framework: true}
	for _, nested := range c.Modules {
		if !isNestedModule(nested, c.Framework) {
			return fmt.Errorf("module %q is outside %s", nested, c.Framework)
		}
		modules[nested] = true
	}
	seen := make(map[string]bool, len(c.Packages))
	for i := range c.Packages {
		if err := c.Packages[i].validate(c.Framework, modules, seen); err != nil {
			return err
		}
	}
	return nil
}

func (p *contractPackage) validate(framework string, modules, seen map[string]bool) error {
	if p.Import != framework && !isNestedModule(p.Import, framework) {
		return fmt.Errorf("package %q is outside %s", p.Import, framework)
	}
	if seen[p.Import] {
		return fmt.Errorf("package %q is declared twice", p.Import)
	}
	seen[p.Import] = true
	if p.Module == "" {
		p.Module = framework
	}
	if !modules[p.Module] || (p.Import != p.Module && !isNestedModule(p.Import, p.Module)) {
		return fmt.Errorf("package %q declares module %q that is undeclared or does not contain it", p.Import, p.Module)
	}
	return p.validateKeys()
}

func (p *contractPackage) validateKeys() error {
	if len(p.Capabilities) == 0 || len(p.Capabilities) > maxContractKeys || len(p.Replaces) > maxContractReplaces {
		return fmt.Errorf("package %q needs 1..%d capabilities and at most %d replaces", p.Import, maxContractKeys, maxContractReplaces)
	}
	for _, key := range p.Capabilities {
		if !contractKeyPattern.MatchString(key) {
			return fmt.Errorf("package %q declares invalid capability key %q", p.Import, key)
		}
	}
	for _, replaced := range p.Replaces {
		if !isModulePathShaped(replaced) {
			return fmt.Errorf("package %q replaces invalid module path %q", p.Import, replaced)
		}
	}
	return nil
}

// isNestedModule reports whether path lies strictly below parent on a path boundary.
func isNestedModule(path, parent string) bool {
	return strings.HasPrefix(path, parent+"/")
}

// contractRelative returns the checkout-relative directory of an import path; the
// framework root package itself maps to the empty path.
func contractRelative(importPath, framework string) string {
	relative, ok := strings.CutPrefix(importPath, framework+"/")
	if !ok {
		return ""
	}
	return relative
}

// stripMajorSuffix drops a trailing /vN major-version segment so a demand for
// github.com/acme/lib/v2 matches a contract that replaces github.com/acme/lib.
func stripMajorSuffix(modulePath string) string {
	index := strings.LastIndex(modulePath, "/")
	if index <= 0 || !isMajorVersionSegment(modulePath[index+1:]) {
		return modulePath
	}
	return modulePath[:index]
}

// observeContractFramework builds the index from the contract's package inventory. Every
// declared package is still source-observed inside its declared module: a declaration
// alone never establishes availability, and declared nested modules are honoured instead
// of being rejected as foreign modules.
func observeContractFramework(ctx context.Context, index *FrameworkIndex, contract *frameworkContract) error {
	packages := slices.Clone(contract.Packages)
	slices.SortFunc(packages, func(a, b contractPackage) int { return strings.Compare(a.Import, b.Import) })
	index.Contract = FrameworkContractFile
	index.Replacements = make(map[string][]string)
	total := 0
	for i := range packages {
		present, err := observeContractPackage(ctx, index, &packages[i], &total)
		if err != nil {
			return fmt.Errorf("inspect contract package %s: %w", packages[i].Import, err)
		}
		if present {
			addContractPackage(index, &packages[i])
		}
	}
	return ctx.Err()
}

func observeContractPackage(ctx context.Context, index *FrameworkIndex, pkg *contractPackage, total *int) (present bool, err error) {
	relative := contractRelative(pkg.Import, index.Name)
	root, err := contextopt.OpenDirectory(ctx, filepath.Join(index.RootPath, filepath.FromSlash(relative)))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	inModule, err := contractPackageInModule(ctx, index.RootPath, relative, contractRelative(pkg.Module, index.Name))
	if err != nil || !inModule {
		return false, err
	}
	entries, err := frameworkPackageEntries(root)
	if err != nil {
		return false, err
	}
	return frameworkPackageHasSource(ctx, root, entries, total)
}

// contractPackageInModule reports whether the package directory sits inside the module the
// contract declares for it: the only go.mod on the path from the checkout root may be the
// declared module's own, and a declared nested module must actually carry one.
func contractPackageInModule(ctx context.Context, base, relative, moduleRelative string) (bool, error) {
	parts := splitRelative(relative)
	if len(parts) > maxPathSegments {
		return false, errors.New("contract package exceeds path depth bound")
	}
	current, prefix, declaredSeen := base, "", moduleRelative == ""
	for _, part := range parts {
		current = filepath.Join(current, part)
		prefix = joinRelative(prefix, part)
		_, exists, err := contextopt.ObserveSnapshot(ctx, filepath.Join(current, "go.mod"))
		if err != nil {
			return false, err
		}
		if exists && prefix != moduleRelative {
			return false, nil
		}
		declaredSeen = declaredSeen || (exists && prefix == moduleRelative)
	}
	return declaredSeen, nil
}

func splitRelative(relative string) []string {
	if relative == "" {
		return nil
	}
	return strings.Split(relative, "/")
}

func joinRelative(prefix, part string) string {
	if prefix == "" {
		return part
	}
	return prefix + "/" + part
}

// addContractPackage records an observed contract package, its capabilities and the
// third-party modules it replaces. Packages are visited in import order, so claimant
// lists are deterministic.
func addContractPackage(index *FrameworkIndex, pkg *contractPackage) {
	entry := FrameworkPackage{ImportPath: pkg.Import, Module: pkg.Module, Domain: pkg.Domain, Description: pkg.Description}
	if entry.Domain == "" {
		entry.Domain, _, _ = strings.Cut(contractRelative(pkg.Import, index.Name), "/")
	}
	for _, key := range pkg.Capabilities {
		capability := CapabilityKey(key)
		entry.Capabilities = appendUniqueCap(entry.Capabilities, capability)
		index.Capabilities[capability] = appendUniqueStr(index.Capabilities[capability], pkg.Import)
	}
	index.Packages[pkg.Import] = entry
	for _, replaced := range pkg.Replaces {
		key := stripMajorSuffix(replaced)
		index.Replacements[key] = appendUniqueStr(index.Replacements[key], pkg.Import)
	}
}

// contractReplacement resolves a demand through the contract. Among the packages whose
// entries replace the module, one declaring the demanded capability wins, then the first
// claimant in import order; with no claimant, any observed package declaring the
// capability. All of them are observed packages; none proves API compatibility.
func contractReplacement(idx *FrameworkIndex, dep *DependencyDemand) (string, bool) {
	if idx.Contract == "" {
		return "", false
	}
	claimants := idx.Replacements[stripMajorSuffix(dep.Package)]
	for _, path := range claimants {
		if slices.Contains(idx.Packages[path].Capabilities, dep.Capability) {
			return path, true
		}
	}
	if len(claimants) > 0 {
		return claimants[0], true
	}
	if paths := idx.Capabilities[dep.Capability]; len(paths) > 0 {
		return paths[0], true
	}
	return "", false
}

// reconcileContractDemand marks a demand covered by its contract replacement. A demand the
// catalog did not know adopts the replacement package's first declared capability so it
// stops counting as a custom gap.
func reconcileContractDemand(idx *FrameworkIndex, dep *DependencyDemand) bool {
	path, ok := contractReplacement(idx, dep)
	if !ok {
		return false
	}
	pkg := idx.Packages[path]
	if !slices.Contains(pkg.Capabilities, dep.Capability) && len(pkg.Capabilities) > 0 {
		dep.Capability = pkg.Capabilities[0]
	}
	if dep.Status != StatusCovered {
		dep.Notes = fmt.Sprintf("%s declares %s for %s", idx.Contract, path, dep.Capability)
	}
	dep.Status, dep.GolusorisReplacement = StatusCovered, path
	return true
}

// isFrameworkModule reports whether pkg is the selected framework module or one of its
// nested modules, which a consumer imports natively rather than as third-party demand.
func isFrameworkModule(idx *FrameworkIndex, pkg string) bool {
	return pkg == idx.Name || isNestedModule(pkg, idx.Name)
}

func markFrameworkNative(idx *FrameworkIndex, dep *DependencyDemand) {
	dep.Capability, dep.Status, dep.GolusorisReplacement = FrameworkNativeCapability, StatusNative, ""
	dep.Relationship = nil
	dep.Notes = fmt.Sprintf("Module of the selected framework %s; retained", idx.Name)
}
