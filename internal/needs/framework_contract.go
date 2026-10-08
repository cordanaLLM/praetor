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

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	// contractSchemaVersion is the only capability contract schema this reader accepts.
	contractSchemaVersion = 1
	maxContractBytes      = 1 << 20
	maxContractModules    = 64
	maxContractKeys       = 32
	maxContractReplaces   = 64
	// maxContractFoundations bounds the contract's top-level foundations list.
	maxContractFoundations = 64
	// maxContractNameBytes bounds one third-party name (npm's own limit).
	maxContractNameBytes = 214
	// contractEcosystemGo is the ecosystem of a contract that names none.
	contractEcosystemGo = "go"
)

// contractKeyPattern is the capability key grammar shared with the framework's contract
// package: `domain.name[.sub]`, lowercase; later elements may start with a digit.
var contractKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(\.[a-z0-9][a-z0-9_]*)+$`)

// contractNameGrammars checks the third-party names of every ecosystem but go, whose names
// are module paths (config.IsModulePathShaped).
var contractNameGrammars = map[string]*regexp.Regexp{
	"npm":    regexp.MustCompile(`^(@[a-z0-9~][a-z0-9._~-]*/)?[a-z0-9~][a-z0-9._~-]*$`),
	"pypi":   regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`),
	"cargo":  regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`),
	"system": regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]*$`),
}

// goStandardImport is the shape of a Go standard-library import path, which a go contract
// may retain as a foundation.
var goStandardImport = regexp.MustCompile(`^[a-z0-9]+(/[a-z0-9_]+)*$`)

// frameworkContract mirrors the version-1 capabilities.yaml a framework publishes: every
// importable package, the module that contains it, the capability keys it satisfies and
// the third-party names it replaces, adapts, wraps or is tooling for, plus the third-party
// names the framework retains as foundations. Ecosystem (go, npm, pypi, cargo or system;
// go when empty) selects the grammar those names are checked against. Every field after
// the version-1 core is optional, and unknown fields are ignored, so a newer contract stays
// readable; unknown versions fail. Umbrellas (framework_umbrella.go) describe the packages
// that only group every subsystem's modules.
type frameworkContract struct {
	Version     int                `yaml:"version"`
	Framework   string             `yaml:"framework"`
	Ecosystem   string             `yaml:"ecosystem,omitempty"`
	Modules     []string           `yaml:"modules,omitempty"`
	Foundations []string           `yaml:"foundations,omitempty"`
	Packages    []contractPackage  `yaml:"packages"`
	Umbrellas   []contractUmbrella `yaml:"umbrellas,omitempty"`
}

type contractPackage struct {
	Import       string   `yaml:"import"`
	Module       string   `yaml:"module,omitempty"`
	Domain       string   `yaml:"domain,omitempty"`
	Capabilities []string `yaml:"capabilities"`
	Description  string   `yaml:"description,omitempty"`
	Replaces     []string `yaml:"replaces,omitempty"`
	Adapts       []string `yaml:"adapts,omitempty"`
	Wraps        []string `yaml:"wraps,omitempty"`
	ToolingFor   []string `yaml:"tooling_for,omitempty"`
}

// ecosystem returns the contract's ecosystem, go when it names none.
func (c *frameworkContract) ecosystem() string {
	if c.Ecosystem == "" {
		return contractEcosystemGo
	}
	return c.Ecosystem
}

// loadFrameworkContract reads the contract at the checkout root. A missing file is not an
// error: the caller falls back to the configured contract, if any. A present file must
// validate against the module identity the checkout's go.mod declares.
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

