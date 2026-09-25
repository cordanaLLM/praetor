package flavor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/state"
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
