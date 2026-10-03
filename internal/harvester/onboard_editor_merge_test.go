package harvester

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/editor"
)

// Issue #717: onboarding resolves editor files through editor.PrepareWritesIn, the rule
// `editors generate` applies, before its first write, and publishes them bound to that read,
// instead of replacing every existing file that is not developer-owned. These cases pin what
// follows for .vscode/settings.json.

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
// editor.CommentedJSONError naming it before onboarding writes anything: its bytes stay, the
// manifest and lock are unchanged, and no scaffold, harness or editor file appears.
func TestOnboardRepository_Negative_RefusesCommentedVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	const commented = "{\n  // repository note\n  \"repo.key\": true,\n}\n"
	path := writeVSCodeSettings(t, repo, commented)
	manifest := readOnboardOutput(t, filepath.Join(repo, ".standards.yaml"))
	lock := readOnboardOutput(t, filepath.Join(repo, ".standards.lock"))
	plan, err := OnboardRepository(context.Background(), repo, false)
	var refusal *editor.CommentedJSONError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), ".vscode/settings.json") {
		t.Fatalf("commented file not refused by name: %v", err)
	}
	if plan != nil {
		t.Errorf("refused run returned a plan: %+v", plan)
	}
	if got := readOnboardOutput(t, path); got != commented {
		t.Fatalf("refused file rewritten:\n%s", got)
	}
	for _, rel := range []string{"AGENTS.md", ".standards-baseline.json", ".vscode/tasks.json"} {
		if onboardExists(repo, rel) {
			t.Errorf("%s was written although the run refused an editor file", rel)
		}
	}
	if readOnboardOutput(t, filepath.Join(repo, ".standards.yaml")) != manifest ||
		readOnboardOutput(t, filepath.Join(repo, ".standards.lock")) != lock {
		t.Error("a refused run changed the manifest or the lock")
	}
}

// Negative: in a repository holding nothing but the refused settings file, the refusal leaves
// exactly that file: no manifest, baseline, lock, AGENTS.md, vendor or editor file is written.
func TestOnboardRepository_Negative_RefusalWritesNothing(t *testing.T) {
	repo := t.TempDir()
	const commented = "{\n  // repository note\n  \"repo.key\": true\n}\n"
	path := writeVSCodeSettings(t, repo, commented)
	_, err := OnboardRepository(context.Background(), repo, false)
	var refusal *editor.CommentedJSONError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), ".vscode/settings.json") {
		t.Fatalf("commented file not refused by name: %v", err)
	}
	if got := readOnboardOutput(t, path); got != commented {
		t.Fatalf("refused file rewritten:\n%s", got)
	}
	if files := onboardTreeFiles(t, repo); len(files) != 1 || files[0] != ".vscode/settings.json" {
		t.Fatalf("a refused run wrote files: %v", files)
	}
}

