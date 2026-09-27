package needs

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// acmeContract is a version-1 contract for the placeholder framework example.com/acme/kit:
// a db package replacing pgx, a config package wrapping koanf, a log package adapting
// logrus, tooling for ogen and fx as a retained foundation.
const acmeContract = `version: 1
framework: example.com/acme/kit
ecosystem: go
foundations: [go.uber.org/fx, log/slog]
packages:
  - import: example.com/acme/kit/db
    capabilities: [db.postgres]
    replaces: [github.com/jackc/pgx]
  - import: example.com/acme/kit/config
    capabilities: [config.loader]
    wraps: [github.com/knadh/koanf/v2]
  - import: example.com/acme/kit/log
    capabilities: [telemetry.logging]
    adapts: [github.com/sirupsen/logrus]
  - import: example.com/acme/kit/openapi
    capabilities: [http.openapi]
    tooling_for: [github.com/ogen-go/ogen]
`

func writeAcmeContract(t *testing.T) string {
	t.Helper()
	return writeFixture(t, t.TempDir(), "kit.capabilities.yaml", acmeContract)
}

func TestInspectFrameworkSources_3D(t *testing.T) {
	contract := writeAcmeContract(t)
	// Positive: a contract declares the framework without observing a checkout.
	declared, err := InspectFramework(t.Context(), FrameworkSource{Contract: contract, Module: "example.com/acme/kit"})
	if err != nil {
		t.Fatal(err)
	}
	if declared.Name != "example.com/acme/kit" || declared.Basis != FrameworkCatalogDeclared || declared.Version != declaredFrameworkVersion ||
		declared.Contract != "kit.capabilities.yaml" || declared.RootPath != "" || len(declared.Packages) != 4 {
		t.Fatalf("declared contract index = %+v", declared)
	}
	// Positive: a contract without a configured module names the framework itself.
	if named, err := InspectFramework(t.Context(), FrameworkSource{Contract: contract}); err != nil || named.Name != "example.com/acme/kit" {
		t.Fatalf("contract without module = %+v, %v", named, err)
	}
	// Positive: the built-in module alone keeps the built-in declared catalog.
	legacy, err := InspectFramework(t.Context(), FrameworkSource{Module: defaultFrameworkModule})
	if err != nil || legacy.Version != defaultFrameworkVersion || legacy.Basis != FrameworkCatalogDeclared || len(legacy.Packages) == 0 {
		t.Fatalf("built-in declared catalog = %+v, %v", legacy, err)
	}
	// Boundary: another module alone is named without a declared package; nothing selected
	// is not configured.
	identity, err := InspectFramework(t.Context(), FrameworkSource{Module: "example.com/acme/kit"})
	if err != nil || identity.Basis != FrameworkIdentityDeclared || len(identity.Packages) != 0 || identity.Name != "example.com/acme/kit" {
		t.Fatalf("module-only index = %+v, %v", identity, err)
	}
	none, err := InspectFramework(t.Context(), FrameworkSource{})
	if err != nil || none.Basis != FrameworkNotConfigured || none.Name != "" || len(none.Packages) != 0 {
		t.Fatalf("unselected index = %+v, %v", none, err)
	}
	// Negative: a contract naming another framework than the configured module, and a
	// missing contract, are errors.
	if _, err := InspectFramework(t.Context(), FrameworkSource{Contract: contract, Module: "example.com/other/kit"}); err == nil ||
		!strings.Contains(err.Error(), "does not match the selected module") {
		t.Fatalf("mismatched contract = %v", err)
	}
	if _, err := InspectFramework(t.Context(), FrameworkSource{Contract: filepath.Join(t.TempDir(), "missing.yaml")}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing contract = %v", err)
	}
}

// The declared contract reaches every claim kind a report shows: replacement, adapter,
// wrapped library, tooling and retained foundation, and never a built-in package.
func TestDeclaredContractReconcilesEveryClaim(t *testing.T) {
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.com/consumer\n\ngo 1.27\n\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.7.2\n"+
		"\tgithub.com/knadh/koanf/v2 v2.3.0\n\tgithub.com/sirupsen/logrus v1.9.0\n\tgithub.com/ogen-go/ogen v1.13.0\n\tgo.uber.org/fx v1.24.0\n"+
		"\tgithub.com/unknown/thing v1.0.0\n)\n")
	writeFixture(t, repo, "main.go", "package main\n\nfunc main() {}\n")
	source := FrameworkSource{Contract: writeAcmeContract(t), Module: "example.com/acme/kit"}
	index, err := InspectFramework(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	registry := NewRegistry(Targets{"go": {Module: source.Module, Contract: source.Contract}})
	report, err := ScanRepoWithFramework(t.Context(), repo, index, registry)
	if err != nil {
		t.Fatal(err)
	}
	assertDemand(t, demandFor(t, report, "github.com/jackc/pgx/v5"), StatusCovered, "example.com/acme/kit/db", "db.postgres")
	assertDemand(t, demandFor(t, report, "github.com/sirupsen/logrus"), StatusAdapterAvailable, "example.com/acme/kit/log", "telemetry.logging")
	for pkg, kind := range map[string]LibraryRelationshipKind{
		"github.com/knadh/koanf/v2": RelationshipWrappedBy, "github.com/ogen-go/ogen": RelationshipTooling, "go.uber.org/fx": RelationshipFoundation,
	} {
		dep := demandFor(t, report, pkg)
		if dep.Relationship == nil || dep.Relationship.Kind != kind || dep.FrameworkReplacement != "" || dep.Status == StatusGap {
			t.Errorf("%s = %+v %+v; want a %s relationship", pkg, dep, dep.Relationship, kind)
		}
	}
	if gap := demandFor(t, report, "github.com/unknown/thing"); gap.Status != StatusGap {
		t.Errorf("an undeclared dependency must stay a gap: %+v", gap)
	}
	if text := FormatLibraryRelationships(report); strings.Contains(text, defaultFrameworkModule) {
		t.Fatalf("a declared contract reported built-in packages:\n%s", text)
	}
}

