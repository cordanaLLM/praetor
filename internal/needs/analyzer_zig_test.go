package needs

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// nativeDemandNames returns, sorted, the packages of the demands a scan reports in the system
// ecosystem, the native analyzer's.
func nativeDemandNames(row *RepoNeeds) []string {
	var names []string
	for _, dep := range row.Dependencies {
		if dep.Ecosystem == "system" {
			names = append(names, dep.Package)
		}
	}
	slices.Sort(names)
	return names
}

// Positive: a build.zig with its build.zig.zon is a native project of the Zig language. Its
// demands are the packages fetched by url and those vendored under vendor/; a path package of
// the repository's own tree is no demand.
func TestNativeAnalyzerZigBuild_Positive_ManifestDependencies(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "build.zig", "const std = @import(\"std\");\n")
	writeFixture(t, repo, "build.zig.zon", zigManifestFixture)

	analyzer := NewNativeAnalyzer()
	if !analyzer.Detect(repo) {
		t.Fatal("NativeAnalyzer does not detect build.zig")
	}
	row, err := analyzer.Analyze(context.Background(), repo, acmeTargets()["native"])
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if row.Language != "native" || !slices.Equal(row.Languages, []string{"zig"}) {
		t.Errorf("language = %s %v, want native [zig]", row.Language, row.Languages)
	}
	if got, want := nativeDemandNames(row), []string{"physics", "sound-lib", "zlib"}; !slices.Equal(got, want) {
		t.Errorf("demands = %v, want %v", got, want)
	}
	for _, dep := range row.Dependencies {
		if dep.Capability != CapabilityKey("native.external."+cleanDepKey(dep.Package)) {
			t.Errorf("%s capability = %s", dep.Package, dep.Capability)
		}
	}
}

// Positive: the reported shape, a Cargo workspace beside a build.zig that builds the native
// libraries, is one row carrying both halves: the crates and the Zig packages, Rust and Zig.
func TestScanRepoZigBesideCargoWorkspace_Positive(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "Cargo.toml", "[workspace]\nmembers = [\"crates/engine\"]\n\n[workspace.dependencies]\nserde = \"1\"\n")
	writeFixture(t, repo, filepath.Join("crates", "engine", "Cargo.toml"), "[package]\nname = \"engine\"\n\n[dependencies]\nserde = { workspace = true }\n")
	writeFixture(t, repo, filepath.Join("crates", "engine", "native", "shim.c"), "int shim(void) { return 0; }\n")
	writeFixture(t, repo, "build.zig", "const std = @import(\"std\");\n")
	writeFixture(t, repo, "build.zig.zon", `.{ .name = .engine, .version = "0.1.0", .dependencies = .{ .physics = .{ .path = "vendor/physics" } }, .paths = .{""} }`)
	writeFixture(t, repo, filepath.Join("vendor", "physics", "build.zig"), "const std = @import(\"std\");\n")

	row, err := ScanRepo(context.Background(), repo, NewRegistry(nil))
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	for _, language := range []string{"rust", "native", "zig"} {
		if !slices.Contains(append([]string{row.Language}, row.Languages...), language) {
			t.Errorf("languages %s %v lack %s", row.Language, row.Languages, language)
		}
	}
	if slices.Contains(row.Languages, "c") || slices.Contains(row.Languages, "cuda") {
		t.Errorf("a Zig build without a C-family marker reports %v", row.Languages)
	}
	if !hasDependency(*row, "serde") || !hasDependency(*row, "physics") {
		t.Errorf("row lacks a crate or a Zig package: %+v", row.Dependencies)
	}
	if want := []string{"crates/engine"}; !slices.Equal(row.Subprojects, want) {
		t.Errorf("sub-projects = %v, want %v: vendor/ is pruned", row.Subprojects, want)
	}
}

// Boundary: build.zig alone, and build.zig.zon alone, are a native project without demands;
// a Zig build beside meson reports both builds' languages.
func TestNativeAnalyzerZigBuild_Boundary(t *testing.T) {
	for _, marker := range []string{"build.zig", "build.zig.zon"} {
		repo := t.TempDir()
		writeFixture(t, repo, marker, ".{}\n")
		analyzer := NewNativeAnalyzer()
		row, err := analyzer.Analyze(context.Background(), repo, Target{})
		if !analyzer.Detect(repo) || err != nil || len(row.Dependencies) != 0 || !slices.Equal(row.Languages, []string{"zig"}) {
			t.Errorf("%s alone: detect=%v row=%+v err=%v", marker, analyzer.Detect(repo), row, err)
		}
	}
	repo := t.TempDir()
	writeFixture(t, repo, "meson.build", "dep = dependency('zlib')\n")
	writeFixture(t, repo, "build.zig.zon", `.{ .dependencies = .{ .zlib = .{ .url = "https://example.com/zlib.tar.gz" } } }`)
	row, err := NewNativeAnalyzer().Analyze(context.Background(), repo, Target{})
	if err != nil || !slices.Equal(row.Languages, []string{"c", "cpp", "cuda", "zig"}) || len(row.Dependencies) != 1 {
		t.Errorf("meson beside build.zig.zon = %+v, %v; want four languages and zlib once", row, err)
	}
}

