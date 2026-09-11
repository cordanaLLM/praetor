package needs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratePreMigrationEpic_Positive(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()

	goMod := `module github.com/test/epic-target
go 1.27
require (
	github.com/jackc/pgx/v5 v5.5.0
	github.com/unknown/lib v1.0.0
)
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}

	epic, err := GeneratePreMigrationEpic(ctx, tempDir, "github.com/golusoris/golusoris")
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}

	if epic.RepoName != "github.com/test/epic-target" {
		t.Errorf("expected repo name github.com/test/epic-target, got %s", epic.RepoName)
	}
	if len(epic.ChildIssues) != 4 {
		t.Fatalf("expected 4 child tasks, got %d", len(epic.ChildIssues))
	}

	// Verify dependency chain
	if len(epic.ChildIssues[1].DependsOn) == 0 {
		t.Error("expected Task 2 to depend on Task 1")
	}
	if len(epic.ChildIssues[2].DependsOn) == 0 {
		t.Error("expected Task 3 to depend on Task 2")
	}
	if len(epic.ChildIssues[3].DependsOn) == 0 {
		t.Error("expected Task 4 to depend on Task 3")
	}

	if !strings.Contains(epic.ChecklistMarkdown, "Pre-Migration Epic") {
		t.Errorf("checklist markdown missing title: %s", epic.ChecklistMarkdown)
	}
}

func TestGeneratePreMigrationEpic_Negative(t *testing.T) {
	ctx := context.Background()
	_, err := GeneratePreMigrationEpic(ctx, "/nonexistent/invalid/repo", "github.com/golusoris/golusoris")
	if err == nil {
		t.Fatal("expected error for nonexistent repository")
	}
}

func TestWriteEpicMarkdown_Boundary(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "epics", "TARGET_EPIC.md")

	epic := &PreMigrationEpic{
		RepoName:        "sample-repo",
		TargetFramework: "github.com/golusoris/golusoris",
		ReadinessScore:  75.0,
		ChecklistMarkdown: "# Sample Epic Checklist",
		ChildIssues: createChildTasks("sample-repo", &RepoNeeds{
			Readiness: ReadinessMetrics{Score: 75.0},
		}, &MigrationPlan{Framework: "github.com/golusoris/golusoris"}),
	}

	if err := WriteEpicMarkdown(epic, tempFile); err != nil {
		t.Fatalf("failed to write epic markdown: %v", err)
	}

	data, err := os.ReadFile(tempFile)
	if err != nil {
		t.Fatalf("failed to read generated epic markdown: %v", err)
	}
	if !strings.Contains(string(data), "[TASK 1/4]") {
		t.Errorf("missing [TASK 1/4] in markdown output: %s", string(data))
	}
}