// parseFrameworkContract parses and validates a contract. module is the identity the
// contract must declare; empty accepts the framework the contract names.
func parseFrameworkContract(raw []byte, module string) (*frameworkContract, error) {
	if len(raw) > maxContractBytes {
		return nil, errors.New("framework contract exceeds 1 MiB")
	}
	var contract frameworkContract
	if err := yaml.Unmarshal(raw, &contract); err != nil {
		return nil, fmt.Errorf("parse framework contract: %w", err)
	}
	if module == "" {
		module = contract.Framework
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
		return fmt.Errorf("framework %q does not match the selected module %q", c.Framework, module)
	}
	if len(c.Modules) > maxContractModules || len(c.Packages) > maxFrameworkPackages {
		return fmt.Errorf("exceeds %d modules or %d packages", maxContractModules, maxFrameworkPackages)
	}
	if err := c.validateEcosystem(); err != nil {
		return err
	}
	modules, err := c.declaredModules()
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(c.Packages))
	for i := range c.Packages {
		if err := c.Packages[i].validate(c.Framework, modules, seen); err != nil {
			return err
		}
		if err := c.Packages[i].validateNames(c.ecosystem()); err != nil {
			return err
		}
	}
	return validateUmbrellas(c.Umbrellas, c.ecosystem(), c.Framework, seen)
}

// validateEcosystem checks the ecosystem and the top-level foundations against it.
func (c *frameworkContract) validateEcosystem() error {
	if _, known := contractNameGrammars[c.ecosystem()]; !known && c.ecosystem() != contractEcosystemGo {
		return fmt.Errorf("unknown ecosystem %q (want go, npm, pypi, cargo or system)", c.Ecosystem)
	}
	return validateContractNames(c.ecosystem(), "foundations", c.Foundations, maxContractFoundations, true)
}

// declaredModules returns the framework module and every nested module the contract
// declares, refusing a module outside the framework.
func (c *frameworkContract) declaredModules() (map[string]bool, error) {
	modules := map[string]bool{c.Framework: true}
	for _, nested := range c.Modules {
		if !isNestedModule(nested, c.Framework) {
			return nil, fmt.Errorf("module %q is outside %s", nested, c.Framework)
		}
		modules[nested] = true
	}
	return modules, nil
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
	if len(p.Capabilities) == 0 || len(p.Capabilities) > maxContractKeys {
		return fmt.Errorf("package %q needs 1..%d capabilities", p.Import, maxContractKeys)
	}
	for _, key := range p.Capabilities {
		if !contractKeyPattern.MatchString(key) {
			return fmt.Errorf("package %q declares invalid capability key %q", p.Import, key)
		}
	}
	return nil
}

// validateNames checks every third-party name list of the package against the ecosystem.
func (p *contractPackage) validateNames(ecosystem string) error {
	for _, list := range p.nameLists() {
		if err := validateContractNames(ecosystem, list.field, list.names, maxContractReplaces, false); err != nil {
			return fmt.Errorf("package %q: %w", p.Import, err)
		}
	}
	return nil
}

// contractNameList is one named third-party list of a contract package.
type contractNameList struct {
	field string
	names []string
}

func (p *contractPackage) nameLists() [4]contractNameList {
	return [4]contractNameList{{"replaces", p.Replaces}, {"adapts", p.Adapts}, {"wraps", p.Wraps}, {"tooling_for", p.ToolingFor}}
}

// validateContractNames bounds a third-party name list and checks each name against the
// ecosystem's grammar. A go name is a module path; a go foundation may also be a standard
// library import path.
func validateContractNames(ecosystem, field string, names []string, limit int, foundation bool) error {
	if len(names) > limit {
		return fmt.Errorf("%s exceeds %d entries", field, limit)
	}
	for _, name := range names {
		if !validContractName(ecosystem, name, foundation) {
			return fmt.Errorf("%s lists %q, which is not a valid %s name", field, name, ecosystem)
		}
	}
	return nil
}

func validContractName(ecosystem, name string, foundation bool) bool {
	if name == "" || len(name) > maxContractNameBytes {
		return false
	}
	if ecosystem != contractEcosystemGo {
		return contractNameGrammars[ecosystem].MatchString(name)
	}
	return config.IsModulePathShaped(name) || foundation && goStandardImport.MatchString(name)
}

// contractNameKey keys a third-party name in the index maps: a go module path without its
// major-version suffix, a PyPI name normalised per PEP 503, any other name lower-cased.
func contractNameKey(ecosystem, name string) string {
	switch ecosystem {
	case "", contractEcosystemGo:
		return stripMajorSuffix(name)
	case "pypi":
		return normalizePyPIName(name)
	default:
		return strings.ToLower(name)
	}
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
	if index <= 0 || !util.IsGoMajorVersionElement(modulePath[index+1:]) {
		return modulePath
	}
	return modulePath[:index]
}