// Negative: a malformed build.zig.zon fails the scan with the manifest and the reason, never a
// row without its demands; an empty directory is no native project.
func TestNativeAnalyzerZigBuild_Negative_MalformedManifest(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "build.zig", "const std = @import(\"std\");\n")
	writeFixture(t, repo, "build.zig.zon", ".{\n  .dependencies = .{ .zlib = .{ .hash = \"h\" } },\n")
	_, err := ScanRepo(context.Background(), repo, NewRegistry(nil))
	if err == nil || !strings.Contains(err.Error(), "build.zig.zon") || !strings.Contains(err.Error(), "line 3: expected a value, found end of file") {
		t.Fatalf("ScanRepo() error = %v, want the malformed build.zig.zon named with its reason", err)
	}
	if NewNativeAnalyzer().Detect(t.TempDir()) {
		t.Error("an empty directory is detected as a native project")
	}
}

// Negative: a build.zig under a directory discovery prunes is not a sub-project, and a
// directory holding one outside every checkout is a repository of its own.
func TestDiscoverFleetZigMarkers(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, "Cargo.toml"), "[package]\nname = \"repo\"\n")
	writeRepoFile(t, filepath.Join(repo, "engine", "build.zig"), "const std = @import(\"std\");\n")
	writeRepoFile(t, filepath.Join(repo, "vendor", "physics", "build.zig"), "const std = @import(\"std\");\n")
	writeRepoFile(t, filepath.Join(repo, "third_party", "audio", "build.zig.zon"), ".{}\n")
	writeRepoFile(t, filepath.Join(root, "tool", "build.zig.zon"), ".{}\n")

	layout, err := discoverFleet(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverFleet() error = %v", err)
	}
	var subprojects []string
	roots := make([]string, 0, len(layout.repos))
	for _, found := range layout.repos {
		roots = append(roots, found.root)
		if found.root == repo {
			subprojects = found.subprojects
		}
	}
	if want := []string{repo, filepath.Join(root, "tool")}; !slices.Equal(roots, want) {
		t.Errorf("repositories = %v, want %v", roots, want)
	}
	if want := []string{repo, filepath.Join(repo, "engine")}; !slices.Equal(subprojects, want) {
		t.Errorf("sub-projects = %v, want %v", subprojects, want)
	}
}

// fetchedZigApp writes a Zig application after `zig build`: its manifest fetches dep by url,
// and Zig 0.16 copied the package into zig-pkg/<name>-<version>-<hash>/ with the package's own
// build.zig and build.zig.zon, next to the .zig-cache/ cache and the zig-out/ install prefix.
func fetchedZigApp(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeFixture(t, repo, "build.zig", "const std = @import(\"std\");\n")
	writeFixture(t, repo, "build.zig.zon", `.{ .name = .app, .version = "0.1.0", .dependencies = .{ .dep = .{ .url = "https://example.com/dep.tar.gz", .hash = "dep-0.0.1-h" } }, .paths = .{""} }`)
	fetched := filepath.Join("zig-pkg", "dep-0.0.1-h")
	writeFixture(t, repo, filepath.Join(fetched, "build.zig"), "const std = @import(\"std\");\n")
	writeFixture(t, repo, filepath.Join(fetched, "build.zig.zon"), `.{ .name = .dep, .version = "0.0.1", .dependencies = .{ .inner = .{ .url = "https://example.com/inner.tar.gz", .hash = "inner-1.0.0-h" } }, .paths = .{""} }`)
	writeFixture(t, repo, filepath.Join(fetched, "src", "dep.c"), "int dep(void) { return 0; }\n")
	writeFixture(t, repo, filepath.Join("zig-out", "lib", "build.zig"), "const std = @import(\"std\");\n")
	writeFixture(t, repo, filepath.Join(".zig-cache", "o", "h", "build.zig.zon"), ".{}\n")
	return repo
}

