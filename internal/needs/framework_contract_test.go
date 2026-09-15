package needs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const contractFrameworkModule = "example.com/fw"

const validContract = `version: 1
framework: example.com/fw
modules:
  - example.com/fw/core
packages:
  - import: example.com/fw/core/id
    module: example.com/fw/core
    domain: id
    capabilities: [id.uuid]
    replaces: [github.com/google/uuid]
  - import: example.com/fw/otel
    domain: otel
    capabilities: [telemetry.otel]
    replaces: [go.opentelemetry.io/otel]
  - import: example.com/fw/db/sqlite
    domain: db
    capabilities: [db.sqlite]
    replaces: [github.com/acme/kv/v2]
  - import: example.com/fw/httpx/client
    domain: httpx
    capabilities: [http.client]
    replaces: [go.opentelemetry.io/otel, github.com/acme/kv/v2]
`

// setupContractCheckout writes a framework checkout that publishes a capability contract:
// a root module with an otel adapter, a nested core module with an id package, and a db
// package whose contract replaces a versioned third-party module.
func setupContractCheckout(t *testing.T, contract string) string {
	t.Helper()
	root := setupFrameworkCheckout(t, contractFrameworkModule, "core/id", "otel", "db/sqlite", "httpx/client")
	writeFixture(t, root, "core/go.mod", "module "+contractFrameworkModule+"/core\n\ngo 1.27\n")
	writeFixture(t, root, "core/id/id.go", "package id\n\nfunc New() string { return \"id\" }\n")
	writeFixture(t, root, "otel/otel.go", "package otel\n\ntype Provider struct{}\n")
	writeFixture(t, root, "db/sqlite/sqlite.go", "package sqlite\n\nconst Driver = \"sqlite\"\n")
	writeFixture(t, root, "httpx/client/client.go", "package client\n\ntype Client struct{}\n")
	writeFixture(t, root, FrameworkContractFile, contract)
	return root
}

// setupContractConsumer writes a consumer that imports two catalog-known libraries, the
// framework's root and nested modules, a versioned module only the contract knows, and
// one library nothing covers.
func setupContractConsumer(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	requires := []string{
		"github.com/google/uuid v1.6.0",
		"go.opentelemetry.io/otel v1.30.0",
		contractFrameworkModule + " v0.10.0",
		contractFrameworkModule + "/core v0.9.0",
		"github.com/acme/kv/v3 v3.1.0",
		"github.com/unknown/gap-lib v1.0.0",
	}
	writeFixture(t, repo, "go.mod", "module example.com/consumer\n\ngo 1.27\n\nrequire (\n\t"+strings.Join(requires, "\n\t")+"\n)\n")
	imports := []string{
		"github.com/google/uuid",
		"go.opentelemetry.io/otel",
		contractFrameworkModule + "/grpc",
		contractFrameworkModule + "/core/config",
		"github.com/acme/kv/v3",
		"github.com/unknown/gap-lib",
	}
	writeFixture(t, repo, "main.go", "package main\n\nimport (\n\t_ \""+strings.Join(imports, "\"\n\t_ \"")+"\"\n)\n\nfunc main() {}\n")
	return repo
}

func demandFor(t *testing.T, report *RepoNeeds, pkg string) DependencyDemand {
	t.Helper()
	for _, dep := range report.Dependencies {
		if dep.Package == pkg {
			return dep
		}
	}
	t.Fatalf("dependency %s missing from %+v", pkg, report.Dependencies)
	return DependencyDemand{}
}

func assertDemand(t *testing.T, dep DependencyDemand, status CapabilityStatus, replacement string, capability CapabilityKey) {
	t.Helper()
	if dep.Status != status || dep.GolusorisReplacement != replacement {
		t.Errorf("%s: got status=%s replacement=%q, want %s %q", dep.Package, dep.Status, dep.GolusorisReplacement, status, replacement)
	}
	if capability != "" && dep.Capability != capability {
		t.Errorf("%s: got capability %s, want %s", dep.Package, dep.Capability, capability)
	}
}