// rebase returns a copy of the contract declaring the same packages under module: every
// import path and module moves from the contract's framework onto module, so a fork is
// observed at the paths its upstream's contract names.
func (c *frameworkContract) rebase(module string) *frameworkContract {
	move := func(path string) string { return module + strings.TrimPrefix(path, c.Framework) }
	out := *c
	out.Framework = module
	out.Modules = make([]string, 0, len(c.Modules))
	for _, nested := range c.Modules {
		out.Modules = append(out.Modules, move(nested))
	}
	out.Packages = slices.Clone(c.Packages)
	for i := range out.Packages {
		out.Packages[i].Import = move(out.Packages[i].Import)
		if out.Packages[i].Module != "" {
			out.Packages[i].Module = move(out.Packages[i].Module)
		}
	}
	out.Umbrellas = rebaseUmbrellas(c.Umbrellas, move)
	return &out
}

// observeContractFramework builds the index from the contract's package inventory. Every
// declared package is still source-observed inside its declared module: a declaration
// alone never establishes availability, and declared nested modules are honoured instead
// of being rejected as foreign modules. name labels the contract in notes.
func observeContractFramework(ctx context.Context, index *FrameworkIndex, contract *frameworkContract, name string) error {
	packages := beginContractIndex(index, contract, name)
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

// beginContractIndex prepares index for a contract's inventory and returns the contract's
// packages in import order. name labels the contract in notes; it is a file name, never a
// local path, because notes reach published issue bodies.
func beginContractIndex(index *FrameworkIndex, contract *frameworkContract, name string) []contractPackage {
	index.Contract, index.Ecosystem = name, contract.ecosystem()
	index.Replacements = make(map[string][]string)
	index.Adaptations = make(map[string][]string)
	index.Wrappers = make(map[string][]string)
	index.Tooling = make(map[string][]string)
	index.Foundations = slices.Clone(contract.Foundations)
	index.Umbrellas = indexUmbrellas(contract.Umbrellas)
	packages := slices.Clone(contract.Packages)
	slices.SortFunc(packages, func(a, b contractPackage) int { return strings.Compare(a.Import, b.Import) })
	return packages
}

// declareContractFramework builds the index from the contract at path without observing
// source: every package is declared, and the basis says so. The contract must name the
// selected module when one is selected, and names the framework otherwise.
func declareContractFramework(ctx context.Context, index *FrameworkIndex, path string) error {
	raw, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("read framework contract %s: %w", path, err)
	}
	contract, err := parseFrameworkContract(raw, index.Name)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	index.Name, index.RootPath = contract.Framework, ""
	packages := beginContractIndex(index, contract, filepath.Base(path))
	for i := range packages {
		addContractPackage(index, &packages[i])
	}
	return ctx.Err()
}

// addContractPackage records a contract package, its capabilities and the third-party names
// it replaces, adapts, wraps or is tooling for. Packages are visited in import order, so
// claimant lists are deterministic.
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
	for _, list := range [...]struct {
		claims map[string][]string
		names  []string
	}{{index.Replacements, pkg.Replaces}, {index.Adaptations, pkg.Adapts}, {index.Wrappers, pkg.Wraps}, {index.Tooling, pkg.ToolingFor}} {
		for _, name := range list.names {
			key := contractNameKey(index.Ecosystem, name)
			list.claims[key] = appendUniqueStr(list.claims[key], pkg.Import)
		}
	}
}

// preferredClaimant returns the claimant declaring capability, else the first in import
// order, else "".
func preferredClaimant(idx *FrameworkIndex, claimants []string, capability CapabilityKey) string {
	for _, path := range claimants {
		if slices.Contains(idx.Packages[path].Capabilities, capability) {
			return path
		}
	}
	if len(claimants) > 0 {
		return claimants[0]
	}
	return ""
}