// Positive: after a build, the packages Zig fetched into zig-pkg/ are third-party copies, not
// sub-projects: the repository's demands stay the ones its own build.zig.zon declares, and a
// fetched package's own url dependencies are no demands of the repository.
func TestScanRepoZigToolchainTreesPruned_Positive(t *testing.T) {
	row, err := ScanRepo(context.Background(), fetchedZigApp(t), NewRegistry(nil))
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	if len(row.Subprojects) != 0 || len(row.UnscannedSubprojects) != 0 {
		t.Errorf("toolchain trees became sub-projects: %v unscanned %v", row.Subprojects, row.UnscannedSubprojects)
	}
	if got, want := nativeDemandNames(row), []string{"dep"}; !slices.Equal(got, want) {
		t.Errorf("demands = %v, want %v", got, want)
	}
}

// Negative: discovery prunes the toolchain trees in every repository, so neither a fetched
// package nor the cache or install prefix is a sub-project or a repository of its own.
func TestDiscoverFleetZigToolchainTreesPruned_Negative(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	makeCheckout(t, repo)
	writeRepoFile(t, filepath.Join(repo, "build.zig"), "const std = @import(\"std\");\n")
	for _, tree := range []string{"zig-pkg/dep-0.0.1-h", "zig-out/lib", ".zig-cache/o/h", "engine/zig-pkg/dep-0.0.1-h"} {
		writeRepoFile(t, filepath.Join(repo, filepath.FromSlash(tree), "build.zig.zon"), ".{}\n")
	}
	writeRepoFile(t, filepath.Join(root, "zig-pkg", "loose", "build.zig"), "const std = @import(\"std\");\n")

	layout, err := discoverFleet(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverFleet() error = %v", err)
	}
	if len(layout.repos) != 1 || layout.repos[0].root != repo {
		t.Fatalf("repositories = %+v, want only %s", layout.repos, repo)
	}
	if want := []string{repo}; !slices.Equal(layout.repos[0].subprojects, want) {
		t.Errorf("sub-projects = %v, want %v", layout.repos[0].subprojects, want)
	}
}

// Boundary: the prune matches the exact directory names only; a first-party directory whose
// name resembles a toolchain tree is scanned, and a path package vendored into zig-pkg/ is a
// third-party demand like one under vendor/.
func TestScanRepoZigToolchainTreeLookalikes_Boundary(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "build.zig", "const std = @import(\"std\");\n")
	writeFixture(t, repo, "build.zig.zon", `.{ .name = .app, .dependencies = .{ .pinned = .{ .path = "zig-pkg/pinned" }, .tools = .{ .path = "zig-pkg-tools" } }, .paths = .{""} }`)
	writeFixture(t, repo, filepath.Join("zig-pkg-tools", "build.zig"), "const std = @import(\"std\");\n")
	writeFixture(t, repo, filepath.Join("zig", "build.zig.zon"), ".{}\n")

	row, err := ScanRepo(context.Background(), repo, NewRegistry(nil))
	if err != nil {
		t.Fatalf("ScanRepo() error = %v", err)
	}
	if want := []string{"zig", "zig-pkg-tools"}; !slices.Equal(row.Subprojects, want) {
		t.Errorf("sub-projects = %v, want %v", row.Subprojects, want)
	}
	if got, want := nativeDemandNames(row), []string{"pinned"}; !slices.Equal(got, want) {
		t.Errorf("demands = %v, want %v", got, want)
	}
}

// NativeLanguages names the languages behind the analyzer id "native". Positive: meson or CMake
// is C, C++ and CUDA, a Zig build is Zig. Negative: a directory without a native marker, or with
// build.zig only inside zig-pkg/, has none. Boundary: a Zig build beside CMake has all four.
func TestNativeLanguages(t *testing.T) {
	for _, tc := range []struct {
		files []string
		want  []string
	}{
		{[]string{"meson.build"}, []string{"c", "cpp", "cuda"}},
		{[]string{"CMakeLists.txt"}, []string{"c", "cpp", "cuda"}},
		{[]string{"build.zig"}, []string{"zig"}},
		{nil, nil},
		{[]string{filepath.Join("zig-pkg", "dep", "build.zig"), "main.zig"}, nil},
		{[]string{"CMakeLists.txt", "build.zig.zon"}, []string{"c", "cpp", "cuda", "zig"}},
	} {
		repo := t.TempDir()
		for _, name := range tc.files {
			writeFixture(t, repo, name, "\n")
		}
		if got := NativeLanguages(repo); !slices.Equal(got, tc.want) {
			t.Errorf("%v: NativeLanguages = %v, want %v", tc.files, got, tc.want)
		}
	}
}
