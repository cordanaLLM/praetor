package needs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// RustAnalyzer extracts Rust crate dependencies from Cargo.toml.
type RustAnalyzer struct{}

// NewRustAnalyzer initializes a RustAnalyzer instance.
func NewRustAnalyzer() *RustAnalyzer {
	return &RustAnalyzer{}
}

// Language returns the language identifier.
func (a *RustAnalyzer) Language() string {
	return "rust"
}

// Detect checks if the repository contains Cargo.toml.
func (a *RustAnalyzer) Detect(repoPath string) bool {
	return util.FileExists(filepath.Join(repoPath, "Cargo.toml"))
}

// Analyze extracts the third-party crates Cargo.toml depends on and maps them to
// capabilities of the rust target framework. Path dependencies are crates of this
// repository and are left out (cargoManifest.thirdPartyCrates).
func (a *RustAnalyzer) Analyze(ctx context.Context, repoPath string, target Target) (*RepoNeeds, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	repoName, fallback := projectRepositoryName("", repoPath)
	repoNeeds := &RepoNeeds{
		Version:            1,
		Repository:         repoName,
		RepositoryFallback: fallback,
		Language:           "rust",
		Languages:          []string{"rust"},
		Capabilities:       CapabilityDeclaration{Required: make([]CapabilityKey, 0), Optional: make([]CapabilityKey, 0)},
		Dependencies:       make([]DependencyDemand, 0),
		UpdatedAt:          time.Now().UTC(),
	}

	deps, err := readCargoDependencies(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	for _, pkg := range slices.Sorted(maps.Keys(deps)) {
		demand := rustClassifier.classify(pkg, deps[pkg], target.RoutingKit())
		repoNeeds.Dependencies = append(repoNeeds.Dependencies, demand)
		repoNeeds.Capabilities.Required = appendUniqueCap(repoNeeds.Capabilities.Required, demand.Capability)
	}
	target.applyTo(repoNeeds)

	if declErr := loadExistingDeclarations(ctx, repoPath, repoNeeds); declErr != nil {
		return nil, fmt.Errorf("failed to load existing declarations: %w", declErr)
	}
	calculateReadiness(repoNeeds)
	return repoNeeds, nil
}

// readCargoDependencies returns the third-party crates the Cargo.toml in crateDir
// depends on, keyed by name, with the version each declares.
func readCargoDependencies(ctx context.Context, crateDir string) (map[string]string, error) {
	manifest, err := parseCargoToml(filepath.Join(crateDir, "Cargo.toml"))
	if err != nil {
		return nil, fmt.Errorf("failed to parse Cargo.toml in %q: %w", crateDir, err)
	}
	root, err := cargoWorkspaceRoot(ctx, crateDir, manifest)
	if err != nil {
		return nil, fmt.Errorf("failed to find the Cargo workspace of %q: %w", crateDir, err)
	}
	return manifest.thirdPartyCrates(root), nil
}

// maxCargoWorkspaceAscent bounds how many parent directories the workspace-root search
// visits (HISS-02). Cargo searches every parent directory; the filesystem root ends the
// search well before the bound on any real path.
const maxCargoWorkspaceAscent = 64

// cargoDependency is one crate dependency a Cargo.toml declares.
type cargoDependency struct {
	version string
	// local marks a `path` dependency: a crate of this repository, such as a workspace
	// member, never a registry crate.
	local bool
	// inherited marks `workspace = true`: the workspace root's [workspace.dependencies]
	// holds the declaration.
	inherited bool
}

// with applies one dependency key. Every other key (features, optional,
// default-features, git, ...) leaves the classification unchanged.
func (d cargoDependency) with(key, value string) cargoDependency {
	switch key {
	case "version":
		d.version = value
	case "path":
		d.local = true
	case "workspace":
		d.inherited = value == "true"
	}
	return d
}

// cargoManifest is what the Rust analyzer reads from one Cargo.toml.
type cargoManifest struct {
	// deps holds every dependency table except [workspace.dependencies].
	deps map[string]cargoDependency
	// workspaceDeps holds [workspace.dependencies], the declarations members inherit.
	workspaceDeps map[string]cargoDependency
	// workspace is true when the manifest has a [workspace] table: it is a workspace root.
	workspace bool
}

// inherits reports whether any dependency takes its declaration from the workspace root.
func (m *cargoManifest) inherits() bool {
	for _, dep := range m.deps {
		if dep.inherited {
			return true
		}
	}
	return false
}

// thirdPartyCrates returns the registry and git crates the manifest depends on, with the
// version each declares. A path dependency is first-party and left out: a workspace
// member or a sibling crate of the repository is no third-party gap. A dependency
// inherited with `workspace = true` takes root's [workspace.dependencies] entry, so an
// inherited path crate is first-party too and an inherited registry crate carries the
// root's version; an entry root lacks stays third-party without a version. A
// [workspace.dependencies] entry of this manifest is a demand of its own unless a
// dependency table of the manifest names the crate.
func (m *cargoManifest) thirdPartyCrates(root *cargoManifest) map[string]string {
	crates := make(map[string]string, len(m.deps)+len(m.workspaceDeps))
	for name, dep := range m.deps {
		if decl, found := root.workspaceDeps[name]; dep.inherited && found {
			dep = decl
		}
		if !dep.local {
			crates[name] = dep.version
		}
	}
	for name, dep := range m.workspaceDeps {
		if _, named := m.deps[name]; !named && !dep.local {
			crates[name] = dep.version
		}
	}
	return crates
}

// cargoWorkspaceRoot returns the manifest whose [workspace.dependencies] the crate in
// crateDir inherits from: the crate's own manifest when it has a [workspace] table or
// inherits nothing, otherwise the nearest Cargo.toml above crateDir with a [workspace]
// table, the manifest Cargo searches the parent directories for. Without one it returns
// an empty manifest, so every inherited dependency stays third-party.
func cargoWorkspaceRoot(ctx context.Context, crateDir string, crate *cargoManifest) (*cargoManifest, error) {
	if crate.workspace || !crate.inherits() {
		return crate, nil
	}
	dir, err := filepath.Abs(crateDir)
	if err != nil {
		return nil, fmt.Errorf("resolve %q: %w", crateDir, err)
	}
	for range maxCargoWorkspaceAscent {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		manifestPath := filepath.Join(dir, "Cargo.toml")
		if !util.FileExists(manifestPath) {
			continue
		}
		manifest, parseErr := parseCargoToml(manifestPath)
		if parseErr != nil {
			return nil, parseErr
		}
		if manifest.workspace {
			return manifest, nil
		}
	}
	return &cargoManifest{}, nil
}

// cargoState tracks which Cargo.toml table the line scanner is inside.
type cargoState struct {
	manifest *cargoManifest
	// table receives the entries of the dependency table the scanner is inside, nil
	// outside every dependency table.
	table map[string]cargoDependency
	// crate names the crate when inside a [dependencies.<crate>] sub-table.
	crate string
}

// parseCargoToml reads a Cargo.toml's dependency tables in every spelling Cargo accepts:
// [dependencies], [dev-dependencies], [build-dependencies], [workspace.dependencies],
// [target.'cfg(...)'.dependencies] and the [dependencies.<crate>] sub-table, each entry
// a version string, an inline table (`{ version = "1" }`, `{ path = "../core" }`,
// `{ workspace = true }`) or dotted keys (`cc.workspace = true`). It also records
// whether the manifest is a workspace root.
func parseCargoToml(cargoPath string) (*cargoManifest, error) {
	st := cargoState{manifest: &cargoManifest{
		deps:          make(map[string]cargoDependency),
		workspaceDeps: make(map[string]cargoDependency),
	}}
	if err := scanManifestLines(cargoPath, st.consume); err != nil {
		return nil, err
	}
	return st.manifest, nil
}

// consume classifies a single trimmed Cargo.toml line.
func (s *cargoState) consume(line string) {
	if strings.HasPrefix(line, "[") {
		s.enterTable(util.TOMLTableName(line))
		return
	}
	if s.table == nil || line == "" || strings.HasPrefix(line, "#") {
		return
	}
	key, value, ok := splitTOMLKey(line)
	if !ok {
		return
	}
	if s.crate != "" {
		s.table[s.crate] = s.table[s.crate].with(key, tomlScalar(value))
		return
	}
	if crate, field, dotted := strings.Cut(key, "."); dotted {
		crate = unquoteTOMLKey(crate)
		s.table[crate] = s.table[crate].with(unquoteTOMLKey(field), tomlScalar(value))
		return
	}
	s.table[key] = parseCargoDependency(value)
}

// parseCargoDependency reads the value of a `name = value` dependency entry: a version
// string or an inline table of dependency keys.
func parseCargoDependency(value string) cargoDependency {
	if !strings.HasPrefix(value, "{") {
		return cargoDependency{version: tomlScalar(value)}
	}
	var dep cargoDependency
	util.TOMLInlineTableFields(value, func(key, fieldValue string) {
		dep = dep.with(unquoteTOMLKey(key), tomlScalar(fieldValue))
	})
	return dep
}

// enterTable updates the scanner state for a TOML table name in util.TOMLTableName form.
func (s *cargoState) enterTable(name string) {
	s.table, s.crate = nil, ""
	segments := strings.Split(name, ".")
	for i := range segments {
		segments[i] = unquoteTOMLKey(segments[i])
	}
	if segments[0] == "workspace" {
		s.manifest.workspace = true
	}
	last := len(segments) - 1
	switch {
	case isCargoDependencyTable(segments[last]):
		s.table = s.manifest.tableFor(segments[0])
	case last >= 1 && isCargoDependencyTable(segments[last-1]):
		s.table, s.crate = s.manifest.tableFor(segments[0]), segments[last]
		if _, seen := s.table[s.crate]; !seen {
			s.table[s.crate] = cargoDependency{}
		}
	}
}

// tableFor returns the map filled by a dependency table whose name starts with first.
func (m *cargoManifest) tableFor(first string) map[string]cargoDependency {
	if first == "workspace" {
		return m.workspaceDeps
	}
	return m.deps
}

// isCargoDependencyTable reports whether a table name holds crate dependencies.
func isCargoDependencyTable(name string) bool {
	return name == "dependencies" || name == "dev-dependencies" ||
		name == "build-dependencies"
}

// unquoteTOMLKey trims the white space and quotes around one TOML key segment.
func unquoteTOMLKey(key string) string {
	return strings.Trim(strings.TrimSpace(key), `"'`)
}

// splitTOMLKey splits `key = value` as util.TOMLKeyValue does and unquotes the key; a key
// that unquotes to nothing is no assignment.
func splitTOMLKey(line string) (string, string, bool) {
	key, value, ok := util.TOMLKeyValue(line)
	key = unquoteTOMLKey(key)
	return key, value, ok && key != ""
}

// tomlScalar normalises a raw TOML value: a trailing comment is dropped and a string
// loses its quotes.
func tomlScalar(value string) string {
	if comment := strings.Index(value, "#"); comment >= 0 {
		value = strings.TrimSpace(value[:comment])
	}
	return strings.Trim(value, `"',`)
}

// splitTOMLAssignment splits `key = value` and normalises the value: a bare string loses
// its quotes, an inline table is reduced to its `version` field, empty when the table
// pins the dependency by path, git revision or workspace.
func splitTOMLAssignment(line string) (string, string, bool) {
	name, value, ok := splitTOMLKey(line)
	if !ok {
		return "", "", false
	}
	if !strings.HasPrefix(value, "{") {
		return name, tomlScalar(value), true
	}
	version := ""
	util.TOMLInlineTableFields(value, func(key, fieldValue string) {
		if unquoteTOMLKey(key) == "version" {
			version = tomlScalar(fieldValue)
		}
	})
	return name, version, true
}
