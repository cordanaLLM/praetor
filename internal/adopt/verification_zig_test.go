package adopt

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

// zigWorkspace is the shape #566 reports, with generic names: a Cargo workspace whose crate links
// a C shim, beside a build.zig that builds the native libraries the crates link.
func zigWorkspace() map[string]string {
	return map[string]string{
		"Cargo.toml":                   "[workspace]\nmembers = [\"crates/engine\"]\n",
		"crates/engine/Cargo.toml":     cargoToml,
		"crates/engine/native/shim.c":  cSource,
		"build.zig":                    "const std = @import(\"std\");\n",
		"build.zig.zon":                ".{ .name = .engine, .version = \"0.1.0\", .paths = .{\"\"} }\n",
		"vendor/physics/src/physics.c": cSource,
	}
}

// zigNativeStepReason is the reason a plan with a Zig build and no test command states.
const zigNativeStepReason = "Select and exercise the project's configured native test commands for build.zig; a governance profile cannot select them."

// Positive: `zig build` runs ahead of every language build, the language tests stay the plan's
// tests, and the rendered Makefile builds the native libraries first.
func TestVerificationZigBuildRunsAheadOfLanguageBuilds(t *testing.T) {
	_, plan := verificationFixture(t, zigWorkspace())
	if plan.Status != verificationDeclared || !reflect.DeepEqual(plan.Runtimes, []string{"cargo", "zig"}) {
		t.Fatalf("zig workspace plan = %+v", plan)
	}
	if want := [][]string{{"zig", "build"}, {"cargo", "build", "--locked"}}; !reflect.DeepEqual(plan.Build, want) {
		t.Errorf("build = %q, want %q", plan.Build, want)
	}
	if want := [][]string{{"cargo", "test", "--locked"}}; !reflect.DeepEqual(plan.Test, want) {
		t.Errorf("test = %q, want %q", plan.Test, want)
	}
	makefile := buildMakefile(plan)
	zig, cargo := strings.Index(makefile, "@exec 'zig' 'build'"), strings.Index(makefile, "@exec 'cargo' 'build' '--locked'")
	if zig < 0 || cargo < 0 || zig > cargo {
		t.Errorf("Makefile does not build with zig before cargo:\n%s", makefile)
	}
	_, mixed := verificationFixture(t, map[string]string{
		"build.zig": "", "go.mod": "module example.com/engine\n",
		"package.json": `{"scripts":{"build":"fixture build","test":"fixture test"}}`,
	})
	if want := [][]string{{"zig", "build"}, {"npm", "run", "build"}, {"go", "build", "-v", "./..."}}; mixed.Status != verificationDeclared || !reflect.DeepEqual(mixed.Build, want) {
		t.Errorf("mixed build = %q (%s), want %q", mixed.Build, mixed.Status, want)
	}
}

// Negative: a Zig build with no language marker declaring tests is unavailable and names the
// missing native test step, as a meson or CMake marker does; its test recipe fails. A manifest
// without the build script, and a symlinked build script, select nothing.
func TestVerificationZigBuildWithoutTestsUnavailable(t *testing.T) {
	root, plan := verificationFixture(t, map[string]string{"build.zig": "const std = @import(\"std\");\n"})
	if plan.Status != verificationUnavailable || !reflect.DeepEqual(plan.Runtimes, []string{"zig"}) || plan.Reasons[0] != zigNativeStepReason {
		t.Fatalf("build.zig alone = %+v", plan)
	}
	mustWrite(t, filepath.Join(root, "Makefile"), buildMakefile(plan))
	if _, err := util.RunCommand(t.Context(), root, "make", "--no-print-directory", "test"); err == nil {
		t.Fatal("unavailable Zig test recipe succeeded")
	}
	_, manifestOnly := verificationFixture(t, map[string]string{"build.zig.zon": ".{}\n"})
	if manifestOnly.Status != verificationUnavailable || len(manifestOnly.Runtimes) != 0 {
		t.Errorf("build.zig.zon alone selected a runtime: %+v", manifestOnly)
	}
	_, meson := verificationFixture(t, map[string]string{"meson.build": "project('fixture', 'c')\n"})
	if want := "Select and exercise the project's configured native test/build commands for meson.build; a governance profile cannot select them."; meson.Reasons[0] != want {
		t.Errorf("meson reason = %q, want %q", meson.Reasons[0], want)
	}
	linked := t.TempDir()
	mustWrite(t, filepath.Join(linked, "Cargo.toml"), cargoToml)
	outside := filepath.Join(t.TempDir(), "build.zig")
	mustWrite(t, outside, "const std = @import(\"std\");\n")
	if err := os.Symlink(outside, filepath.Join(linked, "build.zig")); err != nil {
		// Windows creates a symlink only with administrator rights or developer mode enabled.
		if runtime.GOOS != "windows" {
			t.Fatal(err)
		}
		t.Logf("symlink case not run: this platform cannot create the link (%v)", err)
		return
	}
	if _, err := resolveVerificationPlan(t.Context(), linked); err == nil || !strings.Contains(err.Error(), "build.zig that is not a regular file") {
		t.Fatalf("symlinked build.zig = %v, want it refused", err)
	}
}

