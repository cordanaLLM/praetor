package needs

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// umbrellaContract describes a placeholder framework whose umbrella package groups three
// subsystems, one of them a nested module. No real framework.
const umbrellaContract = `version: 1
framework: example.com/kit
modules:
  - example.com/kit/store
packages:
  - import: example.com/kit/config
    capabilities: [config.loader]
  - import: example.com/kit/httpx
    capabilities: [http.router, http.middleware]
  - import: example.com/kit/store
    module: example.com/kit/store
    capabilities: [db.postgres]
umbrellas:
  - import: example.com/kit
    groupings:
      - name: Core
        packages: [example.com/kit/config]
      - name: HTTP
        packages: [example.com/kit/httpx]
      - name: Store
        packages: [example.com/kit/store]
`

// umbrellaIndex declares the framework a contract describes, without a checkout.
func umbrellaIndex(t *testing.T, contract string) *FrameworkIndex {
	t.Helper()
	path := writeFixture(t, t.TempDir(), "kit.capabilities.yaml", contract)
	index, err := InspectFramework(t.Context(), FrameworkSource{Contract: path})
	if err != nil {
		t.Fatal(err)
	}
	return index
}

// writeKitModule writes the framework the contract describes as local modules below dir:
// the umbrella package grouping config, httpx and the nested store module.
func writeKitModule(t *testing.T, dir string) {
	t.Helper()
	writeFixture(t, dir, "go.mod", "module example.com/kit\n\ngo 1.22\n\nrequire example.com/kit/store v0.0.0\n")
	writeFixture(t, dir, "kit.go", "package kit\n\nimport (\n\t\"example.com/kit/config\"\n\t\"example.com/kit/httpx\"\n"+
		"\t\"example.com/kit/store\"\n)\n\nvar Core = []any{config.Module}\n\nvar HTTP = []any{httpx.Module}\n\n"+
		"var Store = []any{store.Module}\n\nfunc Version() string { return \"v0\" }\n")
	writeFixture(t, dir, "config/config.go", "package config\n\nvar Module = \"config\"\n")
	writeFixture(t, dir, "httpx/httpx.go", "package httpx\n\nvar Module = \"httpx\"\n")
	writeFixture(t, dir, "store/go.mod", "module example.com/kit/store\n\ngo 1.22\n")
	writeFixture(t, dir, "store/store.go", "package store\n\nvar Module = \"store\"\n")
}

// writeUmbrellaApp writes a consumer module whose main.go is source. With kit set, go.mod
// resolves the framework from that local tree; without it, the framework is a module no
// offline cache holds.
func writeUmbrellaApp(t *testing.T, dir, kit, source string) {
	t.Helper()
	gomod := "module example.com/app\n\ngo 1.22\n\nrequire (\n\texample.com/kit v1.0.0\n\texample.com/kit/store v1.0.0 // indirect\n)\n"
	if kit != "" {
		gomod = "module example.com/app\n\ngo 1.22\n\nrequire (\n\texample.com/kit v0.0.0\n\texample.com/kit/store v0.0.0 // indirect\n)\n\n" +
			"replace (\n\texample.com/kit => " + filepath.ToSlash(kit) + "\n\texample.com/kit/store => " +
			filepath.ToSlash(filepath.Join(kit, "store")) + "\n)\n"
	}
	writeFixture(t, dir, "go.mod", gomod)
	writeFixture(t, dir, "main.go", source)
}

func writeUmbrellaAppWithoutIndirect(t *testing.T, dir, kit, source string) {
	t.Helper()
	gomod := "module example.com/app\n\ngo 1.22\n\nrequire example.com/kit v0.0.0\n\n" +
		"replace (\n\texample.com/kit => " + filepath.ToSlash(kit) + "\n\texample.com/kit/store => " +
		filepath.ToSlash(filepath.Join(kit, "store")) + "\n)\n"
	writeFixture(t, dir, "go.mod", gomod)
	writeFixture(t, dir, "main.go", source)
}

// offlineGo makes the go command hermetic for a measurement test: an operator's -mod=mod
// in GOFLAGS (which the measurement overrides) and an empty module cache.
func offlineGo(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("measurement needs the go command on PATH: " + err.Error())
	}
	t.Setenv("GOENV", "off")
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOMODCACHE", t.TempDir())
}

