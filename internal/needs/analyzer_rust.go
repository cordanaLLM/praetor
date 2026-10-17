package needs

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
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
func (d cargoDependency) with(key string, value any) cargoDependency {
	switch key {
	case "version":
		version, isString := value.(string)
		if isString {
			d.version = version
		}
	case "path":
		path, isString := value.(string)
		d.local = isString && path != ""
	case "workspace":
		inherited, isBool := value.(bool)
		d.inherited = isBool && inherited
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

// parseCargoToml reads a Cargo manifest through the TOML parser.
func parseCargoToml(cargoPath string) (*cargoManifest, error) {
	data, err := readManifest(cargoPath)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := util.DecodeTOML(cargoPath, data, &document); err != nil {
		return nil, err
	}
	manifest := &cargoManifest{deps: make(map[string]cargoDependency), workspaceDeps: make(map[string]cargoDependency)}
	if workspace, ok := document["workspace"].(map[string]any); ok {
		manifest.workspace = true
		if err := addCargoDependencies(manifest.workspaceDeps, workspace["dependencies"]); err != nil {
			return nil, fmt.Errorf("%s: %w", cargoPath, err)
		}
	}
	for _, table := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		if err := addCargoDependencies(manifest.deps, document[table]); err != nil {
			return nil, fmt.Errorf("%s: %w", cargoPath, err)
		}
	}
	if err := addTargetCargoDependencies(manifest.deps, document["target"]); err != nil {
		return nil, fmt.Errorf("%s: %w", cargoPath, err)
	}
	return manifest, nil
}

// addTargetCargoDependencies reads dependency tables nested under Cargo target selectors.
func addTargetCargoDependencies(destination map[string]cargoDependency, value any) error {
	targets, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := slices.Sorted(maps.Keys(targets))
	if len(keys) > MaxScannedLines {
		return fmt.Errorf("target tables exceed %d", MaxScannedLines)
	}
	for _, target := range keys {
		tables, isTable := targets[target].(map[string]any)
		if !isTable {
			continue
		}
		for _, table := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
			if err := addCargoDependencies(destination, tables[table]); err != nil {
				return err
			}
		}
	}
	return nil
}

// addCargoDependencies reads a dependency table into destination.
func addCargoDependencies(destination map[string]cargoDependency, value any) error {
	entries, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := slices.Sorted(maps.Keys(entries))
	if len(keys) > MaxScannedLines {
		return fmt.Errorf("dependency entries exceed %d", MaxScannedLines)
	}
	for _, name := range keys {
		dependency := cargoDependency{}
		if fields, ok := entries[name].(map[string]any); ok {
			fieldNames := slices.Sorted(maps.Keys(fields))
			if len(fieldNames) > MaxScannedLines {
				return fmt.Errorf("fields for %s exceed %d", name, MaxScannedLines)
			}
			for _, field := range fieldNames {
				dependency = dependency.with(field, fields[field])
			}
		} else {
			dependency = dependency.with("version", entries[name])
		}
		destination[name] = dependency
	}
	return nil
}
