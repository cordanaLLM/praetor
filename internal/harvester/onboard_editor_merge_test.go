package harvester

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/editor"
)

// Issue #717: onboarding writes editor files through editor.WriteWithReportIn, the rule
// `editors generate` applies, instead of replacing every existing file that is not
// developer-owned. These cases pin what follows for .vscode/settings.json.

// vscodeOnboardFixture is a repository whose manifest selects VS Code alone and no agent client.
func vscodeOnboardFixture(t *testing.T) string {
	t.Helper()
	return selectionOnboardFixture(t, "editors: [vscode]\nagent_clients: []\n")
}

// writeVSCodeSettings writes content as the repository's .vscode/settings.json.
func writeVSCodeSettings(t *testing.T, repo, content string) string {
	t.Helper()
	mustMkdirAll(t, filepath.Join(repo, ".vscode"))
	path := filepath.Join(repo, ".vscode", "settings.json")
	mustWriteFile(t, path, content)
	return path
}

func readOnboardOutput(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// onboardOutcome is the outcome plan reports for the editor file rel, or "" when it lists none.
func onboardOutcome(plan *OnboardPlan, rel string) editor.WriteOutcome {
	if plan == nil {
		return ""
	}
	for _, file := range plan.EditorFiles {
		if file.Path == rel {
			return file.Outcome
		}
	}
	return ""
}

// Positive: an existing settings file keeps the repository's key, gains the managed values,
// and the plan reports it merged.
func TestOnboardRepository_Positive_MergesExistingVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	path := writeVSCodeSettings(t, repo, "{\n  \"repo.key\": \"kept\"\n}\n")
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("onboard: %v", err)
	}
	got := readOnboardOutput(t, path)
	for _, want := range []string{`"repo.key": "kept"`, `"standards.sentinel.headroomMB"`} {
		if !strings.Contains(got, want) {
			t.Errorf("merged settings lack %s:\n%s", want, got)
		}
	}
	if outcome := onboardOutcome(plan, ".vscode/settings.json"); outcome != editor.WriteMerged {
		t.Errorf("settings outcome = %q, want %q (plan files %+v)", outcome, editor.WriteMerged, plan.EditorFiles)
	}
	if outcome := onboardOutcome(plan, ".vscode/tasks.json"); outcome != editor.WriteCreated {
		t.Errorf("tasks outcome = %q, want %q", outcome, editor.WriteCreated)
	}
}

// Negative: a commented settings file that lacks managed values is refused with
// editor.CommentedJSONError naming it; its bytes stay and no other editor file is written.
func TestOnboardRepository_Negative_RefusesCommentedVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	const commented = "{\n  // repository note\n  \"repo.key\": true,\n}\n"
	path := writeVSCodeSettings(t, repo, commented)
	plan, err := OnboardRepository(context.Background(), repo, false)
	var refusal *editor.CommentedJSONError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), ".vscode/settings.json") {
		t.Fatalf("commented file not refused by name: %v", err)
	}
	if got := readOnboardOutput(t, path); got != commented {
		t.Fatalf("refused file rewritten:\n%s", got)
	}
	if onboardExists(repo, ".vscode/tasks.json") {
		t.Error("an editor file was written although the run refused one")
	}
	if plan == nil || len(plan.EditorFiles) != 0 || plan.LockStatus != "" {
		t.Errorf("refused run reported editor outcomes or a lock outcome: %+v", plan)
	}
}

// Boundary: with no settings file the template is written, as before #717.
func TestOnboardRepository_Boundary_CreatesAbsentVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("onboard: %v", err)
	}
	if outcome := onboardOutcome(plan, ".vscode/settings.json"); outcome != editor.WriteCreated {
		t.Fatalf("settings outcome = %q, want %q", outcome, editor.WriteCreated)
	}
	if got := readOnboardOutput(t, filepath.Join(repo, ".vscode", "settings.json")); !strings.Contains(got, `"standards.sentinel.headroomMB"`) {
		t.Fatalf("created settings lack the managed values:\n%s", got)
	}
}

// Boundary: a commented settings file that already holds every managed value keeps its bytes,
// comment included, and is reported present.
func TestOnboardRepository_Boundary_KeepsCompleteCommentedVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	if _, err := OnboardRepository(context.Background(), repo, false); err != nil {
		t.Fatalf("first onboard: %v", err)
	}
	path := filepath.Join(repo, ".vscode", "settings.json")
	complete := "// repository note\n" + readOnboardOutput(t, path)
	mustWriteFile(t, path, complete)
	plan, err := OnboardRepository(context.Background(), repo, false)
	if err != nil {
		t.Fatalf("second onboard: %v", err)
	}
	if got := readOnboardOutput(t, path); got != complete {
		t.Fatalf("a complete commented file was rewritten:\n%s", got)
	}
	if outcome := onboardOutcome(plan, ".vscode/settings.json"); outcome != editor.WritePresent {
		t.Fatalf("settings outcome = %q, want %q", outcome, editor.WritePresent)
	}
}

// Boundary: a dry run writes nothing, not even to a file a live run would refuse, and reports
// no editor outcome.
func TestOnboardRepository_Boundary_DryRunLeavesVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	const commented = "{\n  // repository note\n  \"repo.key\": true\n}\n"
	path := writeVSCodeSettings(t, repo, commented)
	plan, err := OnboardRepository(context.Background(), repo, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := readOnboardOutput(t, path); got != commented {
		t.Fatalf("dry run changed the settings file:\n%s", got)
	}
	if onboardExists(repo, ".vscode/tasks.json") || onboardExists(repo, "AGENTS.md") {
		t.Error("dry run wrote a file")
	}
	if len(plan.EditorFiles) != 0 {
		t.Errorf("dry run reported editor outcomes: %+v", plan.EditorFiles)
	}
}
