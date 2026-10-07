package needs

import (
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
	if finding.Status != UmbrellaRecommend || finding.Project != "." || !slices.Equal(finding.Files, []string{"main.go"}) ||
		!slices.Equal(finding.Groupings, []string{"Core", "HTTP"}) || finding.TotalGroupings != 3 || len(finding.Unmapped) != 0 {
		t.Fatalf("unexpected finding: %+v", finding)
	}
	want := []UmbrellaRecommendation{
		{Import: "example.com/kit/config", Groupings: []string{"Core"}, Capabilities: []CapabilityKey{"config.loader"}},
		{Import: "example.com/kit/httpx", Groupings: []string{"HTTP"}, Capabilities: []CapabilityKey{"http.router", "http.middleware"}},
	}
	if !slices.EqualFunc(finding.Recommendations, want, recommendationEqual) {
		t.Fatalf("recommendations = %+v, want %+v", finding.Recommendations, want)
	}
	measured := finding.Measurement
	if measured == nil || !measured.Measured || measured.ModulesBefore != 2 || measured.ModulesAfter != 1 ||
		measured.PackagesBefore-measured.PackagesAfter != 2 {
		t.Fatalf("measurement = %+v, want modules 2 -> 1 and two packages removed", measured)
	}
	after, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("measurement edited go.mod (err %v):\n%s", err, after)
	}
	text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}})
	for _, line := range []string{"example.com/kit imported in main.go", "wires 2 of 3 groupings (Core, HTTP)",
		"-> example.com/kit/config (grouping Core): config.loader", "-> example.com/kit/httpx (grouping HTTP): http.router, http.middleware",
		"switch effect: modules 2 -> 1 (-1)"} {
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
	measured := finding.Measurement
	if finding.Status != UmbrellaRecommend || measured == nil || measured.Measured || measured.Reason == "" ||
		measured.ModulesBefore != 0 || measured.PackagesBefore != 0 || len(finding.Recommendations) != 2 {
		t.Fatalf("an unavailable module graph must leave the counts not measured: %+v %+v", finding, measured)
	}
	t.Logf("not measured: %s", measured.Reason)
	after, err := os.ReadFile(filepath.Join(app, "go.mod"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("an offline measurement edited go.mod (err %v):\n%s", err, after)
	}
	if text := FormatUmbrellaImports(&RepoNeeds{UmbrellaImports: []UmbrellaFinding{finding}}); !strings.Contains(text, "switch effect: not measured (module graph unavailable offline") {
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
