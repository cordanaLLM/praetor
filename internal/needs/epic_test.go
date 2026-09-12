package needs

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
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

	if epic.RepoName != "test/epic-target" {
		t.Errorf("expected repo name test/epic-target, got %s", epic.RepoName)
	}
	if len(epic.ChildIssues) != 5 {
		t.Fatalf("expected 5 child tasks, got %d", len(epic.ChildIssues))
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
	if len(epic.ChildIssues[4].DependsOn) == 0 {
		t.Error("expected Task 5 to depend on Task 4")
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
		RepoName:          "sample-repo",
		TargetFramework:   "github.com/golusoris/golusoris",
		ReadinessScore:    75.0,
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
	if !strings.Contains(string(data), "[TASK 1/5]") {
		t.Errorf("missing [TASK 1/5] in markdown output: %s", string(data))
	}
}

// newFakeIssueForge serves the GitHub issue-creation endpoint locally so the test stays
// hermetic and exercises the driver's real HTTP path.
func newFakeIssueForge(t *testing.T) *forge.GitHubDriver {
	t.Helper()
	created := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		created++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		payload := map[string]any{"number": created, "url": "https://forge.invalid/issues", "state": "open"}
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("failed encoding fake response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	gh := forge.NewGitHubDriver("forge-token", srv.URL)
	gh.SetRepository("test", "repo")
	return gh
}

func TestPublishPreMigrationEpic_Positive(t *testing.T) {
	ctx := context.Background()
	gh := newFakeIssueForge(t)

	epic := &PreMigrationEpic{
		RepoName: "test/repo",
		ParentEpic: forge.IssueSpec{
			Title: "[EPIC] Pre-Migration",
			Body:  "Checklist",
		},
		ChildIssues: []forge.IssueSpec{
			{Title: "[TASK 1/5] Invariants", Body: "Task body"},
			{Title: "[TASK 2/5] Decoupling", Body: "Task body"},
		},
	}

	parentRes, childResults, err := PublishPreMigrationEpic(ctx, gh, epic)
	if err != nil {
		t.Fatalf("failed to publish epic: %v", err)
	}
	if parentRes.Number != 1 {
		t.Errorf("expected parent issue number 1, got %d", parentRes.Number)
	}
	if len(childResults) != 2 {
		t.Fatalf("expected 2 child results, got %d", len(childResults))
	}
}

func TestPublishPreMigrationEpic_Negative(t *testing.T) {
	ctx := context.Background()
	if _, _, err := PublishPreMigrationEpic(ctx, nil, nil); err == nil {
		t.Fatal("expected error for nil forge and nil epic")
	}

	gh := newFakeIssueForge(t)
	if _, _, err := PublishPreMigrationEpic(ctx, gh, nil); err == nil {
		t.Fatal("expected error for nil epic")
	}
}

func TestRegenerateFleetEpics_3D(t *testing.T) {
	ctx := context.Background()
	tempDevDir := t.TempDir()

	// Positive: directory with 2 mock repos
	repo1 := filepath.Join(tempDevDir, "org1", "repo1")
	repo2 := filepath.Join(tempDevDir, "org2", "repo2")
	_ = os.MkdirAll(filepath.Join(repo1, ".git"), 0755)
	_ = os.MkdirAll(filepath.Join(repo1, ".workingdir"), 0755)
	_ = os.MkdirAll(filepath.Join(repo2, ".workingdir"), 0755)
	_ = os.WriteFile(filepath.Join(repo1, "go.mod"), []byte("module github.com/org1/repo1\ngo 1.27\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo2, "package.json"), []byte("{\"name\": \"repo2\", \"version\": \"1.0.0\"}\n"), 0644)
	_ = os.WriteFile(filepath.Join(repo2, ".standards.yaml"), []byte("repository:\n  name: repo2\n  owner: org2\n"), 0644)

	epics, err := RegenerateFleetEpics(ctx, tempDevDir, "github.com/golusoris/golusoris")
	if err != nil {
		t.Fatalf("regenerate fleet epics failed: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 regenerated epics, got %d", len(epics))
	}

	// Boundary: empty directory
	emptyDir := t.TempDir()
	emptyEpics, err := RegenerateFleetEpics(ctx, emptyDir, "github.com/golusoris/golusoris")
	if err != nil {
		t.Fatalf("regenerate on empty dir failed: %v", err)
	}
	if len(emptyEpics) != 0 {
		t.Fatalf("expected 0 epics for empty directory, got %d", len(emptyEpics))
	}

	// Negative: cancelled context
	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RegenerateFleetEpics(cancCtx, tempDevDir, "github.com/golusoris/golusoris"); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
