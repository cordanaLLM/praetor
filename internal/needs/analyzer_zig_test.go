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
