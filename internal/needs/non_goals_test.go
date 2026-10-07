package needs

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// postgresNonGoal is a .needs.yaml that declares db.postgres, the capability the catalog
// gives github.com/jackc/pgx (unconfiguredRepo), a non-goal.
const postgresNonGoal = `version: 1
non_goals:
  - capability: db.postgres
    rationale: Persistence belongs to the application
    alternative: github.com/jackc/pgx used directly
`

// nonGoalFramework writes a checkout of the placeholder go framework that provides no
// package, with manifest as its .needs.yaml when manifest is not empty.
func nonGoalFramework(t *testing.T, manifest string, domains ...string) string {
	t.Helper()
	framework := setupFrameworkCheckout(t, acmeKit, domains...)
	if manifest != "" {
		writeFixture(t, framework, NeedsManifestName, manifest)
	}
	return framework
}

// scoreAgainstFramework scans repo against the framework checkout.
func scoreAgainstFramework(t *testing.T, repo, framework string) *RepoNeeds {
	t.Helper()
	index, err := InspectFramework(t.Context(), acmeSource(framework))
	if err != nil {
		t.Fatalf("InspectFramework: %v", err)
	}
	report, err := ScanRepoWithFramework(t.Context(), repo, index, nil)
	if err != nil {
		t.Fatalf("ScanRepoWithFramework: %v", err)
	}
	return report
}

// A capability the framework declares a non-goal resolves the demands of it: readiness
// counts them as mapped, NonGoalDeps keeps the number visible, and the dependency names the
// rationale and the alternative.
func TestFrameworkNonGoalLiftsReadiness_3D(t *testing.T) {
	repo := unconfiguredRepo(t, t.TempDir())
	// Positive: the framework's non-goal resolves the pgx demand; the unknown widget stays a gap.
	lifted := scoreAgainstFramework(t, repo, nonGoalFramework(t, postgresNonGoal))
	pgx := relationshipDemand(t, lifted, "github.com/jackc/pgx/v5")
	if pgx.Status != StatusNonGoal || !strings.Contains(pgx.Notes, "Persistence belongs to the application.") ||
		!strings.Contains(pgx.Notes, "Alternative: github.com/jackc/pgx used directly") {
		t.Fatalf("pgx demand = %+v", pgx)
	}
	want := ReadinessMetrics{Basis: FrameworkSourceObserved, Score: 50, TotalThirdPartyDeps: 2, GapDeps: 1, NonGoalDeps: 1}
	if lifted.Readiness != want {
		t.Fatalf("readiness = %+v, want %+v", lifted.Readiness, want)
	}
	// Negative: the same framework without the declaration leaves both demands gaps.
	plain := scoreAgainstFramework(t, repo, nonGoalFramework(t, ""))
	if plain.Readiness.Score != 0 || plain.Readiness.GapDeps != 2 || plain.Readiness.NonGoalDeps != 0 {
		t.Fatalf("readiness without a non-goal = %+v", plain.Readiness)
	}
	// Boundary: a non-goal no demand uses changes nothing, and a demand resolved as a non-goal
	// is a gap again when scored against a framework that does not declare it.
	unused := strings.ReplaceAll(postgresNonGoal, "db.postgres", "clikit.tui")
	if got := scoreAgainstFramework(t, repo, nonGoalFramework(t, unused)).Readiness; got.Score != 0 || got.NonGoalDeps != 0 {
		t.Fatalf("readiness with an unused non-goal = %+v", got)
	}
	plainIndex, err := InspectFramework(t.Context(), acmeSource(nonGoalFramework(t, "")))
	if err != nil {
		t.Fatal(err)
	}
	applyFrameworkCoverage(plainIndex, lifted, nil)
	if pgx := relationshipDemand(t, lifted, "github.com/jackc/pgx/v5"); pgx.Status != StatusGap || lifted.Readiness.NonGoalDeps != 0 {
		t.Fatalf("re-scored pgx = %+v, readiness %+v", pgx, lifted.Readiness)
	}
}

// decodeNeeds decodes manifest the way every .needs.yaml reader does.
func decodeNeeds(manifest string) (*RepoNeeds, error) {
	var row RepoNeeds
	err := util.DecodeYAMLDocument([]byte(manifest), &row, util.YAMLDocumentOptions{AllowEmpty: true})
	return &row, err
}

