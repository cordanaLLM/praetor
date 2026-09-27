package needs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unconfiguredRepo writes a checkout that demands pgx, a library the catalog classifies, and
// a library it does not know.
func unconfiguredRepo(t *testing.T, dir string) string {
	t.Helper()
	writeFixture(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, dir, "go.mod", "module example.com/acme/app\n\ngo 1.27\n\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.7.2\n"+
		"\tgithub.com/acme/widget v0.1.0\n)\n")
	writeFixture(t, dir, "main.go", "package main\n\nimport _ \"github.com/jackc/pgx/v5\"\n\nfunc main() {}\n")
	return dir
}

// unconfiguredIndex is the framework a host with no framework.targets selects.
func unconfiguredIndex(t *testing.T) *FrameworkIndex {
	t.Helper()
	index, err := InspectFramework(t.Context(), SelectFrameworkSource(FrameworkSelection{}))
	if err != nil || index.Basis != FrameworkNotConfigured || index.Name != "" {
		t.Fatalf("unconfigured index = %+v, %v", index, err)
	}
	return index
}

// With no framework configured a report classifies every dependency, names no framework and
// claims no mapping percentage (ADR-0014 §4).
func TestUnconfiguredReportClassifiesOnly_3D(t *testing.T) {
	repo := unconfiguredRepo(t, t.TempDir())
	registry, err := RegistryFromPolicy(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: classification works, every demand is a gap, and the header says why.
	report, err := ScanRepoWithFramework(t.Context(), repo, unconfiguredIndex(t), registry)
	if err != nil {
		t.Fatal(err)
	}
	header := FormatReportHeader(report, unconfiguredIndex(t))
	want := "Framework: not configured (set framework.targets.<lang>.module and .contract, or pass --framework) | " +
		"Mapping availability: n/a (no target framework configured)\n"
	if !strings.Contains(header, want) || strings.Contains(header, "%") {
		t.Fatalf("unconfigured header:\n%s", header)
	}
	if pgx := demandFor(t, report, "github.com/jackc/pgx/v5"); pgx.Capability != "db.postgres" || pgx.Status != StatusGap || pgx.FrameworkReplacement != "" {
		t.Fatalf("pgx must be classified, never mapped: %+v", pgx)
	}
	if report.Framework != "" || report.Readiness.Basis != FrameworkNotConfigured || len(report.BuilderKits) != 0 {
		t.Fatalf("unconfigured report = %s %+v %v", report.Framework, report.Readiness, report.BuilderKits)
	}
	// Negative: a configured framework maps the same demand and reports a percentage.
	configured, err := ScanRepoWithFramework(t.Context(), repo, acmeIndex(t), acmeRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := FormatReportHeader(configured, acmeIndex(t)); !strings.Contains(got, "Framework: "+acmeKit+" (declared) | Mapping availability: 50.0%") {
		t.Fatalf("configured header:\n%s", got)
	}
	// Boundary: a dependency-free repository is still n/a, never the empty-denominator 100%.
	empty := t.TempDir()
	writeFixture(t, empty, "go.mod", "module example.com/acme/empty\n\ngo 1.27\n")
	bare, err := ScanRepoWithFramework(t.Context(), empty, unconfiguredIndex(t), registry)
	if err != nil {
		t.Fatal(err)
	}
	if got := MappingAvailability(bare.Readiness); got != "n/a (no target framework configured)" {
		t.Fatalf("dependency-free unconfigured availability = %q", got)
	}
}

func TestMappingAvailability_3D(t *testing.T) {
	// Positive: a scored row renders its percentage.
	if got := MappingAvailability(ReadinessMetrics{Basis: FrameworkCatalogDeclared, Score: 75}); got != "75.0%" {
		t.Errorf("declared = %q", got)
	}
	// Negative: a row scored against no framework is n/a whatever its score says.
	if got := MappingAvailability(ReadinessMetrics{Basis: FrameworkNotConfigured, Score: 100}); got != "n/a (no target framework configured)" {
		t.Errorf("not configured = %q", got)
	}
	// Boundary: a configured framework that maps nothing is a real 0%.
	if got := MappingAvailability(ReadinessMetrics{Basis: FrameworkSourceObserved}); got != "0.0%" {
		t.Errorf("observed zero = %q", got)
	}
}

// A scan with no framework configured writes a manifest without framework or builder kit
// lines, readiness basis not-configured; a configured scan writes both.
func TestUnconfiguredScanOmitsFramework_3D(t *testing.T) {
	repo := unconfiguredRepo(t, t.TempDir())
	// Positive: the unconfigured manifest names no framework.
	report, err := ScanRepo(t.Context(), repo, NewRegistry(nil))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteNeedsManifest(repo, report); err != nil {
		t.Fatal(err)
	}
	written := readManifestText(t, repo)
	for _, absent := range []string{"\nframework:", "builder_kits:", "framework_replacement:", legacyReplacementKey} {
		if strings.Contains(written, absent) {
			t.Errorf("unconfigured manifest carries %q:\n%s", absent, written)
		}
	}
	if !strings.Contains(written, "basis: not-configured") {
		t.Errorf("unconfigured manifest lacks its basis:\n%s", written)
	}
	// Negative: a configured scan names its framework and writes the replacement key.
	configured, err := ScanRepo(t.Context(), repo, acmeRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteNeedsManifest(repo, configured); err != nil {
		t.Fatal(err)
	}
	written = readManifestText(t, repo)
	for _, present := range []string{"\nframework: " + acmeKit + "\n", "builder_kits:", "framework_replacement: " + acmeKit + "/db/pgx"} {
		if !strings.Contains(written, present) {
			t.Errorf("configured manifest lacks %q:\n%s", present, written)
		}
	}
	// Boundary: a target with a module and no contract names the framework and maps nothing.
	moduleOnly, err := ScanRepo(t.Context(), repo, NewRegistry(Targets{"go": {Module: acmeKit}}))
	if err != nil || moduleOnly.Framework != acmeKit || moduleOnly.Readiness.CoveredDeps != 0 || moduleOnly.Readiness.Basis != FrameworkCatalogDeclared {
		t.Fatalf("module-only scan = %+v, %v", moduleOnly, err)
	}
}

func readManifestText(t *testing.T, repo string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repo, NeedsManifestName)) // #nosec G304 -- test-local path
	if err != nil {
		t.Fatal(err)
	}
	return "\n" + string(data)
}

// With no framework configured a migration has nothing to rewrite: the dry run says so and
// an application is refused before any command runs.
func TestUnconfiguredMigrationRefusesApply_3D(t *testing.T) {
	repo := unconfiguredRepo(t, t.TempDir())
	plan, err := PlanMigration(t.Context(), repo, FrameworkSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: the dry run names the reason and proposes nothing.
	if plan.Framework != "" || len(plan.DroppedRequires) != 0 || !strings.Contains(strings.Join(plan.Blockers, "\n"), "nothing to rewrite: no target framework configured") ||
		!strings.Contains(plan.GuideMarkdown, "-> not configured") {
		t.Fatalf("unconfigured plan = %+v", plan)
	}
	// Negative: --apply is refused with the configuration hint, before the evidence gate.
	res, err := ApplyMigration(t.Context(), repo, plan)
	if !errors.Is(err, ErrFrameworkNotConfigured) || res == nil || res.Success || !strings.Contains(res.Error, "nothing to rewrite") ||
		!strings.Contains(err.Error(), "framework.targets.<lang>.module") {
		t.Fatalf("unconfigured apply = %+v, %v", res, err)
	}
	// Boundary: a configured plan still reaches the evidence gate instead.
	configured, err := PlanMigration(t.Context(), repo, acmeDeclared(), acmeRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyMigration(t.Context(), repo, configured); !errors.Is(err, ErrUnverifiedMigration) || errors.Is(err, ErrFrameworkNotConfigured) {
		t.Fatalf("configured apply = %v", err)
	}
}

// With no framework configured an epic previews and names no framework, and publishing it is
// refused before the forge is asked for anything.
func TestUnconfiguredEpicPreviewAndPublish_3D(t *testing.T) {
	repo := unconfiguredRepo(t, t.TempDir())
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: the preview works and says the framework is not configured.
	for _, want := range []string{"- **Target Framework**: not configured\n", "- **Mapping Availability**: n/a (no target framework configured)\n",
		"nothing to rewrite: no target framework configured"} {
		if !strings.Contains(epic.ChecklistMarkdown, want) {
			t.Errorf("unconfigured epic lacks %q:\n%s", want, epic.ChecklistMarkdown)
		}
	}
	// Negative: publishing is refused and creates nothing.
	f := &fakeForge{}
	if _, _, err := PublishPreMigrationEpic(t.Context(), f, epic); !errors.Is(err, ErrFrameworkNotConfigured) || len(f.created) != 0 {
		t.Fatalf("unconfigured publish = %v, %d created", err, len(f.created))
	}
	// Boundary: a configured epic of the same repository renders its framework.
	configured, err := GeneratePreMigrationEpic(t.Context(), repo, acmeDeclared(), acmeRegistry(t))
	if err != nil || configured.TargetFramework != acmeKit || !strings.Contains(configured.ChecklistMarkdown, "- **Target Framework**: `"+acmeKit+"`") {
		t.Fatalf("configured epic = %+v, %v", configured, err)
	}
}

// With no framework configured a fleet report and its requests classify only: the header
// says the framework is not configured and every request is unrouted.
func TestUnconfiguredAggregateAndRequests_3D(t *testing.T) {
	root := t.TempDir()
	unconfiguredRepo(t, filepath.Join(root, "app"))
	report, err := AggregateFleet(t.Context(), root, FrameworkSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: the header names no framework and no coverage percentage.
	markdown := RenderFrameworkDemandMarkdown(report)
	for _, want := range []string{"**Target Framework**: " + FrameworkNotConfiguredText, "**Coverage Basis**: not-configured",
		"**Overall Fleet Target Framework Coverage**: n/a (no target framework configured)"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("unconfigured aggregate lacks %q:\n%s", want, markdown)
		}
	}
	// Positive: requests are synthesized for the classified gaps, all unrouted.
	requests := SynthesizeDemands(report, nil)
	if len(requests) != 2 || UnroutedSummary(requests) != fmt.Sprintf("%d of %d requests unrouted", len(requests), len(requests)) {
		t.Fatalf("unconfigured requests = %+v", requests)
	}
	for _, request := range requests {
		if request.TargetBuilderKit != "" || request.TargetOrg != "" || !strings.Contains(request.SpecificationMarkdown, "unrouted (framework.targets.") {
			t.Errorf("unconfigured request routed: %+v", request)
		}
	}
	// Negative: a configured fleet reports its framework and a percentage.
	configured, err := AggregateFleet(t.Context(), root, acmeDeclared(), acmeRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if got := RenderFrameworkDemandMarkdown(configured); !strings.Contains(got, "**Target Framework**: `"+acmeKit+"`") ||
		!strings.Contains(got, "**Overall Fleet Target Framework Coverage**: 50.0%") {
		t.Fatalf("configured aggregate:\n%s", got)
	}
	// Boundary: a fleet with nothing scanned says so even when not configured.
	blank, err := AggregateFleet(t.Context(), t.TempDir(), FrameworkSource{}, nil)
	if err != nil || !strings.Contains(RenderFrameworkDemandMarkdown(blank), "unknown (no repository could be scanned)") {
		t.Fatalf("empty unconfigured fleet = %+v, %v", blank, err)
	}
}

// A declared contract may name up to 64 nested modules; one more is refused.
func TestContractModuleBound(t *testing.T) {
	contract := func(count int) []byte {
		var sb strings.Builder
		sb.WriteString("version: 1\nframework: " + acmeKit + "\nmodules:\n")
		for i := range count {
			fmt.Fprintf(&sb, "  - %s/m%d\n", acmeKit, i)
		}
		sb.WriteString("packages:\n  - import: " + acmeKit + "/m0/db\n    module: " + acmeKit + "/m0\n    capabilities: [db.postgres]\n")
		return []byte(sb.String())
	}
	// Positive and boundary: exactly the bound parses and keeps every module.
	parsed, err := parseFrameworkContract(contract(maxContractModules), acmeKit)
	if err != nil || len(parsed.Modules) != 64 {
		t.Fatalf("64 modules = %v, %v", parsed, err)
	}
	// Negative: one past the bound fails validation.
	if _, err := parseFrameworkContract(contract(maxContractModules+1), acmeKit); err == nil {
		t.Fatal("65 modules accepted")
	}
	// Boundary: a contract with no package at all is valid and declares nothing.
	empty, err := parseFrameworkContract([]byte("version: 1\nframework: "+acmeKit+"\npackages: []\n"), acmeKit)
	if err != nil || len(empty.Packages) != 0 {
		t.Fatalf("empty contract = %+v, %v", empty, err)
	}
}
