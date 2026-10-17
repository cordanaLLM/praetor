package needs

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// demandVersions returns a row's dependencies as package -> version.
func demandVersions(row *RepoNeeds) map[string]string {
	versions := make(map[string]string, len(row.Dependencies))
	for _, dep := range row.Dependencies {
		versions[dep.Package] = dep.Version
	}
	return versions
}

// scanRow scans the repository at path with the acme targets, failing the test on error.
func scanRow(t *testing.T, path string) *RepoNeeds {
	t.Helper()
	return scanRowWith(t, acmeRegistry(t), path)
}

// scanRowWith scans the repository at path with registry, failing the test on error. A
// test that changes directory loads the registry first: the acme contracts are relative
// to the package directory.
func scanRowWith(t *testing.T, registry *AnalyzerRegistry, path string) *RepoNeeds {
	t.Helper()
	row, err := ScanRepo(context.Background(), path, registry)
	if err != nil {
		t.Fatalf("ScanRepo(%s) error = %v", path, err)
	}
	return row
}

// analyzeCrate runs the Rust analyzer alone on one crate directory.
func analyzeCrate(t *testing.T, dir string) map[string]string {
	t.Helper()
	row, err := NewRustAnalyzer().Analyze(context.Background(), dir, acmeTargets()["rust"])
	if err != nil {
		t.Fatalf("Analyze(%s) error = %v", dir, err)
	}
	return demandVersions(row)
}

// A Cargo path dependency is a crate of the repository, never a third-party gap. Without
// the fix the sibling crate was reported as rust.external.helper next to serde.
func TestCargoPathDependenciesAreFirstParty_3D(t *testing.T) {
	// Positive: one crate with a path dependency on a nested crate reports serde only,
	// and the nested crate is still scanned as a sub-project.
	app := t.TempDir()
	makeCheckout(t, app)
	writeRepoFile(t, filepath.Join(app, "Cargo.toml"), "[package]\nname = \"app\"\nversion = \"0.1.0\"\n\n"+
		"[dependencies]\nhelper = { path = \"helper\" }\nserde = \"1\"\n")
	writeRepoFile(t, filepath.Join(app, "helper", "Cargo.toml"), "[package]\nname = \"helper\"\nversion = \"0.1.0\"\n")
	row := scanRow(t, app)
	if got := demandVersions(row); !maps.Equal(got, map[string]string{"serde": "1"}) {
		t.Errorf("third-party crates = %v, want only serde", got)
	}
	if !slices.Equal(row.Subprojects, []string{"helper"}) || slices.Contains(row.Capabilities.Required, "rust.external.helper") {
		t.Errorf("row = subprojects %v capabilities %v, want helper scanned and never a gap", row.Subprojects, row.Capabilities.Required)
	}

	// Negative: registry crates in an inline table and git crates stay third-party.
	crate := t.TempDir()
	writeRepoFile(t, filepath.Join(crate, "Cargo.toml"), "[dependencies]\n"+
		"tokio = { version = \"1.38\", features = [\"full\"] }\n"+
		"forked = { git = \"https://example.com/forked.git\", branch = \"main\" }\n")
	if got := analyzeCrate(t, crate); !maps.Equal(got, map[string]string{"tokio": "1.38", "forked": ""}) {
		t.Errorf("registry and git crates = %v, want tokio 1.38 and forked", got)
	}

	// Boundary: path plus version (the published fallback), the sub-table form, the dotted
	// form and a target table are all first-party; a commented header still opens its table.
	forms := t.TempDir()
	writeRepoFile(t, filepath.Join(forms, "Cargo.toml"), "[dependencies] # engine crates\n"+
		"core = { version = \"0.1\", path = \"../core\" }\n"+
		"pal.path = \"../pal\"\n"+
		"ash = \"0.38\"\n\n"+
		"[dependencies.render]\npath = \"../render\"\nversion = \"0.1\"\n\n"+
		"[target.'cfg(windows)'.dev-dependencies]\nwin_shim = { path = \"../win_shim\" }\n")
	if got := analyzeCrate(t, forms); !maps.Equal(got, map[string]string{"ash": "0.38"}) {
		t.Errorf("crates with path forms = %v, want only ash", got)
	}
}