func reportUmbrellas(t *testing.T, repo string, index *FrameworkIndex) *RepoNeeds {
	t.Helper()
	report, err := ReportRepoWithFramework(t.Context(), repo, index, nil)
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func onlyFinding(t *testing.T, report *RepoNeeds) UmbrellaFinding {
	t.Helper()
	if len(report.UmbrellaImports) != 1 {
		t.Fatalf("want one umbrella finding, got %+v", report.UmbrellaImports)
	}
	return report.UmbrellaImports[0]
}

const twoGroupingsApp = "package main\n\nimport \"example.com/kit\"\n\nvar app = []any{kit.Core, kit.HTTP}\n\nfunc main() { _ = app }\n"

// An umbrella import wiring two of three groupings yields the two sub-package imports that
// replace it, each with its capabilities, and the switch measured from the offline module
// graph: the umbrella package and the nested store module leave the build.
func TestUmbrellaImportRecommendsWiredGroupings_3D(t *testing.T) {
	offlineGo(t)
	root := t.TempDir()
	kit, app := filepath.Join(root, "kit"), filepath.Join(root, "app")
	writeKitModule(t, kit)
	writeUmbrellaApp(t, app, kit, twoGroupingsApp)
	before, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	assertRecommendsFinding(t, finding)
	assertRecommendsMeasurement(t, finding.Measurement)
	assertFileUnchanged(t, filepath.Join(app, "go.mod"), before)
	assertRecommendsRendering(t, finding)
}

func assertRecommendsFinding(t *testing.T, finding UmbrellaFinding) {
	t.Helper()
	if finding.Status != UmbrellaRecommend || finding.Project != "." {
		t.Fatalf("unexpected status/project: %+v", finding)
	}
	if !slices.Equal(finding.Files, []string{"main.go"}) || !slices.Equal(finding.Groupings, []string{"Core", "HTTP"}) {
		t.Fatalf("unexpected files/groupings: %+v", finding)
	}
	if finding.TotalGroupings != 3 || len(finding.Unmapped) != 0 {
		t.Fatalf("unexpected total/unmapped: %+v", finding)
	}
	want := []UmbrellaRecommendation{
		{Import: "example.com/kit/config", Groupings: []string{"Core"}, Capabilities: []CapabilityKey{"config.loader"}},
		{Import: "example.com/kit/httpx", Groupings: []string{"HTTP"}, Capabilities: []CapabilityKey{"http.router", "http.middleware"}},
	}
	if !slices.EqualFunc(finding.Recommendations, want, recommendationEqual) {
		t.Fatalf("recommendations = %+v, want %+v", finding.Recommendations, want)
	}
}

func assertRecommendsMeasurement(t *testing.T, measured *UmbrellaMeasurement) {
	t.Helper()
	if measured == nil || !measured.Measured {
		t.Fatalf("measurement = %+v, want measured", measured)
	}
	if measured.ModulesBefore != 2 || measured.ModulesAfter != 1 {
		t.Fatalf("modules = %d -> %d, want 2 -> 1", measured.ModulesBefore, measured.ModulesAfter)
	}
	if measured.PackagesBefore-measured.PackagesAfter != 2 {
		t.Fatalf("packages removed = %d, want 2", measured.PackagesBefore-measured.PackagesAfter)
	}
}

func assertFileUnchanged(t *testing.T, path string, before []byte) {
	t.Helper()
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("file %s changed (err %v):\n%s", path, err, after)
	}
}

func assertRecommendsRendering(t *testing.T, finding UmbrellaFinding) {
	t.Helper()
	text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}})
	wantLines := []string{
		"example.com/kit imported in main.go",
		"wires 2 of 3 groupings (Core, HTTP)",
		"-> example.com/kit/config (grouping Core): config.loader",
		"-> example.com/kit/httpx (grouping HTTP): http.router, http.middleware",
		"switch effect: modules 2 -> 1 (-1)",
	}
	for _, line := range wantLines {
		if !strings.Contains(text, line) {
			t.Fatalf("rendering lacks %q:\n%s", line, text)
		}
	}
}

func recommendationEqual(a, b UmbrellaRecommendation) bool {
	return a.Import == b.Import && slices.Equal(a.Groupings, b.Groupings) && slices.Equal(a.Capabilities, b.Capabilities)
}