// A fork resolves built-in catalog paths under its own module; a framework the catalog does
// not describe resolves none of them, and a related package comes from its own contract.
func TestCatalogPathsResolveAgainstTheCatalogModule(t *testing.T) {
	fork := &FrameworkIndex{Name: "example.com/fork/kit", CatalogModule: defaultFrameworkModule, Basis: FrameworkSourceObserved,
		Packages:     map[string]FrameworkPackage{"example.com/fork/kit/db/pgx": {}},
		Capabilities: map[CapabilityKey][]string{"db.postgres": {"example.com/fork/kit/db/pgx"}}}
	if got, ok := availableFrameworkPackage(fork, "db.postgres", defaultFrameworkModule+"/db/pgx"); !ok || got != "example.com/fork/kit/db/pgx" {
		t.Fatalf("fork package = %q, %v", got, ok)
	}
	other := *fork
	other.CatalogModule = "example.com/acme/kit"
	if got, ok := availableFrameworkPackage(&other, "db.postgres", defaultFrameworkModule+"/db/pgx"); ok || got != "" {
		t.Fatalf("a foreign catalog path resolved under another framework: %q", got)
	}
	candidates, err := frameworkCandidates("example.com/acme/kit")
	if err != nil || len(candidates) != 0 {
		t.Fatalf("candidates for a framework the catalog does not describe = %v, %v", candidates, err)
	}
	if candidates, err := frameworkCandidates(""); err != nil || len(candidates) != 0 {
		t.Fatalf("candidates without a catalog module = %v, %v", candidates, err)
	}
	if candidates, err := frameworkCandidates(defaultFrameworkModule); err != nil || len(candidates) == 0 {
		t.Fatalf("candidates for the built-in module = %v, %v", candidates, err)
	}
	related, ok := relatedFrameworkPackage(&other, "db.postgres", defaultFrameworkModule+"/db/pgx")
	if !ok || related != "example.com/fork/kit/db/pgx" {
		t.Fatalf("related package of another framework = %q, %v; want its own package for the capability", related, ok)
	}
	if related, ok := relatedFrameworkPackage(&other, "cache.redis", defaultFrameworkModule+"/cache/redis"); ok || related != "" {
		t.Fatalf("an undeclared capability found a related package: %q", related)
	}
}

func TestLoadRegistryDeclaresTargetContracts(t *testing.T) {
	dir := t.TempDir()
	npm := writeFixture(t, dir, "ui.yaml", "version: 1\nframework: example.com/acme/ui\necosystem: npm\npackages:\n"+
		"  - import: example.com/acme/ui/forms\n    capabilities: [ui.forms]\n    replaces: [zod]\n")
	// Positive: a typescript contract maps npm demands onto its packages in a scan.
	registry, err := LoadRegistry(t.Context(), Targets{"typescript": {Module: "example.com/acme/ui", BuilderKits: []string{"acme/ui"}, Contract: npm}})
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	writeFixture(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, repo, "package.json", `{"name":"ui","dependencies":{"zod":"^3","svelte":"^5"}}`)
	report, err := ScanRepo(t.Context(), repo, registry)
	if err != nil {
		t.Fatal(err)
	}
	assertDemand(t, demandFor(t, report, "zod"), StatusCovered, "example.com/acme/ui/forms", "ui.forms")
	if svelte := demandFor(t, report, "svelte"); svelte.Status != StatusGap || svelte.FrameworkReplacement != "" {
		t.Fatalf("a package the contract does not declare was covered: %+v", svelte)
	}
	// Negative: a contract of the wrong ecosystem and a missing contract refuse to load.
	goContract := writeFixture(t, dir, "go.yaml", "version: 1\nframework: example.com/acme/ui\npackages: []\n")
	if _, err := LoadRegistry(t.Context(), Targets{"typescript": {Contract: goContract}}); err == nil || !strings.Contains(err.Error(), "declares npm") {
		t.Fatalf("wrong ecosystem = %v", err)
	}
	if _, err := LoadRegistry(t.Context(), Targets{"rust": {Contract: filepath.Join(dir, "missing.yaml")}}); err == nil ||
		!strings.Contains(err.Error(), "framework.targets.rust.contract") {
		t.Fatalf("missing contract = %v", err)
	}
	// Boundary: no contract configured loads none and keeps scanning.
	if registry, err := LoadRegistry(t.Context(), nil); err != nil || registry.frameworkFor("typescript", nil) != nil {
		t.Fatalf("no targets = %v", err)
	}
}
