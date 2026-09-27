package needs

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
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
// wrapped library, tooling and retained foundation, each naming the contract's package.
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
	if text := FormatLibraryRelationships(report); !strings.Contains(text, acmeKit+"/db") {
		t.Fatalf("a declared contract did not report its packages:\n%s", text)
	}
}

// A checkout without a capabilities.yaml of its own is observed against the configured
// contract: a package the checkout provides is available, a missing one is not, and a fork
// is observed at the contract's paths under its own module.
func TestCheckoutObservedAgainstConfiguredContract_3D(t *testing.T) {
	contract := writeAcmeContract(t)
	// Positive: the upstream checkout provides db; config is absent and unavailable.
	upstream := setupFrameworkCheckout(t, acmeKit, "db")
	writeFixture(t, upstream, "db/db.go", "package db\n\ntype Pool struct{}\n")
	index, err := InspectFramework(t.Context(), FrameworkSource{Checkout: upstream, Contract: contract, Module: acmeKit})
	if err != nil {
		t.Fatal(err)
	}
	if index.Basis != FrameworkSourceObserved || index.Contract != "kit.capabilities.yaml" || len(index.Packages) != 1 ||
		!slices.Equal(index.Replacements["github.com/jackc/pgx"], []string{acmeKit + "/db"}) {
		t.Fatalf("upstream checkout = %+v", index)
	}
	// Positive: a fork is observed under its own module at the contract's paths.
	fork := setupFrameworkCheckout(t, "example.com/fork/kit", "db")
	writeFixture(t, fork, "db/db.go", "package db\n\ntype Pool struct{}\n")
	forked, err := InspectFramework(t.Context(), FrameworkSource{Checkout: fork, Contract: contract, Module: acmeKit})
	if err != nil {
		t.Fatal(err)
	}
	if forked.Name != "example.com/fork/kit" || !slices.Equal(forked.Replacements["github.com/jackc/pgx"], []string{"example.com/fork/kit/db"}) {
		t.Fatalf("fork checkout = %+v", forked)
	}
	// Negative: a checkout with neither its own contract nor a configured one observes no
	// package, whatever its directories are called.
	bare, err := InspectFramework(t.Context(), FrameworkSource{Checkout: upstream, Module: acmeKit})
	if err != nil || len(bare.Packages) != 0 || bare.Contract != "" {
		t.Fatalf("checkout without an inventory = %+v, %v", bare, err)
	}
	if _, err := InspectFramework(t.Context(), FrameworkSource{Checkout: upstream, Contract: filepath.Join(t.TempDir(), "missing.yaml")}); err == nil {
		t.Fatal("a missing configured contract must fail the inspection")
	}
	// Boundary: the checkout's own capabilities.yaml wins over the configured contract.
	writeFixture(t, upstream, FrameworkContractFile, "version: 1\nframework: "+acmeKit+"\npackages:\n"+
		"  - import: "+acmeKit+"/db\n    capabilities: [db.orm]\n    replaces: [gorm.io/gorm]\n")
	own, err := InspectFramework(t.Context(), FrameworkSource{Checkout: upstream, Contract: contract, Module: acmeKit})
	if err != nil || own.Contract != FrameworkContractFile || len(own.Replacements["github.com/jackc/pgx"]) != 0 ||
		!slices.Equal(own.Replacements["gorm.io/gorm"], []string{acmeKit + "/db"}) {
		t.Fatalf("checkout contract = %+v, %v", own, err)
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

// A demand a scan mapped to the configured contract's package, re-mapped by a report to a
// fork's package at the same status, gets a note naming the fork's package; a note is kept
// when neither the package nor the status changes.
func TestContractNoteFollowsReplacement_3D(t *testing.T) {
	contract := writeAcmeContract(t)
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.com/consumer\n\ngo 1.27\n\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	registry, err := LoadRegistry(t.Context(), Targets{"go": {Module: acmeKit, Contract: contract}})
	if err != nil {
		t.Fatal(err)
	}
	fork := setupFrameworkCheckout(t, "example.com/fork/kit", "db")
	writeFixture(t, fork, "db/db.go", "package db\n\ntype Pool struct{}\n")
	forked, err := InspectFramework(t.Context(), FrameworkSource{Checkout: fork, Contract: contract, Module: acmeKit})
	if err != nil {
		t.Fatal(err)
	}
	// Positive: the fork report's note names the fork's package, not the contract's.
	report, err := ScanRepoWithFramework(t.Context(), repo, forked, registry)
	if err != nil {
		t.Fatal(err)
	}
	pgx := demandFor(t, report, "github.com/jackc/pgx/v5")
	if pgx.Status != StatusCovered || pgx.FrameworkReplacement != "example.com/fork/kit/db" ||
		!strings.Contains(pgx.Notes, "example.com/fork/kit/db") || strings.Contains(pgx.Notes, acmeKit+"/db") {
		t.Fatalf("fork report demand = %+v", pgx)
	}
	// Negative: the same package at the same status keeps the note it has.
	kept := DependencyDemand{Package: "github.com/jackc/pgx/v5", Ecosystem: "go", Capability: "db.postgres",
		Status: StatusCovered, FrameworkReplacement: "example.com/fork/kit/db", Notes: "operator note"}
	if !reconcileContractDemand(forked, &kept) || kept.Notes != "operator note" {
		t.Fatalf("unchanged demand = %+v", kept)
	}
	// Boundary: a gap the contract now covers gets the contract's note.
	gap := DependencyDemand{Package: "github.com/jackc/pgx/v5", Ecosystem: "go", Capability: "db.postgres", Status: StatusGap}
	if !reconcileContractDemand(forked, &gap) || gap.Notes != "kit.capabilities.yaml declares example.com/fork/kit/db for db.postgres" {
		t.Fatalf("covered gap = %+v", gap)
	}
}