// A repository importing only the sub-packages has no umbrella import to report.
func TestUmbrellaImportSubPackagesOnlyYieldNothing_3D(t *testing.T) {
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", "package main\n\nimport (\n\t\"example.com/kit/config\"\n\t\"example.com/kit/httpx\"\n)\n\n"+
		"var app = []any{config.Module, httpx.Module}\n\nfunc main() { _ = app }\n")
	report := reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract))
	if len(report.UmbrellaImports) != 0 || FormatUmbrellaImports(report) != "" {
		t.Fatalf("sub-package imports reported an umbrella: %+v", report.UmbrellaImports)
	}
	// Boundary: the framework's own module imports its umbrella natively and is not judged.
	writeFixture(t, app, "go.mod", "module example.com/kit/tools\n\ngo 1.22\n")
	writeFixture(t, app, "main.go", twoGroupingsApp)
	if own := reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)); len(own.UmbrellaImports) != 0 {
		t.Fatalf("a framework module's own umbrella import was judged: %+v", own.UmbrellaImports)
	}
}

// Wiring every grouping justifies the umbrella: no recommendation, nothing measured.
func TestUmbrellaImportEveryGroupingJustified_3D(t *testing.T) {
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", "package main\n\nimport k \"example.com/kit\"\n\nvar app = []any{k.Core, k.HTTP, k.Store}\n\nfunc main() { _ = app }\n")
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if finding.Status != UmbrellaJustified || len(finding.Recommendations) != 0 || finding.Measurement != nil ||
		len(finding.Groupings) != 3 {
		t.Fatalf("every grouping wired must justify the umbrella: %+v", finding)
	}
	if text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}}); !strings.Contains(text, "the umbrella import is justified") {
		t.Fatalf("rendering lacks the verdict:\n%s", text)
	}
}

// An umbrella the contract does not describe is reported as unknown, never mapped by guess;
// a root package the contract declares as an ordinary package is no umbrella at all.
func TestUmbrellaImportUndescribedIsUnknown_3D(t *testing.T) {
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", twoGroupingsApp)
	undescribed := strings.Split(umbrellaContract, "umbrellas:")[0]
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, undescribed)))
	if finding.Status != UmbrellaUnknown || finding.Umbrella != "example.com/kit" || len(finding.Groupings) != 0 ||
		len(finding.Recommendations) != 0 || finding.Measurement != nil {
		t.Fatalf("an undescribed umbrella was analysed: %+v", finding)
	}
	if text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}}); !strings.Contains(text, "unknown umbrella") {
		t.Fatalf("rendering lacks the unknown verdict:\n%s", text)
	}
	ordinary := undescribed + "  - import: example.com/kit\n    capabilities: [runtime.di]\n"
	if report := reportUmbrellas(t, app, umbrellaIndex(t, ordinary)); len(report.UmbrellaImports) != 0 {
		t.Fatalf("a declared ordinary root package was reported as an umbrella: %+v", report.UmbrellaImports)
	}
	// Boundary: no framework configured means no umbrella to judge.
	unconfigured, err := InspectFramework(t.Context(), FrameworkSource{})
	if err != nil {
		t.Fatal(err)
	}
	if report := reportUmbrellas(t, app, unconfigured); len(report.UmbrellaImports) != 0 {
		t.Fatalf("an unconfigured framework judged umbrellas: %+v", report.UmbrellaImports)
	}
}

// Without the module graph offline (a framework module no cache holds) the switch is
// reported as not measured, never estimated, and go.mod is left as it was even though
// GOFLAGS asks for -mod=mod.
func TestUmbrellaMeasurementUnavailableOffline_3D(t *testing.T) {
	offlineGo(t)
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", twoGroupingsApp)
	before, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	assertUnavailableFinding(t, finding)
	assertFileUnchanged(t, filepath.Join(app, "go.mod"), before)
	assertUnavailableRendering(t, finding)
}

func assertUnavailableFinding(t *testing.T, finding UmbrellaFinding) {
	t.Helper()
	measured := finding.Measurement
	if finding.Status != UmbrellaRecommend || measured == nil || measured.Measured {
		t.Fatalf("an unavailable module graph must leave the counts not measured: %+v", finding)
	}
	if measured.Reason == "" || measured.ModulesBefore != 0 || measured.PackagesBefore != 0 {
		t.Fatalf("unexpected measurement state: %+v", measured)
	}
	if len(finding.Recommendations) != 2 {
		t.Fatalf("want 2 recommendations, got %d", len(finding.Recommendations))
	}
}