// writeCargoWorkspace lays out a virtual workspace whose members wire each other through
// [workspace.dependencies] path entries, sibling path dependencies and dotted inheritance.
func writeCargoWorkspace(t *testing.T, root string) {
	t.Helper()
	writeRepoFile(t, filepath.Join(root, "Cargo.toml"), "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n\n"+
		"[workspace.dependencies]\n"+
		"engine_core = { path = \"crates/engine_core\" }\n"+
		"ash = \"0.38\"\n"+
		"wasmtime = { version = \"25\", default-features = false }\n")
	writeRepoFile(t, filepath.Join(root, "crates", "engine_core", "Cargo.toml"),
		"[package]\nname = \"engine_core\"\nversion = \"0.1.0\"\n\n[dependencies]\nash = { workspace = true }\n")
	writeRepoFile(t, filepath.Join(root, "crates", "engine_pal", "Cargo.toml"),
		"[package]\nname = \"engine_pal\"\nversion = \"0.1.0\"\n")
	writeRepoFile(t, filepath.Join(root, "crates", "engine_app", "Cargo.toml"),
		"[package]\nname = \"engine_app\"\nversion = \"0.1.0\"\n\n[dependencies]\n"+
			"engine_core = { workspace = true, features = [\"gpu\"] }\n"+
			"engine_pal = { path = \"../engine_pal\" }\n"+
			"wasmtime.workspace = true\n"+
			"wat = \"1\"\n")
}

// Workspace members inherit [workspace.dependencies] with `workspace = true`: an inherited
// path crate is first-party and an inherited registry crate carries the root's version.
func TestCargoWorkspaceMembersAreFirstParty_3D(t *testing.T) {
	// Positive: the repository row lists the three registry crates only, with the
	// versions the workspace root declares.
	root := t.TempDir()
	makeCheckout(t, root)
	writeCargoWorkspace(t, root)
	want := map[string]string{"ash": "0.38", "wasmtime": "25", "wat": "1"}
	if got := demandVersions(scanRow(t, root)); !maps.Equal(got, want) {
		t.Errorf("workspace third-party crates = %v, want %v", got, want)
	}
	// A member scanned on its own finds the workspace root above it, as Cargo does.
	if got := analyzeCrate(t, filepath.Join(root, "crates", "engine_app")); !maps.Equal(got, map[string]string{"wasmtime": "25", "wat": "1"}) {
		t.Errorf("member crates = %v, want wasmtime 25 and wat 1", got)
	}

	// Negative: an inherited crate the root does not declare, or a crate with no workspace
	// root above it, stays third-party without a version.
	writeRepoFile(t, filepath.Join(root, "crates", "engine_net", "Cargo.toml"),
		"[package]\nname = \"engine_net\"\n\n[dependencies]\nmio = { workspace = true }\n")
	if got := analyzeCrate(t, filepath.Join(root, "crates", "engine_net")); !maps.Equal(got, map[string]string{"mio": ""}) {
		t.Errorf("undeclared inherited crate = %v, want mio without a version", got)
	}
	orphan := t.TempDir()
	writeRepoFile(t, filepath.Join(orphan, "Cargo.toml"), "[dependencies]\nserde.workspace = true\n")
	if got := analyzeCrate(t, orphan); !maps.Equal(got, map[string]string{"serde": ""}) {
		t.Errorf("crate without a workspace root = %v, want serde without a version", got)
	}

	// Boundary: the nearest workspace root wins. A nested workspace that declares nothing
	// stops the search, so its member's inherited crate is not resolved against the outer
	// root's path entry.
	inner := filepath.Join(root, "tools", "inner")
	writeRepoFile(t, filepath.Join(inner, "Cargo.toml"), "[workspace]\nmembers = [\"cli\"]\n")
	writeRepoFile(t, filepath.Join(inner, "cli", "Cargo.toml"),
		"[package]\nname = \"cli\"\n\n[dependencies]\nengine_core = { workspace = true }\n")
	if got := analyzeCrate(t, filepath.Join(inner, "cli")); !maps.Equal(got, map[string]string{"engine_core": ""}) {
		t.Errorf("nested workspace member = %v, want engine_core unresolved by the inner root", got)
	}
}