// nonGoalEntries renders count non_goals entries with distinct capabilities.
func nonGoalEntries(count int) string {
	var sb strings.Builder
	sb.WriteString("version: 1\nnon_goals:\n")
	for i := range count {
		fmt.Fprintf(&sb, "  - capability: custom.goal_%d\n    rationale: out of scope\n    alternative: the application's own code\n", i)
	}
	return sb.String()
}

// The non_goals list is decoded strictly and validated: every entry names its capability, a
// rationale and an alternative, once, and never a capability the row also declares needed.
func TestNonGoalDeclarationValidation_3D(t *testing.T) {
	// Positive: a complete entry decodes with every field.
	row, err := decodeNeeds(postgresNonGoal)
	if err != nil || len(row.NonGoals) != 1 || row.NonGoals[0] != (NonGoal{Capability: "db.postgres",
		Rationale: "Persistence belongs to the application", Alternative: "github.com/jackc/pgx used directly"}) {
		t.Fatalf("decoded non-goals = %+v, %v", row.NonGoals, err)
	}
	// Negative: each defective entry is refused, naming the entry.
	second := postgresNonGoal + "  - capability: clikit.tui\n"
	for name, tc := range map[string]struct{ manifest, want string }{
		"empty rationale":   {second + "    rationale: \"  \"\n    alternative: bubbletea\n", "non_goals[1] (clikit.tui) has an empty rationale"},
		"empty alternative": {second + "    rationale: no terminal UI\n", "non_goals[1] (clikit.tui) has an empty alternative"},
		"no capability":     {"non_goals:\n  - rationale: why\n    alternative: what\n", "non_goals[0] names no capability"},
		"duplicate":         {strings.Replace(second, "clikit.tui", "db.postgres", 1) + "    rationale: r\n    alternative: a\n", "non_goals[1] (db.postgres) repeats the capability of non_goals[0]"},
		"misspelled key":    {"non_goals:\n  - capability: db.postgres\n    rationle: why\n    alternative: what\n", "field rationle not found"},
		"scalar entry":      {"non_goals:\n  - db.postgres\n", "non_goals entry at line 2 must be a mapping"},
	} {
		if _, err := decodeNeeds(tc.manifest); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want %q", name, err, tc.want)
		}
	}
	// Boundary: a capability both needed and a non-goal is refused under either list; the
	// list holds exactly maxNonGoals entries and refuses one more.
	for _, list := range []string{"required", "optional"} {
		needed := strings.Replace(postgresNonGoal, "non_goals:", "capabilities:\n  "+list+": [db.postgres]\nnon_goals:", 1)
		want := "non_goals[0] (db.postgres) is also declared needed under capabilities." + list
		if _, err := decodeNeeds(needed); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("needed under %s: err = %v, want %q", list, err, want)
		}
	}
	if row, err := decodeNeeds(nonGoalEntries(maxNonGoals)); err != nil || len(row.NonGoals) != maxNonGoals {
		t.Fatalf("%d non-goals: %d decoded, %v", maxNonGoals, len(row.NonGoals), err)
	}
	if _, err := decodeNeeds(nonGoalEntries(maxNonGoals + 1)); err == nil || !strings.Contains(err.Error(), "at most 128") {
		t.Fatalf("%d non-goals: err = %v", maxNonGoals+1, err)
	}
	// JSON rows are validated the same way.
	var fromJSON RepoNeeds
	if err := json.Unmarshal([]byte(`{"non_goals":[{"capability":"db.postgres","alternative":"pgx"}]}`), &fromJSON); err == nil ||
		!strings.Contains(err.Error(), "non_goals[0] (db.postgres) has an empty rationale") {
		t.Fatalf("JSON row with an empty rationale: %v", err)
	}
}