func assertUnavailableRendering(t *testing.T, finding UmbrellaFinding) {
	t.Helper()
	text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}})
	if !strings.Contains(text, "switch effect: not measured (module graph unavailable offline") {
		t.Fatalf("rendering lacks the not-measured reason:\n%s", text)
	}
}

// An identifier no grouping describes keeps the umbrella import: recommendations still list
// the wired groupings, but the switch is not measured; a blank import alone references no
// grouping at all.
func TestUmbrellaImportUnmappedReferences_3D(t *testing.T) {
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", "package main\n\nimport \"example.com/kit\"\n\nvar app = []any{kit.Core}\n\nfunc main() { _ = kit.Version() }\n")
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if finding.Status != UmbrellaRecommend || !slices.Equal(finding.Unmapped, []string{"Version"}) ||
		finding.Measurement == nil || finding.Measurement.Measured || !strings.Contains(finding.Measurement.Reason, "stays for Version") {
		t.Fatalf("an unmapped reference must keep the umbrella and skip the measurement: %+v %+v", finding, finding.Measurement)
	}
	writeFixture(t, app, "main.go", "package main\n\nimport _ \"example.com/kit\"\n\nfunc main() {}\n")
	blank := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if blank.Status != UmbrellaUnmapped || !slices.Equal(blank.Unmapped, []string{"a blank import"}) || blank.Measurement != nil {
		t.Fatalf("a blank import must reference no grouping: %+v", blank)
	}
}

// The contract's name for an umbrella decides what an unrenamed import binds when the
// package clause differs from the path's last element.
func TestUmbrellaImportDeclaredPackageName(t *testing.T) {
	offlineGo(t)
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", "package main\n\nimport \"example.com/kit\"\n\nvar app = []any{kitfx.Core}\n\nfunc main() { _ = app }\n")
	named := strings.Replace(umbrellaContract, "  - import: example.com/kit\n    groupings:", "  - import: example.com/kit\n    name: kitfx\n    groupings:", 1)
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, named)))
	if !slices.Equal(finding.Groupings, []string{"Core"}) {
		t.Fatalf("the declared package name was not bound: %+v", finding)
	}
	// Negative: without the declared name the path's last element binds, and kitfx.Core
	// is no reference of the umbrella.
	plain := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if plain.Status != UmbrellaUnmapped || len(plain.Groupings) != 0 || len(plain.Unmapped) != 0 {
		t.Fatalf("an unbound selector counted as a reference: %+v", plain)
	}
	text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{plain}})
	if !strings.Contains(text, "references no grouping; nothing to recommend") {
		t.Fatalf("rendering of an unreferenced umbrella:\n%s", text)
	}
}

// The no-edit guarantee: when go.mod lacks indirect requires that -mod=mod would add,
// -mod=readonly refuses the update, go.mod remains byte-identical, and the failure is reported.
func TestUmbrellaMeasurementNoEditReadonlyRefusesUpdates_3D(t *testing.T) {
	offlineGo(t)
	kit := t.TempDir()
	writeKitModule(t, kit)
	app := t.TempDir()
	writeUmbrellaAppWithoutIndirect(t, app, kit, twoGroupingsApp)
	before, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	assertFileUnchanged(t, filepath.Join(app, "go.mod"), before)
	if finding.Measurement == nil || finding.Measurement.Measured {
		t.Fatalf("expected unmeasured due to readonly refusal: %+v", finding.Measurement)
	}
	if !strings.Contains(finding.Measurement.Reason, "updates to go.mod needed") {
		t.Fatalf("reason should contain 'updates to go.mod needed', got: %s", finding.Measurement.Reason)
	}
}

// The offline guarantee: when go.sum entries exist and GOPROXY points to a network host,
// GOPROXY=off prevents network access, no download happens, and GOPROXY=off is reported.
func TestUmbrellaMeasurementOfflineGoproxyOff_3D(t *testing.T) {
	offlineGo(t)
	t.Setenv("GOPROXY", "http://127.0.0.1:1")
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", twoGroupingsApp)
	gosum := "example.com/kit v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n" +
		"example.com/kit v1.0.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n" +
		"example.com/kit/store v1.0.0 h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n" +
		"example.com/kit/store v1.0.0/go.mod h1:47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=\n"
	writeFixture(t, app, "go.sum", gosum)
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if finding.Measurement == nil || finding.Measurement.Measured {
		t.Fatalf("expected unmeasured due to offline GOPROXY=off: %+v", finding.Measurement)
	}
	if !strings.Contains(finding.Measurement.Reason, "GOPROXY=off") {
		t.Fatalf("reason should contain 'GOPROXY=off', got: %s", finding.Measurement.Reason)
	}
}