// A workspace root that cannot be read fails the member's scan rather than guessing, and
// a cancelled search stops before reading any parent manifest.
func TestCargoWorkspaceRootErrors_3D(t *testing.T) {
	// Positive: a member that inherits nothing never searches, whatever sits above it.
	root := t.TempDir()
	writeRepoFile(t, filepath.Join(root, "Cargo.toml"), strings.Repeat("#", 1024*1024+1))
	plain := filepath.Join(root, "plain")
	writeRepoFile(t, filepath.Join(plain, "Cargo.toml"), "[dependencies]\nserde = \"1\"\n")
	if got := analyzeCrate(t, plain); !maps.Equal(got, map[string]string{"serde": "1"}) {
		t.Errorf("non-inheriting crate = %v, want serde 1", got)
	}

	// Negative: an oversized workspace-root candidate is an error naming the member.
	member := filepath.Join(root, "member")
	writeRepoFile(t, filepath.Join(member, "Cargo.toml"), "[dependencies]\nserde = { workspace = true }\n")
	_, err := NewRustAnalyzer().Analyze(context.Background(), member, acmeTargets()["rust"])
	if !errors.Is(err, util.ErrFileTooLarge) || !strings.Contains(err.Error(), "Cargo workspace") {
		t.Errorf("Analyze error = %v, want the oversized root manifest reported", err)
	}

	// Boundary: a cancelled context ends the search with the context's error.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	crate := &cargoManifest{deps: map[string]cargoDependency{"serde": {inherited: true}}}
	if _, err := cargoWorkspaceRoot(ctx, member, crate); !errors.Is(err, context.Canceled) {
		t.Errorf("cargoWorkspaceRoot error = %v, want context.Canceled", err)
	}
}

// Systems and graphics crates take the capability keys the native catalog uses, so one
// capability has one key whichever language binds it. Without the catalog rows ash was
// rust.external.ash beside the native gpu.vulkan.
func TestRustCatalogSystemsAndGraphics_3D(t *testing.T) {
	// Positive: the Vulkan, CUDA and OpenCL bindings share the native keys, and the Wasm
	// runtimes share one key of their own.
	shared := map[string]string{"ash": "vulkan", "vulkano": "vulkan", "cudarc": "cuda", "opencl3": "opencl", "ocl": "opencl"}
	for crate, native := range shared {
		got := rustClassifier.classify(crate, "", "").Capability
		if want := nativeClassifier.classify(native, "", "").Capability; got != want {
			t.Errorf("crate %s = %s, want the native %s key %s", crate, got, native, want)
		}
	}
	for _, crate := range []string{"wasmtime", "wasmer", "wasmi"} {
		if got := rustClassifier.classify(crate, "", "").Capability; got != "runtime.wasm" {
			t.Errorf("crate %s = %s, want runtime.wasm", crate, got)
		}
	}

	// Negative: an unlisted crate is still an external capability.
	if got := rustClassifier.classify("engine_core", "", "").Capability; got != "rust.external.engine_core" {
		t.Errorf("unlisted crate = %s, want rust.external.engine_core", got)
	}

	// Boundary: the lookup folds case, and every catalog row is a lower-case crate with a
	// <domain>.<name> key outside the external prefix, and notes.
	if got := rustClassifier.classify("WGPU", "", "").Capability; got != "gpu.webgpu" {
		t.Errorf("WGPU = %s, want gpu.webgpu", got)
	}
	for crate, mapping := range rustCatalog {
		domain, name, ok := strings.Cut(string(mapping.Capability), ".")
		if !ok || domain == "" || name == "" || domain == "rust" || crate != strings.ToLower(crate) || mapping.Notes == "" {
			t.Errorf("catalog row %s = %+v", crate, mapping)
		}
	}
}