// contractReplacement resolves a demand through the contract and returns the package and
// the status it earns. Among the packages whose entries replace the name, one declaring the
// demanded capability wins, then the first claimant in import order (covered); then the
// packages that adapt it (adapter available); with no claimant, any package declaring the
// capability (covered). None of them proves API compatibility.
func contractReplacement(idx *FrameworkIndex, dep *DependencyDemand) (string, CapabilityStatus, bool) {
	if idx.Contract == "" {
		return "", "", false
	}
	key := contractNameKey(idx.Ecosystem, dep.Package)
	if path := preferredClaimant(idx, idx.Replacements[key], dep.Capability); path != "" {
		return path, StatusCovered, true
	}
	if path := preferredClaimant(idx, idx.Adaptations[key], dep.Capability); path != "" {
		return path, StatusAdapterAvailable, true
	}
	if paths := idx.Capabilities[dep.Capability]; len(paths) > 0 {
		return paths[0], StatusCovered, true
	}
	return "", "", false
}

// reconcileContractDemand marks a demand covered, or adapter-available, by its contract
// replacement. A demand the catalog did not know adopts the replacement package's first
// declared capability so it stops counting as a custom gap. The note is rewritten whenever
// the status or the package changes, so a demand a scan mapped to the configured contract's
// package and a report re-mapped to a fork's never names the package it no longer maps to.
func reconcileContractDemand(idx *FrameworkIndex, dep *DependencyDemand) bool {
	path, status, ok := contractReplacement(idx, dep)
	if !ok {
		return false
	}
	pkg := idx.Packages[path]
	if !slices.Contains(pkg.Capabilities, dep.Capability) && len(pkg.Capabilities) > 0 {
		dep.Capability = pkg.Capabilities[0]
	}
	if dep.Status != status || dep.FrameworkReplacement != path {
		dep.Notes = fmt.Sprintf("%s declares %s for %s", idx.Contract, path, dep.Capability)
	}
	dep.Status, dep.FrameworkReplacement = status, path
	return true
}

// applyContractRelationship applies the relationship the selected contract declares for a
// demand: a foundation it retains, or the package that wraps the library or is tooling for
// it. The library stays either way; it reports false when the contract declares neither.
func applyContractRelationship(idx *FrameworkIndex, dep *DependencyDemand) bool {
	if idx.Contract == "" {
		return false
	}
	key := contractNameKey(idx.Ecosystem, dep.Package)
	if slices.ContainsFunc(idx.Foundations, func(name string) bool { return contractNameKey(idx.Ecosystem, name) == key }) {
		dep.Relationship = &LibraryRelationship{Kind: RelationshipFoundation, Basis: FrameworkCatalogDeclared}
		dep.Status, dep.FrameworkReplacement = StatusNative, ""
		dep.Notes = fmt.Sprintf("%s retains %s as a foundation", idx.Contract, dep.Package)
		return true
	}
	for _, role := range [...]struct {
		kind   LibraryRelationshipKind
		claims map[string][]string
	}{{RelationshipWrappedBy, idx.Wrappers}, {RelationshipTooling, idx.Tooling}} {
		path := preferredClaimant(idx, role.claims[key], dep.Capability)
		if path == "" {
			continue
		}
		pkg := idx.Packages[path]
		if !slices.Contains(pkg.Capabilities, dep.Capability) && len(pkg.Capabilities) > 0 {
			dep.Capability = pkg.Capabilities[0]
		}
		dep.Relationship = &LibraryRelationship{Kind: role.kind, FrameworkPackage: path, Basis: idx.Basis}
		dep.Status, dep.FrameworkReplacement = StatusCovered, ""
		dep.Notes = fmt.Sprintf("%s: %s is %s %s; retain the library", idx.Contract, dep.Package, role.kind, path)
		return true
	}
	return false
}

// isFrameworkModule reports whether pkg is the selected framework module or one of its
// nested modules, which a consumer imports natively rather than as third-party demand.
func isFrameworkModule(idx *FrameworkIndex, pkg string) bool {
	return pkg == idx.Name || isNestedModule(pkg, idx.Name)
}

func markFrameworkNative(idx *FrameworkIndex, dep *DependencyDemand) {
	dep.Capability, dep.Status, dep.FrameworkReplacement = FrameworkNativeCapability, StatusNative, ""
	dep.Relationship = nil
	dep.Notes = fmt.Sprintf("Module of the selected framework %s; retained", idx.Name)
}
