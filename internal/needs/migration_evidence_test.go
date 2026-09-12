package needs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func migrationEvidenceFixture(t *testing.T, source string) (string, string) {
	t.Helper()
	repo := t.TempDir()
	writeFixture(t, repo, "go.mod", "module example.org/consumer\ngo 1.27\nrequire github.com/jackc/pgx/v5 v5.7.2\n")
	writeFixture(t, repo, "main.go", "package main\nimport \"github.com/jackc/pgx/v5\"\nfunc main() { _ = pgx.Connect }\n")
	framework := setupFrameworkCheckout(t, "example.org/fork", "db/pgx")
	writeFixture(t, framework, "db/pgx/doc.go", source)
	return repo, framework
}

func TestMigrationAndEpicUseSelectedFramework(t *testing.T) {
	for _, tc := range []struct {
		name, source string
		count        int
		score        float64
	}{
		{"header", "package pgx\n", 0, 0},
		{"observed", "package pgx\ntype Exists struct{}\n", 1, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo, framework := migrationEvidenceFixture(t, tc.source)
			plan, err := PlanMigration(t.Context(), repo, framework)
			if err != nil {
				t.Fatal(err)
			}
			epic, err := GeneratePreMigrationEpic(t.Context(), repo, framework)
			if err != nil {
				t.Fatal(err)
			}
			if plan.Framework != "example.org/fork" || len(plan.AddedRequires) != 0 || len(plan.Replacements) != tc.count {
				t.Fatalf("selected source must supply unresolved candidates: %+v", plan)
			}
			for _, replacement := range plan.Replacements {
				if replacement.NewImport != "example.org/fork/db/pgx" {
					t.Fatalf("wrong replacement: %+v", replacement)
				}
			}
			if epic.ReadinessScore != tc.score || epic.CoverageBasis != FrameworkSourceObserved || epic.TargetFramework != plan.Framework {
				t.Fatalf("epic disagrees with selected source: %+v", epic)
			}
			if strings.Contains(epic.ChecklistMarkdown, framework) || strings.Contains(epic.ChecklistMarkdown, "v0.8.0") {
				t.Fatal("epic leaks path or invents release")
			}
		})
	}
}

func TestMigrationSelectionErrorsDoNotBecomeEpics(t *testing.T) {
	repo, framework := migrationEvidenceFixture(t, "package pgx\n")
	missing := filepath.Join(framework, "missing")
	if _, err := PlanMigration(t.Context(), repo, missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selection: %v", err)
	}
	if _, err := GeneratePreMigrationEpic(t.Context(), repo, missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing epic selection: %v", err)
	}
}

func TestMigrationApplyRejectsUnverifiedBeforeCommands(t *testing.T) {
	repo, framework := migrationEvidenceFixture(t, "package pgx\ntype Exists struct{}\n")
	plan, err := PlanMigration(t.Context(), repo, framework)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	run := func(context.Context, string, string, ...string) (string, error) { calls++; return "true", nil }
	result, err := ApplyMigrationWithOptions(t.Context(), repo, plan, MigrationOptions{Runner: run, SkipTidy: true})
	if !errors.Is(err, ErrUnverifiedMigration) || calls != 0 || result == nil || result.Success {
		t.Fatalf("unverified apply: calls=%d result=%+v err=%v", calls, result, err)
	}
	after, err := os.ReadFile(filepath.Join(repo, "main.go"))
	if err != nil || string(after) != string(before) {
		t.Fatal("unverified migration changed source")
	}
}

func TestMigrationCandidateMetadataCannotAdmitApplication(t *testing.T) {
	repo, framework := migrationEvidenceFixture(t, "package pgx\ntype Exists struct{}\n")
	initGitFixture(t, repo)
	before := make(map[string]string)
	for _, name := range []string{"main.go", "go.mod", ".git/HEAD"} {
		data, err := os.ReadFile(filepath.Join(repo, name))
		if err != nil {
			t.Fatal(err)
		}
		before[name] = string(data)
	}
	plan, err := PlanMigration(t.Context(), repo, framework)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Status != "candidate" || plan.FrameworkVersion != "unverified" || len(plan.Blockers) < 3 {
		t.Fatalf("missing candidate metadata: %+v", plan)
	}
	// Public JSON metadata is not a trusted admission receipt, even when forged.
	plan.Status, plan.FrameworkVersion = "verified", "v9.9.9"
	plan.Blockers = nil
	plan.AddedRequires = []string{"example.org/fork v9.9.9"}
	result, err := ApplyMigration(t.Context(), repo, plan)
	var unverified *UnverifiedMigrationError
	if !errors.Is(err, ErrUnverifiedMigration) || !errors.As(err, &unverified) || result == nil || result.Success || result.Branch != "" || len(result.FilesChanged) != 0 {
		t.Fatalf("forged metadata authorized apply: %+v %v", result, err)
	}
	for name, expected := range before {
		data, readErr := os.ReadFile(filepath.Join(repo, name))
		if readErr != nil || string(data) != expected {
			t.Fatalf("candidate changed %s: %v", name, readErr)
		}
	}
	for _, name := range []string{"MIGRATION.md", ".git/refs/heads/" + migrationBranch} {
		if _, statErr := os.Stat(filepath.Join(repo, name)); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("unexpected migration artifact %s: %v", name, statErr)
		}
	}
}

func TestMigrationAnalysisRejectsInvalidSourceAndContext(t *testing.T) {
	repo, framework := migrationEvidenceFixture(t, "package pgx\nfunc broken(\n")
	for _, generate := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := PlanMigration(ctx, repo, framework); return err },
		func(ctx context.Context) error { _, err := GeneratePreMigrationEpic(ctx, repo, framework); return err },
	} {
		if err := generate(t.Context()); err == nil || !strings.Contains(err.Error(), "parse framework source") {
			t.Fatalf("invalid selected source: %v", err)
		}
		if err := generate(nil); err == nil {
			t.Fatal("nil context accepted")
		}
		cancelled, cancel := context.WithCancel(t.Context())
		cancel()
		if err := generate(cancelled); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation lost: %v", err)
		}
	}
	var absentContext context.Context // Deliberate invalid caller input, not a fallback context.
	if _, err := ApplyMigrationWithOptions(absentContext, repo, &MigrationPlan{}, MigrationOptions{}); err == nil {
		t.Fatal("nil apply context accepted")
	}
}

func TestMigrationSelectionKeepsDeclarationsUnverified(t *testing.T) {
	repo, _ := migrationEvidenceFixture(t, "package pgx\n")
	for _, tc := range []struct {
		selection, basis string
		candidates       int
	}{
		{"", FrameworkCatalogDeclared, 1},
		{"example.org/unknown", FrameworkIdentityDeclared, 0},
	} {
		plan, err := PlanMigration(t.Context(), repo, tc.selection)
		if err != nil {
			t.Fatal(err)
		}
		if plan.CoverageBasis != tc.basis || len(plan.Replacements) != tc.candidates || len(plan.AddedRequires) != 0 || plan.FrameworkVersion != "unverified" {
			t.Fatalf("declaration claimed verification: %+v", plan)
		}
	}
}
