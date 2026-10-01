package cifilter_test

import (
	"context"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/cifilter"
)

// codeOnly fails unless path alone classifies as code, not unclassified and not configuration,
// and selects the code gates without context sync: the decision a plain .go or .rs change gets.
func codeOnly(t *testing.T, path string) {
	t.Helper()
	cs := cifilter.ClassifyChanges([]string{path})
	if !cs.CodeChanged || cs.UnclassifiedChanged || cs.ConfigChanged || cs.DocsOnly {
		t.Errorf("%s: want code only, got code=%t unclassified=%t config=%t docsOnly=%t",
			path, cs.CodeChanged, cs.UnclassifiedChanged, cs.ConfigChanged, cs.DocsOnly)
		return
	}
	d := cifilter.MakeDecision(cs, false)
	if !d.RunTests || !d.RunLinters || !d.RunSecurity || d.RunContextSync || d.SkipHeavyGates {
		t.Errorf("%s: want tests, linters and security without context sync, got %+v", path, d)
	}
}

// Positive (#570): Zig, the C++ sources and headers, HIP and shading-language sources are code.
// Before, each was unclassified: tests still ran, but code_changed was false and context sync
// ran as if configuration had changed. The C-family rows come from the HISS scanner's table.
func TestSourceKindsHissDoesNotListAreCode(t *testing.T) {
	for _, path := range []string{
		"build.zig", "src/main.zig",
		"engine/render.cc", "engine/render.cxx", "engine/render.hpp", "engine/render.hh", "gpu/kernel.hip",
		"assets/shaders/post/blur.comp", "assets/shaders/mesh.vert", "assets/shaders/mesh.frag",
		"assets/shaders/common.glsl", "assets/shaders/hair.geom", "assets/shaders/terrain.tesc",
		"assets/shaders/terrain.tese", "assets/shaders/rt/primary.rgen", "assets/shaders/rt/sphere.rint",
		"assets/shaders/rt/alpha.rahit", "assets/shaders/rt/hit.rchit", "assets/shaders/rt/sky.rmiss",
		"assets/shaders/rt/call.rcall", "assets/shaders/tonemap.hlsl", "web/shaders/blit.wgsl",
		"apple/shaders/blit.metal",
	} {
		codeOnly(t, path)
	}
}

// Positive (#570): Cargo keeps integration tests and benchmarks in tests/ and benches/ beside
// each crate's src/, so those directories are tests at any depth. The file stays code too.
func TestNestedTestDirectoriesAreTests(t *testing.T) {
	for _, path := range []string{
		"crates/client/tests/integration.rs", "crates/engine/benches/throughput.rs",
		"benches/root.rs", "tests/root.rs", "test/root.go", "service/src/test/java/AppTest.java",
	} {
		cs := cifilter.ClassifyChanges([]string{path})
		if !cs.TestsChanged || !cs.CodeChanged || cs.UnclassifiedChanged {
			t.Errorf("%s: want tests and code, got tests=%t code=%t unclassified=%t",
				path, cs.TestsChanged, cs.CodeChanged, cs.UnclassifiedChanged)
		}
		if d := cifilter.MakeDecision(cs, false); !d.RunTests || d.RunContextSync {
			t.Errorf("%s: want tests without context sync, got %+v", path, d)
		}
	}
}

// Negative: a test location names no file kind. A script or fixture under a tests/ directory,
// nested or at the root, still fails closed to linters and security; before, a root tests/
// fixture selected tests alone.
func TestUnknownKindUnderTestDirectoryFailsClosed(t *testing.T) {
	for _, path := range []string{"crates/client/tests/run.ps1", "tests/fixtures/input.bin", "benches/data/sample.dat"} {
		cs := cifilter.ClassifyChanges([]string{path})
		d := cifilter.MakeDecision(cs, false)
		if !cs.TestsChanged || !cs.UnclassifiedChanged || !d.RunLinters || !d.RunSecurity || d.SkipHeavyGates {
			t.Errorf("%s: want a test path that fails closed, got %+v / %+v", path, cs, d)
		}
	}
}

// Negative: names that only resemble a test directory or a code extension are neither.
func TestLookalikeTestDirectoriesAndExtensionsAreNotClassified(t *testing.T) {
	for _, path := range []string{
		"internal/testsupport/helper.go", "internal/hiss/testdata/sample.go", "crates/client/src/tests.rs",
		"crates/engine/benchmarks/run.rs", "attest/verify.go",
	} {
		if cs := cifilter.ClassifyChanges([]string{path}); cs.TestsChanged {
			t.Errorf("%s: not under a test directory, got tests=true", path)
		}
	}
	for _, path := range []string{"build.zigx", "engine/render.hhh", "shaders/blur.glslx", "shaders/blur.frag.bak", "shaders/blur.mesh"} {
		if cs := cifilter.ClassifyChanges([]string{path}); cs.CodeChanged || !cs.UnclassifiedChanged {
			t.Errorf("%s: want unclassified, got code=%t unclassified=%t", path, cs.CodeChanged, cs.UnclassifiedChanged)
		}
	}
}

// Boundary: extension case does not matter, a shader under docs/ keeps the docs/ prefix rule,
// and a file named tests with no directory above it is not a test.
func TestSourceKindBoundaries(t *testing.T) {
	for _, path := range []string{"Build.ZIG", "engine/Render.HH", "shaders/Blur.COMP", "shaders/Mesh.Frag"} {
		codeOnly(t, path)
	}
	if cs := cifilter.ClassifyChanges([]string{"docs/figures/blur.comp"}); !cs.DocsOnly || cs.CodeChanged {
		t.Errorf("a shader under docs/ must stay docs-only, got %+v", cs)
	}
	if cs := cifilter.ClassifyChanges([]string{"tests"}); cs.TestsChanged {
		t.Errorf("a root file named tests is not under a test directory, got %+v", cs)
	}
}

// Positive, end to end (#570): the two commits from the report, run through AnalyzeChanges
// against real git history, give a plain source decision.
func TestAnalyzeChangesShaderZigAndNestedTestCommits(t *testing.T) {
	dir := gitSandbox(t)
	writeRepoFile(t, dir, "Cargo.toml", "[workspace]\n")
	runRepoGit(t, dir, "init", "-q", "-b", "main")
	runRepoGit(t, dir, "add", ".")
	runRepoGit(t, dir, "commit", "-q", "-m", "base")
	analyze := func(message string, files ...string) *cifilter.FilterDecision {
		t.Helper()
		for _, name := range files {
			writeRepoFile(t, dir, name, "// probe\n")
		}
		runRepoGit(t, dir, "add", ".")
		runRepoGit(t, dir, "commit", "-q", "-m", message)
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		decision, err := cifilter.AnalyzeChanges(ctx, cifilter.FilterOptions{RepoDir: dir, BaseRef: "HEAD~1"})
		if err != nil {
			t.Fatal(err)
		}
		return decision
	}

	d := analyze("shader and zig", "assets/shaders/post/blur.comp", "build.zig")
	if cs := d.ChangeSet; !cs.CodeChanged || cs.ConfigChanged || cs.UnclassifiedChanged || d.RunContextSync || !d.RunTests {
		t.Errorf("shader and zig commit: want code without context sync, got %+v / %+v", d, cs)
	}
	d = analyze("nested tests", "crates/client/tests/integration.rs")
	if cs := d.ChangeSet; !cs.TestsChanged || !cs.CodeChanged || d.RunContextSync {
		t.Errorf("nested test commit: want tests and code without context sync, got %+v / %+v", d, cs)
	}
}
