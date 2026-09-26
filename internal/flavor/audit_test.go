package flavor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/templates"
)

// TestAuditFlavor_Positive_FreshCloneWithoutLedgerScoresFull pins the fix for the gate
// flavor stage rejecting every fresh clone: the session ledger is local, git-ignored state
// owned by `state audit`, so its absence says nothing about whether the repository conforms.
// conformingGoLibrary carries every tracked file and no .workingdir/, which is what a fresh
// clone or a linked worktree of a conforming repository looks like.
func TestAuditFlavor_Positive_FreshCloneWithoutLedgerScoresFull(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, nil)
	if _, err := os.Stat(filepath.Join(repo, state.WorkingDirName)); !os.IsNotExist(err) {
		t.Fatalf("fixture must not carry %s, stat err %v", state.WorkingDirName, err)
	}

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit a fresh clone: %v", err)
	}
	if report.Score != 100.0 || !report.Passed || len(report.MissingTemplates) != 0 {
		t.Fatalf("a conforming fresh clone must score 100 and pass, got score %v passed %v missing %+v",
			report.Score, report.Passed, report.MissingTemplates)
	}
}

// TestRequiredTemplates_Positive_NoFlavorRequiresTheLedger holds every registered flavor to
// the same rule, so a flavor added later cannot reintroduce the ledger as a template.
func TestRequiredTemplates_Positive_NoFlavorRequiresTheLedger(t *testing.T) {
	prefix := state.WorkingDirName + "/"
	for _, flv := range flavor.List() {
		for _, tmpl := range flv.RequiredTemplates() {
			paths := append([]string{tmpl.Path}, tmpl.AltPaths...)
			for _, p := range paths {
				if strings.HasPrefix(p, prefix) {
					t.Errorf("flavor %s requires git-ignored ledger %s as a template", flv.Name(), p)
				}
			}
		}
	}
}

// TestAuditFlavor_Negative_MissingTrackedTemplateStillFails keeps the verdict honest: dropping
// the ledger from the template list must not weaken the check on files the repository tracks.
func TestAuditFlavor_Negative_MissingTrackedTemplateStillFails(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, nil)
	if err := os.Remove(filepath.Join(repo, ".golangci.yml")); err != nil {
		t.Fatalf("remove a required template: %v", err)
	}

	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Passed {
		t.Fatalf("a repository missing .golangci.yml must fail, got %+v", report)
	}
	if len(report.MissingTemplates) != 1 || report.MissingTemplates[0].Path != ".golangci.yml" {
		t.Fatalf("expected exactly .golangci.yml missing, got %+v", report.MissingTemplates)
	}
}

