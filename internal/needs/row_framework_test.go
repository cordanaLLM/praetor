package needs

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// acmePy is the placeholder python framework testdata/contracts/py.capabilities.yaml declares.
const acmePy = "example.com/acme/py"

// pythonOnlyRegistry configures the python target alone, as a host whose operator settings
// set only framework.targets.python.
func pythonOnlyRegistry(t *testing.T) *AnalyzerRegistry {
	t.Helper()
	registry, err := LoadRegistry(t.Context(), Targets{"python": acmeTargets()["python"]})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

// pythonOnlyRepo writes a python checkout demanding fastapi and requests, which the python
// contract replaces, and weird-lib, which nothing declares.
func pythonOnlyRepo(t *testing.T, dir string) string {
	t.Helper()
	writeFixture(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, dir, "requirements.txt", "fastapi==0.110\nrequests==2.31\nweird-lib==1.0\n")
	return dir
}

// mixedRepo writes a go module demanding a library nothing declares beside a python project
// demanding fastapi: go is the row's own language, python its second.
func mixedRepo(t *testing.T, dir string) string {
	t.Helper()
	writeFixture(t, dir, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixture(t, dir, "go.mod", "module example.com/acme/mixed\n\ngo 1.27\n\nrequire github.com/acme/widget v0.1.0\n")
	writeFixture(t, dir, "requirements.txt", "fastapi==0.110\n")
	return dir
}

func TestRowFramework_3D(t *testing.T) {
	unset := unconfiguredIndex(t)
	python := &RepoNeeds{Language: "python", Languages: []string{"python"}}
	// Positive: a python row on a python-only host names the python contract's framework.
	if got := RowFramework(pythonOnlyRegistry(t), python, unset); got.Name != acmePy || got.Basis != FrameworkCatalogDeclared {
		t.Fatalf("python row = %s (%s); want %s", got.Name, got.Basis, acmePy)
	}
	// Negative: with nothing configured the row keeps the unconfigured selection.
	if got := RowFramework(nil, python, unset); got != unset {
		t.Fatalf("unconfigured python row = %+v", got)
	}
	// Boundary: a row whose own language (go) is unconfigured takes the first configured
	// framework another of its languages is reconciled against; a configured go selection
	// keeps a go row on the selection; a nil row keeps the selection.
	mixed := &RepoNeeds{Language: "go", Languages: []string{"go", "python"}}
	if got := RowFramework(pythonOnlyRegistry(t), mixed, unset); got.Name != acmePy {
		t.Fatalf("mixed row = %s; want %s", got.Name, acmePy)
	}
	selected := acmeIndex(t)
	if got := RowFramework(acmeRegistry(t), mixed, selected); got != selected {
		t.Fatalf("configured go row = %s; want the selection %s", got.Name, acmeKit)
	}
	if got := RowFramework(pythonOnlyRegistry(t), nil, unset); got != unset {
		t.Fatalf("nil row = %+v", got)
	}
}

// A host that configures only a python target reports its python repositories against that
// target in the fleet header and leaderboard, as a scan does, never as not configured.
func TestPythonOnlyHostAggregate_3D(t *testing.T) {
	root := t.TempDir()
	pythonOnlyRepo(t, filepath.Join(root, "pyapp"))
	// Positive: the header names the python framework and the coverage percentage.
	report, err := AggregateFleet(t.Context(), root, FrameworkSource{}, pythonOnlyRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	markdown := RenderFrameworkDemandMarkdown(report)
	for _, want := range []string{"**Target Framework**: `" + acmePy + "`", "**Coverage Basis**: catalog-declared",
		"**Overall Fleet Target Framework Coverage**: 66.7%", "| 66.7% | 2 | 1 |"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("python-only aggregate lacks %q:\n%s", want, markdown)
		}
	}
	if strings.Contains(markdown, mappingNotConfigured) {
		t.Errorf("python-only aggregate rendered not configured:\n%s", markdown)
	}
	// Negative: the same fleet with nothing configured is not configured throughout.
	unset, err := AggregateFleet(t.Context(), root, FrameworkSource{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := RenderFrameworkDemandMarkdown(unset); !strings.Contains(got, "**Target Framework**: "+FrameworkNotConfiguredText) ||
		!strings.Contains(got, "| "+mappingNotConfigured+" | 0 | 3 |") || strings.Contains(got, "%") {
		t.Fatalf("unconfigured aggregate:\n%s", got)
	}
	// Boundary: beside a go repository no target configures and a mixed go and python
	// repository, the go row alone is n/a; the mixed row is scored against python.
	unconfiguredRepo(t, filepath.Join(root, "goapp"))
	mixedRepo(t, filepath.Join(root, "mixed"))
	mixed, err := AggregateFleet(t.Context(), root, FrameworkSource{}, pythonOnlyRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	board := renderDemandLeaderboard(mixed)
	for _, want := range []string{"`pyapp` | `pyapp` | 66.7% | 2 | 1 |", "`mixed` | 50.0% | 1 | 1 |", "`goapp` | " + mappingNotConfigured + " | 0 | 2 |"} {
		if !strings.Contains(board, want) {
			t.Errorf("mixed leaderboard lacks %q:\n%s", want, board)
		}
	}
	if mixed.Framework != acmePy || mixed.CoverageBasis != FrameworkCatalogDeclared || !strings.Contains(renderDemandHeader(mixed), "42.9%") {
		t.Fatalf("mixed header = %s (%s):\n%s", mixed.Framework, mixed.CoverageBasis, renderDemandHeader(mixed))
	}
}

// A go selection and another configured framework are both named in the fleet header, the
// selection first; a fleet that only reaches the selection names it alone.
func TestAggregateNamesEveryScoredFramework(t *testing.T) {
	root := t.TempDir()
	unconfiguredRepo(t, filepath.Join(root, "goapp"))
	goOnly, err := AggregateFleet(t.Context(), root, acmeDeclared(), acmeRegistry(t))
	if err != nil || goOnly.Framework != acmeKit {
		t.Fatalf("go fleet framework = %q, %v", goOnly.Framework, err)
	}
	pythonOnlyRepo(t, filepath.Join(root, "pyapp"))
	both, err := AggregateFleet(t.Context(), root, acmeDeclared(), acmeRegistry(t))
	if err != nil || both.Framework != acmeKit+", "+acmePy || both.CoverageBasis != FrameworkCatalogDeclared {
		t.Fatalf("go and python fleet framework = %q (%s), %v", both.Framework, both.CoverageBasis, err)
	}
}

// A host that configures only a python target plans migrations and epics of its python
// repositories against that target: no nothing-to-rewrite blocker, and --apply and
// --publish refuse only for want of evidence, not for want of a framework.
func TestPythonOnlyHostMigrationAndEpic_3D(t *testing.T) {
	repo := pythonOnlyRepo(t, t.TempDir())
	// Positive: the plan and the epic name the python framework and its percentage.
	plan, err := PlanMigration(t.Context(), repo, FrameworkSource{}, pythonOnlyRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if plan.Framework != acmePy || plan.CoverageBasis != FrameworkCatalogDeclared || strings.Contains(strings.Join(plan.Blockers, "\n"), nothingToRewrite) {
		t.Fatalf("python-only plan = %+v", plan)
	}
	if _, err := ApplyMigration(t.Context(), repo, plan); !errors.Is(err, ErrUnverifiedMigration) || errors.Is(err, ErrFrameworkNotConfigured) {
		t.Fatalf("python-only apply = %v", err)
	}
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, FrameworkSource{}, pythonOnlyRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"- **Target Framework**: `" + acmePy + "`", "- **Mapping Availability**: `66.7%`"} {
		if !strings.Contains(epic.ChecklistMarkdown, want) {
			t.Errorf("python-only epic lacks %q:\n%s", want, epic.ChecklistMarkdown)
		}
	}
	if _, _, err := PublishPreMigrationEpic(t.Context(), &fakeForge{}, epic); err != nil {
		t.Fatalf("python-only publish = %v", err)
	}
	// Negative: with nothing configured the same repository has nothing to rewrite.
	unset, err := PlanMigration(t.Context(), repo, FrameworkSource{}, nil)
	if err != nil || unset.Framework != "" || !strings.Contains(strings.Join(unset.Blockers, "\n"), nothingToRewrite) {
		t.Fatalf("unconfigured plan = %+v, %v", unset, err)
	}
	// Boundary: a go repository the python target does not configure is still refused, and
	// the fleet listing scores only the python repository against python.
	goRepo := unconfiguredRepo(t, t.TempDir())
	goEpic, err := GeneratePreMigrationEpic(t.Context(), goRepo, FrameworkSource{}, pythonOnlyRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := PublishPreMigrationEpic(t.Context(), &fakeForge{}, goEpic); !errors.Is(err, ErrFrameworkNotConfigured) {
		t.Fatalf("go epic on a python-only host = %v", err)
	}
	assertPythonOnlyFleetEpics(t)
}

// assertPythonOnlyFleetEpics checks the fleet epic listing of a python-only host: the python
// repository at its percentage, the go repository not configured.
func assertPythonOnlyFleetEpics(t *testing.T) {
	t.Helper()
	root := t.TempDir()
	pythonOnlyRepo(t, filepath.Join(root, "pyapp"))
	unconfiguredRepo(t, filepath.Join(root, "goapp"))
	epics, _, err := RegenerateFleetEpics(t.Context(), root, FleetEpicOptions{Registry: pythonOnlyRegistry(t), DryRun: true})
	if err != nil || len(epics) != 2 {
		t.Fatalf("fleet epics = %d, %v", len(epics), err)
	}
	for _, epic := range epics {
		got := MappingAvailability(ReadinessMetrics{Score: epic.ReadinessScore, Basis: epic.CoverageBasis})
		want := map[string]string{"pyapp": "66.7%", "example.com/acme/app": mappingNotConfigured}[epic.RepoName]
		if got != want {
			t.Errorf("fleet epic %s readiness = %q; want %q", epic.RepoName, got, want)
		}
	}
}