func TestFrameworkContractObservesDeclaredPackagesAndReplacements(t *testing.T) {
	root := setupContractCheckout(t, validContract)
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if index.Contract != FrameworkContractFile || index.Basis != FrameworkSourceObserved || index.Name != contractFrameworkModule {
		t.Fatalf("contract index must stay source-observed under the checkout module: %+v", index)
	}
	id := index.Packages[contractFrameworkModule+"/core/id"]
	if id.Module != contractFrameworkModule+"/core" || !index.ProvidesCapability("id.uuid") {
		t.Fatalf("a package in a declared nested module must be observed: %+v", id)
	}
	if got := index.Replacements["github.com/acme/kv"]; len(got) != 2 || got[0] != contractFrameworkModule+"/db/sqlite" {
		t.Fatalf("replaces must be recorded without the major suffix, claimants in import order: %q", got)
	}
	report, err := ScanRepoWithFramework(t.Context(), setupContractConsumer(t), index)
	if err != nil {
		t.Fatal(err)
	}
	assertDemand(t, demandFor(t, report, "github.com/google/uuid"), StatusCovered, contractFrameworkModule+"/core/id", "id.uuid")
	// httpx/client sorts first among otel's claimants; the claimant declaring the
	// demanded capability must still win.
	assertDemand(t, demandFor(t, report, "go.opentelemetry.io/otel"), StatusCovered, contractFrameworkModule+"/otel", "telemetry.otel")
	assertDemand(t, demandFor(t, report, "github.com/acme/kv/v3"), StatusCovered, contractFrameworkModule+"/db/sqlite", "db.sqlite")
	assertDemand(t, demandFor(t, report, contractFrameworkModule), StatusNative, "", FrameworkNativeCapability)
	assertDemand(t, demandFor(t, report, contractFrameworkModule+"/core"), StatusNative, "", FrameworkNativeCapability)
	gap := demandFor(t, report, "github.com/unknown/gap-lib")
	assertDemand(t, gap, StatusGap, "", "")
	if !strings.HasPrefix(string(gap.Capability), "custom.") {
		t.Fatalf("an unmapped library keeps its custom capability: %+v", gap)
	}
	if report.Readiness.CoveredDeps != 5 || report.Readiness.GapDeps != 1 || report.Readiness.Basis != FrameworkSourceObserved {
		t.Fatalf("five of six demands map: %+v", report.Readiness)
	}
}

func TestFrameworkContractDeclarationAloneDoesNotEstablishAvailability(t *testing.T) {
	root := setupContractCheckout(t, validContract)
	writeFixture(t, root, "core/id/id.go", "// Package id is planned.\npackage id\n")
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, observed := index.Packages[contractFrameworkModule+"/core/id"]; observed || index.ProvidesCapability("id.uuid") {
		t.Fatal("a contract entry without library declarations is an unavailable stub")
	}
	if offered := index.Replacements["github.com/google/uuid"]; len(offered) != 0 {
		t.Fatal("replaces of an unobserved package must not be offered")
	}
	report, err := ScanRepoWithFramework(t.Context(), setupContractConsumer(t), index)
	if err != nil {
		t.Fatal(err)
	}
	assertDemand(t, demandFor(t, report, "github.com/google/uuid"), StatusGap, "", "id.uuid")
}