// TestTemplateSatisfied_Negative_DirectoryIsNotATemplate covers a directory that happens to
// carry a template's name. It configures nothing, so it must not count as the file, under the
// canonical path or an accepted alternative.
func TestTemplateSatisfied_Negative_DirectoryIsNotATemplate(t *testing.T) {
	tmp := t.TempDir()
	item := flavor.TemplateItem{Path: "tsconfig.json", AltPaths: []string{"tsconfig.base.json"}}
	for _, name := range []string{"tsconfig.json", "tsconfig.base.json"} {
		if err := os.MkdirAll(filepath.Join(tmp, name), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	if flavor.TemplateSatisfied(tmp, item) {
		t.Fatal("a directory named like the template satisfied it")
	}

	if err := os.WriteFile(filepath.Join(tmp, "tsconfig.json", "inner.json"), []byte("{}"), 0o600); err != nil {
		t.Fatalf("write inner file: %v", err)
	}
	if flavor.TemplateSatisfied(tmp, item) {
		t.Fatal("a non-empty directory named like the template satisfied it")
	}
}

// TestAuditFlavor_Boundary_FlavorWithNoTemplatesScoresOnSettings covers the flavor whose only
// templates were the ledger: with none left, the verdict rests on its settings alone.
func TestAuditFlavor_Boundary_FlavorWithNoTemplatesScoresOnSettings(t *testing.T) {
	emptyPATH(t)
	repo := repoWithFiles(t, map[string]string{
		"Chart.yaml":                 "apiVersion: v2\nname: x\nversion: 0.1.0\n",
		".github/rulesets/main.json": "{\"name\": \"main\", \"enforcement\": \"active\"}\n",
	})

	report, err := flavor.AuditFlavor(repo, "infra-k8s")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.TemplatesTotal != 0 {
		t.Fatalf("infra-k8s requires no tracked template, got %d", report.TemplatesTotal)
	}
	if report.Score != 100.0 || !report.Passed {
		t.Fatalf("a conforming infra-k8s repository must pass, got %+v", report)
	}

	rewrite(t, repo, ".github/rulesets/main.json", "not json")
	broken, err := flavor.AuditFlavor(repo, "infra-k8s")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if broken.Score != 0.0 || broken.Passed {
		t.Fatalf("the only setting invalid must score 0 and fail, got %+v", broken)
	}
}

// settingsGapTolerance is, per registered flavor, how many required settings may be missing
// or invalid in a repository that carries every template and still passes the 80% bar.
//
// It is a contract, not arithmetic to recompute: the generated pre-push hook blocks on this
// verdict, so any change to a flavor's template or setting count that moves a number here
// changes which adopter pushes are blocked and needs its own changelog entry. The ledger
// removal did exactly that: with STATE.md, BUGS.md and QUESTIONS.md counted as templates,
// every flavor below at 0 except infra-k8s tolerated one gap on a checkout that carried
// them, and go-service tolerated two.
var settingsGapTolerance = map[string]int{
	"agentic-autonomous": 0,
	"frontend-svelte":    0,
	"go-library":         1,
	"go-service":         1,
	"infra-k8s":          0,
	"jvm-service":        0,
	"mobile-flutter":     0,
	"native-gpu-systems": 1,
	"os-image":           0,
	"python-ml":          0,
	"rust-systems":       1,
	"typescript-node":    1,
}

// fixtureSetting parses both as a non-empty JSON object and as a non-empty YAML mapping, so
// it satisfies every validator the catalog declares.
const fixtureSetting = "{\"praetor\": \"fixture\"}\n"

// fullyConformingRepo writes every template and setting the flavor requires, and the session
// ledger when withLedger is set, so a case varies only how many settings it takes away.
func fullyConformingRepo(t *testing.T, flv flavor.Flavor, withLedger bool) string {
	t.Helper()
	files := make(map[string]string)
	for _, tmpl := range flv.RequiredTemplates() {
		files[tmpl.Path] = conformingTemplateBody(t, tmpl)
	}
	for _, s := range flv.RequiredSettings() {
		files[s.Path] = fixtureSetting
	}
	if withLedger {
		for _, name := range []string{"STATE.md", "BUGS.md", "QUESTIONS.md"} {
			files[state.WorkingDirName+"/"+name] = "# ledger\n"
		}
	}
	return repoWithFiles(t, files)
}

// fixtureMarkdown satisfies the Markdown validator the producer-owned harness files carry.
const fixtureMarkdown = "# Harness\n\nRun make verify-all before concluding a turn.\n"

// conformingTemplateBody is a body that satisfies the template's validator: the embedded
// body flavor apply would scaffold, or, for a producer-owned file flavor apply never
// writes, a minimal document of the producer's format.
func conformingTemplateBody(t *testing.T, tmpl flavor.TemplateItem) string {
	t.Helper()
	if tmpl.Source != "" {
		body, err := templates.RenderFile(tmpl.Source, templates.Context{RepoName: "widget", Owner: "acme"})
		if err != nil {
			t.Fatalf("render %s for %s: %v", tmpl.Source, tmpl.Path, err)
		}
		return body
	}
	if strings.HasSuffix(tmpl.Path, ".md") {
		return fixtureMarkdown
	}
	return fixtureSetting
}

// settingsGapsTolerated removes the flavor's settings one at a time, in declaration order, and
// returns how many were gone when the audit last passed; -1 means a fully conforming
// repository already fails.
func settingsGapsTolerated(t *testing.T, flv flavor.Flavor, withLedger bool) int {
	t.Helper()
	repo := fullyConformingRepo(t, flv, withLedger)
	settings := flv.RequiredSettings()
	for gone := 0; gone <= len(settings); gone++ {
		if gone > 0 {
			if err := os.Remove(filepath.Join(repo, filepath.FromSlash(settings[gone-1].Path))); err != nil {
				t.Fatalf("remove setting %s: %v", settings[gone-1].Path, err)
			}
		}
		report, err := flavor.AuditFlavor(repo, flv.Name())
		if err != nil {
			t.Fatalf("audit %s: %v", flv.Name(), err)
		}
		if !report.Passed {
			return gone - 1
		}
	}
	return len(settings)
}

// TestAuditFlavor_Boundary_SettingsGapTolerancePerFlavor pins settingsGapTolerance for every
// registered flavor, with and without the session ledger present: the ledger is not scored,
// so it must buy no gap. A flavor added to the catalog without an entry fails here, which
// forces the decision to be made rather than inherited from its template count.
func TestAuditFlavor_Boundary_SettingsGapTolerancePerFlavor(t *testing.T) {
	emptyPATH(t)
	registered := flavor.List()
	if len(registered) != len(settingsGapTolerance) {
		t.Errorf("settingsGapTolerance names %d flavors, the catalog registers %d",
			len(settingsGapTolerance), len(registered))
	}
	for _, flv := range registered {
		want, ok := settingsGapTolerance[flv.Name()]
		if !ok {
			t.Errorf("flavor %s has no pinned settings-gap tolerance", flv.Name())
			continue
		}
		for _, withLedger := range []bool{false, true} {
			if got := settingsGapsTolerated(t, flv, withLedger); got != want {
				t.Errorf("flavor %s (ledger present: %v) tolerates %d missing settings, pinned %d",
					flv.Name(), withLedger, got, want)
			}
		}
	}
}

// TestAuditFlavor_Negative_LedgerBuysNoSettingsGap is the case the ledger removal changed,
// pinned by name: a python-ml checkout with the ledger present and .vscode/settings.json
// absent used to score 4 of 5 = 80% and pass. The ledger is no longer scored, so the same
// checkout scores 1 of 2 = 50% and fails, as it does in a fresh clone.
func TestAuditFlavor_Negative_LedgerBuysNoSettingsGap(t *testing.T) {
	emptyPATH(t)
	repo := repoWithFiles(t, map[string]string{
		"ruff.toml":                "line-length = 100\n",
		".workingdir/STATE.md":     "# state\n",
		".workingdir/BUGS.md":      "# bugs\n",
		".workingdir/QUESTIONS.md": "# questions\n",
	})

	report, err := flavor.AuditFlavor(repo, "python-ml")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if report.Score != 50.0 || report.Passed {
		t.Fatalf("python-ml missing its only setting must score 50 and fail, got score %v passed %v",
			report.Score, report.Passed)
	}
	if paths := report.InvalidSettingPaths(); len(paths) != 1 || paths[0] != ".vscode/settings.json" {
		t.Fatalf("the failure must name .vscode/settings.json, got %v", paths)
	}
}