// A repository that still uses a capability it declares a non-goal fails `needs scan
// --check` (CheckNeedsManifest), naming the non-goal and the packages that use it.
func TestDeclaredNonGoalUsedByCode_3D(t *testing.T) {
	// Negative: pgx demands db.postgres, which the committed manifest declares a non-goal.
	repo := unconfiguredRepo(t, t.TempDir())
	writeFixture(t, repo, NeedsManifestName, postgresNonGoal)
	fresh, err := ScanRepo(t.Context(), repo, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = CheckNeedsManifest(t.Context(), repo, fresh)
	if !errors.Is(err, ErrNonGoalContradicted) || !strings.Contains(err.Error(), "non_goals[0] (db.postgres) is used by github.com/jackc/pgx/v5") {
		t.Fatalf("contradicted non-goal: %v", err)
	}
	// Positive: a non-goal the code does not use is carried into the written manifest, which
	// a fresh scan then matches.
	clean := unconfiguredRepo(t, t.TempDir())
	writeFixture(t, clean, NeedsManifestName, strings.ReplaceAll(postgresNonGoal, "db.postgres", "clikit.tui"))
	scanned, err := ScanRepo(t.Context(), clean, nil)
	if err != nil || len(scanned.NonGoals) != 1 {
		t.Fatalf("scan carried non-goals %+v, %v", scanned.NonGoals, err)
	}
	if err := WriteNeedsManifest(clean, scanned); err != nil {
		t.Fatal(err)
	}
	rescanned, err := ScanRepo(t.Context(), clean, nil)
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := CheckNeedsManifest(t.Context(), clean, rescanned); err != nil || drift != "" {
		t.Fatalf("manifest with an unused non-goal: drift %q, %v", drift, err)
	}
	// Boundary: a selected standard-library import uses its capability too, and a long list
	// of users is cut after maxNonGoalUsesReported packages with a count of the rest.
	stdlib := &RepoNeeds{NonGoals: []NonGoal{{Capability: "telemetry.logging", Rationale: "r", Alternative: "a"}},
		StandardLibraryImports: []DependencyDemand{{Package: "log/slog", Capability: "telemetry.logging"}}}
	if err := checkDeclaredNonGoals(stdlib); !errors.Is(err, ErrNonGoalContradicted) || !strings.Contains(err.Error(), "used by log/slog") {
		t.Fatalf("standard-library use: %v", err)
	}
	many := &RepoNeeds{NonGoals: []NonGoal{{Capability: "db.postgres", Rationale: "r", Alternative: "a"}}}
	for i := range maxNonGoalUsesReported + 2 {
		many.Dependencies = append(many.Dependencies, DependencyDemand{Package: fmt.Sprintf("example.com/pg%d", i), Capability: "db.postgres"})
	}
	if err := checkDeclaredNonGoals(many); err == nil || !strings.Contains(err.Error(), "example.com/pg7, 2 more;") ||
		strings.Contains(err.Error(), "example.com/pg8") {
		t.Fatalf("bounded user list: %v", err)
	}
}

// A framework checkout that provides a package for a capability its .needs.yaml declares a
// non-goal is refused, naming both; an invalid declaration fails the inspection.
func TestFrameworkNonGoalContradiction_3D(t *testing.T) {
	// Positive: a declared non-goal the checkout does not provide is loaded.
	index, err := InspectFramework(t.Context(), acmeSource(nonGoalFramework(t, postgresNonGoal)))
	if err != nil || len(index.NonGoals) != 1 || index.NonGoals[0].Capability != "db.postgres" {
		t.Fatalf("framework non-goals = %+v, %v", index, err)
	}
	// Negative: the checkout provides db.postgres in db/pgx.
	framework := nonGoalFramework(t, postgresNonGoal, "db/pgx")
	writeFixture(t, framework, filepath.Join("db", "pgx", "doc.go"), "package pgx\ntype Available struct{}\n")
	_, err = InspectFramework(t.Context(), acmeSource(framework))
	if !errors.Is(err, ErrNonGoalContradicted) || !strings.Contains(err.Error(), "non_goals[0] (db.postgres)") ||
		!strings.Contains(err.Error(), acmeKit+"/db/pgx") {
		t.Fatalf("provided non-goal: %v", err)
	}
	// Boundary: an invalid declaration fails the inspection rather than being ignored, and a
	// declared contract (no checkout) carries no non-goals.
	invalid := strings.Replace(postgresNonGoal, "Persistence belongs to the application", "\"\"", 1)
	if _, err := InspectFramework(t.Context(), acmeSource(nonGoalFramework(t, invalid))); err == nil ||
		!strings.Contains(err.Error(), "non_goals[0] (db.postgres) has an empty rationale") {
		t.Fatalf("invalid framework declaration: %v", err)
	}
	if declared := acmeIndex(t); len(declared.NonGoals) != 0 {
		t.Fatalf("declared contract non-goals = %+v", declared.NonGoals)
	}
}

// The fleet report counts the demands a framework non-goal resolves as covered, lists the
// non-goal with its rationale and alternative, and gives the leaderboard a Non-goals column.
func TestAggregateReportsFrameworkNonGoals_3D(t *testing.T) {
	root := t.TempDir()
	unconfiguredRepo(t, filepath.Join(root, "app"))
	// Positive: one of two demands is resolved.
	report, err := AggregateFleet(t.Context(), root, acmeSource(nonGoalFramework(t, postgresNonGoal)), nil)
	if err != nil {
		t.Fatal(err)
	}
	markdown := RenderFrameworkDemandMarkdown(report)
	for _, want := range []string{"## Framework Non-goals", "- `db.postgres` (1 consumer repositories): Persistence belongs to the application. Alternative: github.com/jackc/pgx used directly",
		"| Covered Deps | Gaps | Non-goals |", "| 50.0% | 0 | 1 | 1 |", "**Overall Fleet Target Framework Coverage**: 50.0%"} {
		if !strings.Contains(markdown, want) {
			t.Errorf("aggregate lacks %q:\n%s", want, markdown)
		}
	}
	if len(report.Gaps) != 1 || report.Gaps[0].Capability == "db.postgres" {
		t.Errorf("a non-goal is listed as a gap: %+v", report.Gaps)
	}
	// Negative: without the declaration nothing is resolved and no section is rendered.
	plain, err := AggregateFleet(t.Context(), root, acmeSource(nonGoalFramework(t, "")), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := RenderFrameworkDemandMarkdown(plain); strings.Contains(got, "Framework Non-goals") || !strings.Contains(got, "| 0.0% | 0 | 2 | 0 |") {
		t.Errorf("aggregate without non-goals:\n%s", got)
	}
	// Boundary: a declared non-goal no repository demands is listed with no consumer.
	unused, err := AggregateFleet(t.Context(), root, acmeSource(nonGoalFramework(t, strings.ReplaceAll(postgresNonGoal, "db.postgres", "clikit.tui"))), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := renderDemandNonGoals(unused); !strings.Contains(got, "`clikit.tui` (0 consumer repositories)") {
		t.Errorf("unused non-goal section:\n%s", got)
	}
}

// Dependency counts name the non-goals only when a row has any; the report header and the
// relationship list mark the resolved demands.
func TestNonGoalRendering_3D(t *testing.T) {
	// Positive: non-goals are counted between covered and gaps.
	if got := FormatDependencyCounts(ReadinessMetrics{CoveredDeps: 1, NonGoalDeps: 3, GapDeps: 2}); got != "1 covered, 3 non-goals, 2 gaps" {
		t.Errorf("counts with non-goals = %q", got)
	}
	// Negative: a row without non-goals keeps the former wording.
	if got := FormatDependencyCounts(ReadinessMetrics{CoveredDeps: 1, GapDeps: 2}); got != "1 covered, 2 gaps" {
		t.Errorf("counts without non-goals = %q", got)
	}
	// Boundary: an empty row.
	if got := FormatDependencyCounts(ReadinessMetrics{}); got != "0 covered, 0 gaps" {
		t.Errorf("empty counts = %q", got)
	}
	repo := unconfiguredRepo(t, t.TempDir())
	framework := nonGoalFramework(t, postgresNonGoal)
	lifted := scoreAgainstFramework(t, repo, framework)
	index, err := InspectFramework(t.Context(), acmeSource(framework))
	if err != nil {
		t.Fatal(err)
	}
	if header := FormatReportHeader(lifted, index); !strings.Contains(header, "Framework non-goals: 1 dependencies resolved as declared non-goals, counted as mapped") {
		t.Errorf("report header:\n%s", header)
	}
	if list := FormatLibraryRelationships(lifted); !strings.Contains(list, "○ github.com/jackc/pgx/v5") ||
		!strings.Contains(list, "framework non-goal (resolved); capability=db.postgres") {
		t.Errorf("relationship list:\n%s", list)
	}
	plain := scoreAgainstFramework(t, repo, nonGoalFramework(t, ""))
	if header := FormatReportHeader(plain, index); strings.Contains(header, "Framework non-goals") {
		t.Errorf("header without non-goals:\n%s", header)
	}
}