// onboardTreeFiles lists every file below repo as a slash-separated relative path.
func onboardTreeFiles(t *testing.T, repo string) []string {
	t.Helper()
	files := []string{}
	err := filepath.WalkDir(repo, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		rel, err := filepath.Rel(repo, path)
		files = append(files, filepath.ToSlash(rel))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
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

// Negative: a dry run predicts the refusal a live run would meet, naming the file, and writes
// nothing.
func TestOnboardRepository_Negative_DryRunPredictsRefusal(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	const commented = "{\n  // repository note\n  \"repo.key\": true\n}\n"
	path := writeVSCodeSettings(t, repo, commented)
	_, err := OnboardRepository(context.Background(), repo, true)
	var refusal *editor.CommentedJSONError
	if !errors.As(err, &refusal) || !strings.Contains(err.Error(), ".vscode/settings.json") {
		t.Fatalf("dry run did not predict the refusal: %v", err)
	}
	if got := readOnboardOutput(t, path); got != commented {
		t.Fatalf("dry run changed the settings file:\n%s", got)
	}
	if onboardExists(repo, ".vscode/tasks.json") || onboardExists(repo, "AGENTS.md") {
		t.Error("dry run wrote a file")
	}
}

// Boundary: a dry run over a file a live run would merge writes nothing and reports no editor
// outcome.
func TestOnboardRepository_Boundary_DryRunLeavesVSCodeSettings(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	const mergeable = "{\n  \"repo.key\": true\n}\n"
	path := writeVSCodeSettings(t, repo, mergeable)
	plan, err := OnboardRepository(context.Background(), repo, true)
	if err != nil {
		t.Fatalf("dry run: %v", err)
	}
	if got := readOnboardOutput(t, path); got != mergeable {
		t.Fatalf("dry run changed the settings file:\n%s", got)
	}
	if onboardExists(repo, ".vscode/tasks.json") || onboardExists(repo, "AGENTS.md") {
		t.Error("dry run wrote a file")
	}
	if len(plan.EditorFiles) != 0 {
		t.Errorf("dry run reported editor outcomes: %+v", plan.EditorFiles)
	}
}

// Positive: the editor set is resolved before the scaffold is written, and the files written
// from it verify against the set the scaffolded repository synthesizes, so resolving early
// cannot leave editor files that `editors verify` reports as drift.
func TestOnboardRepository_Positive_EditorSetResolvedBeforeScaffoldVerifies(t *testing.T) {
	repo := t.TempDir()
	ctx := context.Background()
	// A bare repository has no lock, so the run ends at lock verification, after every file.
	if _, err := OnboardRepository(ctx, repo, false); !errors.Is(err, ErrOnboardingIncomplete) {
		t.Fatalf("onboard: %v, want the missing-lock diagnostic", err)
	}
	selection, err := editor.SelectEditors(nil)
	if err != nil {
		t.Fatal(err)
	}
	opts := editor.DefaultOptions()
	opts.WorkspaceRoot, opts.Editors = repo, selection.Editors
	set, err := editor.SynthesizeContext(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := editor.VerifyWithReport(set, repo); err != nil {
		t.Fatalf("editor files resolved before the scaffold drift from the scaffolded repository: %v", err)
	}
}

// Negative: a file edited after the editor set was resolved is refused at publish instead of
// being overwritten with content merged from the earlier read; the scaffold is already written
// by then, so the error says so and names the command that finishes the run.
func TestPublishOnboardEditors_Negative_RefusesFileEditedAfterPrepare(t *testing.T) {
	repo := vscodeOnboardFixture(t)
	path := writeVSCodeSettings(t, repo, "{\n  \"repo.key\": \"kept\"\n}\n")
	ctx := context.Background()
	editors, err := prepareOnboardEditors(ctx, repo, []string{"vscode"})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	const edited = "{\n  \"repo.key\": \"edited\"\n}\n"
	mustWriteFile(t, path, edited)
	files, err := publishOnboardEditors(ctx, repo, editors)
	if !errors.Is(err, ErrEditorFilesIncomplete) || !strings.Contains(err.Error(), "settings.json") ||
		!strings.Contains(err.Error(), "scaffold files have been written") ||
		!strings.Contains(err.Error(), "harvest onboard --repo="+repo+" --dry-run=false") {
		t.Fatalf("edited file not refused with the recovery command: %v", err)
	}
	if got := readOnboardOutput(t, path); got != edited {
		t.Fatalf("an edit made after prepare was overwritten:\n%s", got)
	}
	if files != nil {
		t.Errorf("a failed publish reported outcomes: %+v", files)
	}
}

// Boundary: with no editor selected there is nothing to publish, and an unchanged prepared set
// publishes every outcome it predicted.
func TestPublishOnboardEditors_Boundary_NilAndUnchangedSet(t *testing.T) {
	ctx := context.Background()
	if files, err := publishOnboardEditors(ctx, t.TempDir(), nil); err != nil || files != nil {
		t.Fatalf("nil set = %+v, %v; want nothing", files, err)
	}
	repo := vscodeOnboardFixture(t)
	editors, err := prepareOnboardEditors(ctx, repo, []string{"vscode"})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	files, err := publishOnboardEditors(ctx, repo, editors)
	if err != nil || len(files) != len(editors.Results()) || len(files) == 0 {
		t.Fatalf("publish = %+v, %v; want the %d predicted outcomes", files, err, len(editors.Results()))
	}
}