// Boundary: a build script past the metadata byte bound is still a marker, since its content is
// never read, and a build.zig below the root, under vendor/ or build/, is no marker of the
// repository's verification.
func TestVerificationZigBuildMarkerBounds(t *testing.T) {
	large := "// " + strings.Repeat("x", maxVerificationInputBytes) + "\n"
	_, plan := verificationFixture(t, map[string]string{"Cargo.toml": cargoToml, "build.zig": large})
	if plan.Status != verificationDeclared || !reflect.DeepEqual(plan.Runtimes, []string{"cargo", "zig"}) {
		t.Errorf("large build.zig = %+v, want the Zig step", plan)
	}
	_, nested := verificationFixture(t, map[string]string{
		"go.mod": "module example.com/engine\n", "tools/build.zig": "", "vendor/lib/build.zig": "", "build/build.zig": "",
	})
	if nested.Status != verificationDeclared || !reflect.DeepEqual(nested.Runtimes, []string{"go"}) {
		t.Errorf("nested build.zig selected a step: %+v", nested)
	}
}

// Positive: the harness of a Zig-built Cargo workspace states the Rust clauses and, through the
// crate's C shim, the C/C++ clauses, lists `zig build` first among its commands, and states no Go
// clause. Boundary: a Zig build with no C or C++ source is a known language set that no labelled
// clause names, not the unknown set that renders every clause.
func TestAdoptedHarnessOfZigBuiltWorkspace(t *testing.T) {
	harness := adoptedHarness(t, "native-gpu-systems", zigWorkspace())
	cells := directiveCells(t, harness)
	for _, want := range []string{"C/C++: zero `goto`", "C/C++: zero banned libc", "Rust: zero `.unwrap()` / `.expect()` outside tests"} {
		if !strings.Contains(cells, want) {
			t.Errorf("harness lacks %q:\n%s", want, cells)
		}
	}
	if strings.Contains(cells, "Go:") {
		t.Errorf("harness states a Go clause:\n%s", cells)
	}
	zig, cargo := strings.Index(harness, "'zig' 'build'"), strings.Index(harness, "'cargo' 'build' '--locked'")
	if zig < 0 || cargo < 0 || zig > cargo {
		t.Errorf("harness does not list zig build before cargo build:\n%s", harness)
	}
	facts, _, err := RepositoryHISSFacts(t.Context(), writeRepo(t, map[string]string{"build.zig": "", "src/main.zig": "pub fn main() void {}\n"}), nil)
	if want := (hisscatalog.Facts{Languages: hisscatalog.LanguageOther, CeilingFuncLOC: config.AuditMaxFuncLOC}); err != nil || facts != want {
		t.Errorf("Zig-only facts = %+v, %v; want %+v", facts, err, want)
	}
}

// builtZigRepository is a pure-Zig repository after `zig build`: Zig 0.16 fetched a package with
// its own build.zig and C source into zig-pkg/, installed a header into the zig-out/ prefix and
// cached a translated header in .zig-cache/. None of the three trees is the repository's own.
func builtZigRepository() map[string]string {
	return map[string]string{
		"build.zig":    "const std = @import(\"std\");\n",
		"src/main.zig": "pub fn main() void {}\n",

		"zig-pkg/dep-0.0.1-h/build.zig":     "const std = @import(\"std\");\n",
		"zig-pkg/dep-0.0.1-h/build.zig.zon": ".{}\n",
		"zig-pkg/dep-0.0.1-h/src/dep.c":     cSource,
		"zig-out/include/dep.h":             "int probe(void);\n",
		".zig-cache/o/h/cimport.h":          "int probe(void);\n",
	}
}

// Positive: the C sources in the trees Zig writes select no C/C++ source language, so a pure-Zig
// repository keeps the known language set no labelled clause names. Negative: the same C source in
// a first-party directory whose name only resembles a toolchain tree is the repository's own.
func TestVerificationSkipsZigToolchainTrees(t *testing.T) {
	root := writeRepo(t, builtZigRepository())
	plan, err := ObserveVerificationPlanWithLimits(t.Context(), root, nil)
	if err != nil || len(plan.SourceLanguages) != 0 {
		t.Fatalf("built Zig repository = %+v, %v; want no source language", plan, err)
	}
	facts, _, err := RepositoryHISSFacts(t.Context(), root, nil)
	if want := (hisscatalog.Facts{Languages: hisscatalog.LanguageOther, CeilingFuncLOC: config.AuditMaxFuncLOC}); err != nil || facts != want {
		t.Errorf("built Zig repository facts = %+v, %v; want %+v", facts, err, want)
	}
	mustWrite(t, filepath.Join(root, "zig-pkg-tools", "shim.c"), cSource)
	facts, _, err = RepositoryHISSFacts(t.Context(), root, nil)
	if err != nil || facts.Languages&hisscatalog.LanguageC == 0 {
		t.Errorf("first-party zig-pkg-tools/shim.c facts = %+v, %v; want C/C++ among the languages", facts, err)
	}
}

// Boundary: the toolchain trees spend none of the walk's entry bound. The root's six entries and
// src/main.zig fit a bound of exactly that many however large the fetched packages grow; one more
// first-party entry stops the walk.
func TestVerificationZigToolchainTreesSpendNoEntries(t *testing.T) {
	files := builtZigRepository()
	for i := range 32 {
		files[fmt.Sprintf("zig-pkg/dep-0.0.1-h/src/f%d.zig", i)] = ""
	}
	root := writeRepo(t, files)
	limits := DefaultVerificationLimits()
	limits.MaxEntries = 6
	if _, err := ObserveVerificationPlanWithLimits(t.Context(), root, &limits); err != nil {
		t.Fatalf("toolchain trees spent the entry bound: %v", err)
	}
	mustWrite(t, filepath.Join(root, "src", "extra.zig"), "")
	if _, err := ObserveVerificationPlanWithLimits(t.Context(), root, &limits); err == nil || !strings.Contains(err.Error(), "--"+VerificationEntriesFlag) {
		t.Fatalf("one entry past the bound: %v; want the entries flag named", err)
	}
}