// An explicit import of the runtime package exercises the runtime root retention clause.
func TestUmbrellaMeasurementRuntimeExplicitImport_3D(t *testing.T) {
	offlineGo(t)
	kit := t.TempDir()
	writeKitModule(t, kit)
	app := t.TempDir()
	src := "package main\n\nimport (\n\t\"runtime\"\n\t\"example.com/kit\"\n)\n\n" +
		"var app = []any{kit.Core, kit.HTTP}\n\nfunc main() { _ = app; _ = runtime.Compiler }\n"
	writeUmbrellaApp(t, app, kit, src)
	finding := onlyFinding(t, reportUmbrellas(t, app, umbrellaIndex(t, umbrellaContract)))
	if finding.Measurement == nil || !finding.Measurement.Measured {
		t.Fatalf("expected successful measurement with explicit runtime import: %+v", finding.Measurement)
	}
}

// umbrellaReplacements maps each project package to the replacements for the groupings it references.
func TestUmbrellaReplacementsPerPackage(t *testing.T) {
	index := umbrellaIndex(t, umbrellaContract)
	umbrella := index.Umbrellas["example.com/kit"]
	use := &umbrellaUse{
		refs: map[string]map[string]struct{}{
			"example.com/app/pkg1": {"Core": {}},
			"example.com/app/pkg2": {"HTTP": {}},
		},
	}
	replacements := umbrellaReplacements(&umbrella, use)
	if !slices.Equal(replacements["example.com/app/pkg1"], []string{"example.com/kit/config"}) {
		t.Fatalf("pkg1 replacements = %v, want config", replacements["example.com/app/pkg1"])
	}
	if !slices.Equal(replacements["example.com/app/pkg2"], []string{"example.com/kit/httpx"}) {
		t.Fatalf("pkg2 replacements = %v, want httpx", replacements["example.com/app/pkg2"])
	}
}

// A subproject whose scan failed is skipped during umbrella inspection.
func TestInspectUmbrellaImportsSkipsFailedSubproject(t *testing.T) {
	repoDir := t.TempDir()
	subGood := filepath.Join(repoDir, "sub-good")
	subBad := filepath.Join(repoDir, "sub-bad")
	writeUmbrellaApp(t, subGood, "", twoGroupingsApp)
	writeUmbrellaApp(t, subBad, "", twoGroupingsApp)
	repo := &fleetRepo{
		root:        repoDir,
		subprojects: []string{subGood, subBad},
	}
	report := &RepoNeeds{
		FailedSubprojects: []SubprojectFailure{{Dir: "sub-bad", Error: "broken"}},
	}
	findings, err := inspectUmbrellaImports(t.Context(), repo, report, umbrellaIndex(t, umbrellaContract))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 || findings[0].Project != "sub-good" {
		t.Fatalf("failed subproject was not skipped: %+v", findings)
	}
}

// A nested subproject reports its importing files relative to the repository root.
func TestUmbrellaFindingNestedSubprojectFilesPath(t *testing.T) {
	repoDir := t.TempDir()
	subDir := filepath.Join(repoDir, "nested", "svc")
	writeUmbrellaApp(t, subDir, "", twoGroupingsApp)
	repo := &fleetRepo{
		root:        repoDir,
		subprojects: []string{subDir},
	}
	findings, err := inspectUmbrellaImports(t.Context(), repo, nil, umbrellaIndex(t, umbrellaContract))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 finding, got %d", len(findings))
	}
	wantFile := filepath.ToSlash(filepath.Join("nested", "svc", "main.go"))
	if findings[0].Project != filepath.ToSlash(filepath.Join("nested", "svc")) || !slices.Equal(findings[0].Files, []string{wantFile}) {
		t.Fatalf("nested subproject files = %+v, want %q", findings[0].Files, wantFile)
	}
}

// ReportRepoWithFramework returns an error when the framework index is nil.
func TestReportRepoWithFrameworkNilFrameworkError(t *testing.T) {
	_, err := ReportRepoWithFramework(t.Context(), t.TempDir(), nil, nil)
	if err == nil || !strings.Contains(err.Error(), "framework index are required") {
		t.Fatalf("want nil framework error, got: %v", err)
	}
}