func TestFrameworkContractHonoursDeclaredModulesOnly(t *testing.T) {
	root := setupContractCheckout(t, validContract)
	if err := os.Remove(filepath.Join(root, "core", "go.mod")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, root, "db/go.mod", "module example.com/elsewhere\n\ngo 1.27\n")
	index, err := InspectFramework(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if index.ProvidesCapability("id.uuid") {
		t.Fatal("a declared nested module without its go.mod does not contain the package")
	}
	if index.ProvidesCapability("db.sqlite") {
		t.Fatal("a package under an undeclared nested module is outside its declared module")
	}
	if !index.ProvidesCapability("telemetry.otel") {
		t.Fatal("root-module packages remain observed")
	}
}

func TestFrameworkContractRejectsInvalidContracts(t *testing.T) {
	cases := map[string]string{
		"unsupported version":    strings.Replace(validContract, "version: 1", "version: 2", 1),
		"framework mismatch":     strings.Replace(validContract, "framework: example.com/fw", "framework: example.com/other", 1),
		"package outside":        strings.Replace(validContract, "import: example.com/fw/otel", "import: example.com/other/otel", 1),
		"undeclared module":      strings.Replace(validContract, "module: example.com/fw/core", "module: example.com/fw/lib", 1),
		"invalid capability key": strings.Replace(validContract, "[telemetry.otel]", "[Telemetry]", 1),
		"duplicate package":      strings.Replace(validContract, "import: example.com/fw/otel", "import: example.com/fw/core/id", 1),
		"invalid replaces path":  strings.Replace(validContract, "[go.opentelemetry.io/otel]", "[/abs/path]", 1),
		"malformed yaml":         "version: [1\n",
		"oversized":              validContract + "# " + strings.Repeat("x", maxContractBytes) + "\n",
	}
	for name, contract := range cases {
		t.Run(name, func(t *testing.T) {
			root := setupContractCheckout(t, contract)
			if _, err := InspectFramework(t.Context(), root); err == nil {
				t.Fatal("an invalid contract must fail inspection instead of silently degrading")
			}
		})
	}
}

func contractWithPackages(count int) string {
	var sb strings.Builder
	sb.WriteString("version: 1\nframework: example.com/fw\npackages:\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&sb, "  - import: example.com/fw/p%d\n    capabilities: [x.p%d]\n", i, i)
	}
	return sb.String()
}

func contractWithKeys(count int) string {
	names := make([]string, 0, count)
	for i := 0; i < count; i++ {
		names = append(names, fmt.Sprintf("x.k%d", i))
	}
	return "version: 1\nframework: example.com/fw\npackages:\n  - import: example.com/fw/otel\n    capabilities: [" + strings.Join(names, ", ") + "]\n"
}

func TestFrameworkContractBoundsAreInclusive(t *testing.T) {
	if _, err := InspectFramework(t.Context(), setupContractCheckout(t, contractWithPackages(maxFrameworkPackages))); err != nil {
		t.Fatalf("package bound is inclusive: %v", err)
	}
	if _, err := InspectFramework(t.Context(), setupContractCheckout(t, contractWithPackages(maxFrameworkPackages+1))); err == nil {
		t.Fatal("one package past the bound must be rejected")
	}
	if _, err := InspectFramework(t.Context(), setupContractCheckout(t, contractWithKeys(maxContractKeys))); err != nil {
		t.Fatalf("capability bound is inclusive: %v", err)
	}
	if _, err := InspectFramework(t.Context(), setupContractCheckout(t, contractWithKeys(maxContractKeys+1))); err == nil {
		t.Fatal("one capability past the bound must be rejected")
	}
}

func TestStripMajorSuffix(t *testing.T) {
	cases := map[string]string{
		"github.com/acme/kv/v2":     "github.com/acme/kv",
		"github.com/acme/kv/v12":    "github.com/acme/kv",
		"github.com/acme/kv":        "github.com/acme/kv",
		"github.com/acme/kv/v2/sub": "github.com/acme/kv/v2/sub",
		"gopkg.in/yaml.v3":          "gopkg.in/yaml.v3",
		"v2":                        "v2",
		"":                          "",
	}
	for input, want := range cases {
		if got := stripMajorSuffix(input); got != want {
			t.Errorf("stripMajorSuffix(%q) = %q, want %q", input, got, want)
		}
	}
}