// classifyModuleGraphError maps go list errors to informative cause labels.
func TestUmbrellaMeasurementLabels(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{exec.ErrNotFound, "go binary not found:"},
		{errors.New("executable file not found in $PATH"), "go binary not found:"},
		{errors.New("cannot run toolchain local"), "toolchain refused by GOTOOLCHAIN=local:"},
		{errors.New("module lookup disabled by GOPROXY=off"), "module graph unavailable offline:"},
		{errors.New("missing go.sum entry"), "module graph unavailable offline:"},
		{errors.New("updates to go.mod needed"), "module graph unavailable offline:"},
		{errors.New("unexpected token in go.mod"), "go list failed:"},
	}
	for _, tc := range cases {
		got := classifyModuleGraphError(tc.err)
		if !strings.HasPrefix(got, tc.want) {
			t.Errorf("classifyModuleGraphError(%v) = %q, want prefix %q", tc.err, got, tc.want)
		}
	}
}

// moduleGraphCache caches listings per project directory within one report run.
func TestUmbrellaMeasurementCachesListingPerProject(t *testing.T) {
	cache := newModuleGraphCache()
	app := t.TempDir()
	writeUmbrellaApp(t, app, "", twoGroupingsApp)
	res1, err1 := cache.list(t.Context(), app)
	if _, ok := cache.listings[app]; !ok {
		t.Fatalf("expected listing to be cached for %s", app)
	}
	res2, err2 := cache.list(t.Context(), app)
	if (err1 != nil) != (err2 != nil) || string(res1) != string(res2) {
		t.Fatalf("cache returned inconsistent results: %v vs %v", err1, err2)
	}
}

// inspectUmbrellaImports skips projects whose first-scan imports contain no candidate umbrellas.
func TestInspectUmbrellaImportsGatedOnFirstScanImports(t *testing.T) {
	repoDir := t.TempDir()
	sub := filepath.Join(repoDir, "sub")
	writeUmbrellaApp(t, sub, "", twoGroupingsApp)
	repo := &fleetRepo{
		root:        repoDir,
		subprojects: []string{sub},
	}
	report := &RepoNeeds{
		ProjectImports: map[string][]string{
			"sub": {"github.com/gin-gonic/gin"},
		},
	}
	findings, err := inspectUmbrellaImports(t.Context(), repo, report, umbrellaIndex(t, umbrellaContract))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("project without candidate imports was inspected: %+v", findings)
	}
}

// An undescribed umbrella imported across multiple subprojects is aggregated once per framework.
func TestUmbrellaImportUndescribedReportedOncePerFramework(t *testing.T) {
	repoDir := t.TempDir()
	sub1 := filepath.Join(repoDir, "sub1")
	sub2 := filepath.Join(repoDir, "sub2")
	writeUmbrellaApp(t, sub1, "", twoGroupingsApp)
	writeUmbrellaApp(t, sub2, "", twoGroupingsApp)
	repo := &fleetRepo{
		root:        repoDir,
		subprojects: []string{sub1, sub2},
	}
	undescribed := strings.Split(umbrellaContract, "umbrellas:")[0]
	findings, err := inspectUmbrellaImports(t.Context(), repo, nil, umbrellaIndex(t, undescribed))
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 1 {
		t.Fatalf("want 1 aggregated finding for undescribed umbrella, got %d: %+v", len(findings), findings)
	}
	f := findings[0]
	if f.Status != UmbrellaUnknown || f.Umbrella != "example.com/kit" || len(f.Files) != 2 {
		t.Fatalf("unexpected aggregated finding: %+v", f)
	}
	rendered := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: findings})
	if !strings.Contains(rendered, "unknown umbrella") || !strings.Contains(rendered, "sub1/main.go") {
		t.Fatalf("rendered output lacks aggregated details:\n%s", rendered)
	}
}

// umbrellaCandidates requires a non-empty contract to yield any candidates.
func TestUmbrellaCandidatesRequiresContract(t *testing.T) {
	if got := umbrellaCandidates(nil); got != nil {
		t.Fatalf("nil framework gave candidates: %v", got)
	}
	frameworkWithoutContract := &FrameworkIndex{Name: "example.com/kit", Ecosystem: "go"}
	if got := umbrellaCandidates(frameworkWithoutContract); got != nil {
		t.Fatalf("framework without contract gave candidates: %v", got)
	}
}
