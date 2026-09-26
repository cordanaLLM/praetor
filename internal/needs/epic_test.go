package needs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/forge"

	"github.com/cordanaLLM/praetor/internal/util"
)

// fakeForge is a Forge stub that hands out increasing issue numbers and lists every
// issue it holds, so a test can observe the dependency chain PublishPreMigrationEpic
// builds, publish twice against the same inventory, and inject failures. The separate
// HTTP test below exercises the real GitHub driver.
type fakeForge struct {
	existing []forge.IssueSpec // inventory ListIssues returns; created issues join it
	created  []forge.IssueSpec // every CreateIssue request, the refused one included
	updates  int
	next     int
	failOn   int // 1-based index of the CreateIssue call that fails; 0 never fails
	listErr  error
}

func (f *fakeForge) Name() string                       { return "fake" }
func (f *fakeForge) Authenticate(context.Context) error { return nil }

func (f *fakeForge) ReconcileProtection(context.Context, string, *config.BranchProtectionPolicy) error {
	return nil
}
func (f *fakeForge) ReconcileLabels(context.Context, []forge.Label) error          { return nil }
func (f *fakeForge) PostStatusCheck(context.Context, string, forge.CheckRun) error { return nil }

func (f *fakeForge) CreatePullRequest(context.Context, forge.PRRequest) (*forge.PRResponse, error) {
	return nil, errors.New("fake forge: pull requests are not implemented")
}

func (f *fakeForge) ListIssues(context.Context, string) ([]forge.IssueSpec, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]forge.IssueSpec(nil), f.existing...), nil
}

func (f *fakeForge) UpdateIssue(context.Context, int, []string, string) error {
	f.updates++
	return nil
}

func (f *fakeForge) CreateIssue(_ context.Context, spec forge.IssueSpec) (*forge.IssueResponse, error) {
	f.created = append(f.created, spec)
	if f.failOn == len(f.created) {
		return nil, errors.New("fake forge: create refused")
	}
	f.next += 100
	f.existing = append(f.existing, forge.IssueSpec{ID: f.next, Title: spec.Title, State: "open"})
	return &forge.IssueResponse{
		Number: f.next,
		URL:    fmt.Sprintf("https://forge.test/issues/%d", f.next),
		State:  "open",
	}, nil
}

func writeEpicFixtureRepo(t *testing.T) string {
	t.Helper()
	tempDir := t.TempDir()
	goMod := "module github.com/test/epic-target\ngo 1.27\nrequire (\n\tgithub.com/jackc/pgx/v5 v5.5.0\n\tgithub.com/unknown/lib v1.0.0\n)\n"
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	return tempDir
}

func TestGeneratePreMigrationEpic_Positive(t *testing.T) {
	ctx := context.Background()
	tempDir := writeEpicFixtureRepo(t)

	epic, err := GeneratePreMigrationEpic(ctx, tempDir, "")
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}

	if epic.RepoName != "test/epic-target" {
		t.Errorf("expected repo name test/epic-target, got %s", epic.RepoName)
	}
	if epic.CoverageBasis != FrameworkCatalogDeclared || !strings.Contains(epic.ChecklistMarkdown, "builds and tests not run") {
		t.Fatalf("epic must retain unverified catalog basis: %+v", epic)
	}
	if len(epic.ChildIssues) != totalEpicTasks {
		t.Fatalf("expected %d child tasks, got %d", totalEpicTasks, len(epic.ChildIssues))
	}

	for i := 1; i < len(epic.ChildIssues); i++ {
		want := taskAnchor(i)
		if got := epic.ChildIssues[i].DependsOn; len(got) != 1 || got[0] != want {
			t.Errorf("task %d: expected DependsOn %q, got %v", i+1, want, got)
		}
	}
	if !strings.Contains(epic.ChecklistMarkdown, "Pre-Migration Epic") {
		t.Errorf("checklist markdown missing title: %s", epic.ChecklistMarkdown)
	}
}

// TestGeneratePreMigrationEpic_FrameworkIsHonoured pins the --framework contract: the
// operator's framework reaches the epic, and a local filesystem path never does.
func TestGeneratePreMigrationEpic_FrameworkIsHonoured(t *testing.T) {
	ctx := context.Background()
	tempDir := writeEpicFixtureRepo(t)

	epic, err := GeneratePreMigrationEpic(ctx, tempDir, "github.com/acme/otherkit")
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	want := "github.com/acme/otherkit"
	if epic.TargetFramework != want {
		t.Errorf("expected target framework %q, got %q", want, epic.TargetFramework)
	}
	if !strings.Contains(epic.ChecklistMarkdown, want) {
		t.Errorf("checklist does not name the requested framework: %s", epic.ChecklistMarkdown)
	}

	localPath := filepath.Join(t.TempDir(), "not-a-checkout")
	if _, err = GeneratePreMigrationEpic(ctx, tempDir, localPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing selected source must fail without public fallback: %v", err)
	}
}

// TestGeneratePreMigrationEpic_NoPlaceholderIssueRefs pins that the pre-publish body
// carries no "<repo>#<n>" reference, which a forge would auto-link to unrelated issues.
func TestGeneratePreMigrationEpic_NoPlaceholderIssueRefs(t *testing.T) {
	ctx := context.Background()
	epic, err := GeneratePreMigrationEpic(ctx, writeEpicFixtureRepo(t), "")
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	for n := 1; n <= totalEpicTasks; n++ {
		ref := fmt.Sprintf("%s#%d", epic.RepoName, n)
		if strings.Contains(epic.ChecklistMarkdown, ref) {
			t.Errorf("epic body cross-references placeholder issue %q", ref)
		}
	}
}

func TestGeneratePreMigrationEpic_Negative(t *testing.T) {
	ctx := context.Background()
	if _, err := GeneratePreMigrationEpic(ctx, "/nonexistent/invalid/repo", "github.com/golusoris/golusoris"); err == nil {
		t.Fatal("expected error for nonexistent repository")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := GeneratePreMigrationEpic(cancelled, t.TempDir(), ""); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestWriteEpicMarkdown_Boundary(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "epics", "TARGET_EPIC.md")

	epic := &PreMigrationEpic{
		RepoName:          "sample-repo",
		TargetFramework:   "github.com/golusoris/golusoris",
		ReadinessScore:    75.0,
		ChecklistMarkdown: "# Sample Epic Checklist",
		ChildIssues:       createChildTasks("sample-repo", &MigrationPlan{Framework: "github.com/golusoris/golusoris"}),
	}

	if err := WriteEpicMarkdown(context.Background(), epic, tempFile); err != nil {
		t.Fatalf("failed to write epic markdown: %v", err)
	}

	data, err := os.ReadFile(tempFile) // #nosec G304 -- test-local path from t.TempDir
	if err != nil {
		t.Fatalf("failed to read generated epic markdown: %v", err)
	}
	if !strings.Contains(string(data), "[TASK 1/5]") {
		t.Errorf("missing [TASK 1/5] in markdown output: %s", string(data))
	}

	info, err := os.Stat(tempFile)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); util.ModeIsProtection() && perm != 0o600 {
		t.Errorf("expected the epic to be owner-only, got %#o", perm)
	}
}

func TestWriteEpicMarkdown_Negative(t *testing.T) {
	ctx := context.Background()
	if err := WriteEpicMarkdown(ctx, nil, filepath.Join(t.TempDir(), "epic.md")); !errors.Is(err, ErrNilEpic) {
		t.Fatalf("expected ErrNilEpic, got %v", err)
	}
	if err := WriteEpicMarkdown(ctx, &PreMigrationEpic{}, "  "); err == nil {
		t.Fatal("expected an error for an empty output path")
	}

	// A regular file in place of the parent directory makes the write impossible.
	root := t.TempDir()
	blocker := filepath.Join(root, "blocked")
	if err := os.WriteFile(blocker, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteEpicMarkdown(ctx, &PreMigrationEpic{}, filepath.Join(blocker, "epic.md")); err == nil {
		t.Fatal("expected an error when the parent directory cannot be created")
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WriteEpicMarkdown(cancelled, &PreMigrationEpic{}, filepath.Join(root, "epic.md")); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

func TestPublishPreMigrationEpic_Positive(t *testing.T) {
	ctx := context.Background()
	f := &fakeForge{}

	epic := &PreMigrationEpic{
		RepoName:   "test/repo",
		ParentEpic: forge.IssueSpec{Title: "[EPIC] Pre-Migration", Body: "Checklist"},
		ChildIssues: []forge.IssueSpec{
			{Title: "[TASK 1/5] Invariants", Body: "Task body"},
			{Title: "[TASK 2/5] Decoupling", Body: "Task body", DependsOn: []string{taskAnchor(1)}},
			{Title: "[TASK 3/5] Substitution", Body: "Task body", DependsOn: []string{taskAnchor(2)}},
		},
	}

	parentRes, childResults, err := PublishPreMigrationEpic(ctx, f, epic)
	if err != nil {
		t.Fatalf("failed to publish epic: %v", err)
	}
	if parentRes.Number != 100 {
		t.Errorf("expected parent issue number 100, got %d", parentRes.Number)
	}
	if len(childResults) != 3 {
		t.Fatalf("expected 3 child results, got %d", len(childResults))
	}

	// Each child must chain onto the issue number of the child created before it.
	wantChain := map[int]string{2: "test/repo#200", 3: "test/repo#300"}
	for idx, want := range wantChain {
		spec := f.created[idx]
		if len(spec.DependsOn) != 1 || spec.DependsOn[0] != want {
			t.Errorf("child %d: expected DependsOn %q, got %v", idx, want, spec.DependsOn)
		}
		if !strings.Contains(spec.Body, "Depends-On: "+want) {
			t.Errorf("child %d body missing %q: %s", idx, want, spec.Body)
		}
	}
	if !strings.Contains(f.created[1].Body, "*Part of Epic #100") {
		t.Errorf("child body missing parent backlink: %s", f.created[1].Body)
	}
}

func TestPublishPreMigrationEpic_Negative(t *testing.T) {
	ctx := context.Background()
	if _, _, err := PublishPreMigrationEpic(ctx, nil, nil); !errors.Is(err, ErrNilForge) {
		t.Fatalf("expected ErrNilForge, got %v", err)
	}
	if _, _, err := PublishPreMigrationEpic(ctx, &fakeForge{}, nil); !errors.Is(err, ErrNilEpic) {
		t.Fatalf("expected ErrNilEpic, got %v", err)
	}

	epic := &PreMigrationEpic{
		RepoName:    "test/repo",
		ParentEpic:  forge.IssueSpec{Title: "[EPIC]"},
		ChildIssues: []forge.IssueSpec{{Title: "[TASK 1/5]"}, {Title: "[TASK 2/5]"}},
	}
	// The second CreateIssue call is the first child: the parent survives, the error does not.
	parentRes, children, err := PublishPreMigrationEpic(ctx, &fakeForge{failOn: 2}, epic)
	if err == nil {
		t.Fatal("expected the child creation failure to be reported")
	}
	if parentRes == nil || len(children) != 0 {
		t.Fatalf("expected the parent result and no children, got %v / %d", parentRes, len(children))
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := PublishPreMigrationEpic(cancelled, &fakeForge{}, epic); err == nil {
		t.Fatal("expected an error for a cancelled context")
	}
}

// testEpic is a three-task epic whose titles are unique.
func testEpic() *PreMigrationEpic {
	return &PreMigrationEpic{
		RepoName:   "test/repo",
		ParentEpic: forge.IssueSpec{Title: "[EPIC] Pre-Migration", Body: "Checklist", State: "open", Labels: []string{"epic"}},
		ChildIssues: []forge.IssueSpec{
			{Title: "[TASK 1/5] Invariants", Body: "Task body", State: "open", Labels: []string{"task"}},
			{Title: "[TASK 2/5] Decoupling", Body: "Task body", State: "open", DependsOn: []string{taskAnchor(1)}},
			{Title: "[TASK 3/5] Substitution", Body: "Task body", State: "open", DependsOn: []string{taskAnchor(2)}},
		},
	}
}

func issueNumbers(parent *forge.IssueUpsertResult, children []*forge.IssueUpsertResult) []int {
	numbers := []int{parent.Number}
	for _, c := range children {
		numbers = append(numbers, c.Number)
	}
	return numbers
}

// TestPublishPreMigrationEpic_RepublishCreatesNothing pins create-missing publishing:
// publishing the epic again resolves every issue already on the forge by title, creates
// nothing and modifies nothing, even when the regenerated epic's bodies and labels
// changed in between. Publishing never converges an existing issue.
func TestPublishPreMigrationEpic_RepublishCreatesNothing(t *testing.T) {
	ctx := context.Background()
	f := &fakeForge{}
	epic := testEpic()

	parent, children, err := PublishPreMigrationEpic(ctx, f, epic)
	if err != nil {
		t.Fatalf("first publish failed: %v", err)
	}
	first := issueNumbers(parent, children)

	epic.ParentEpic.Body += "\n- **Mapping Availability**: `90.0%`\n"
	epic.ChildIssues[0].Labels = []string{"task", "relabelled"}
	again, againChildren, err := PublishPreMigrationEpic(ctx, f, epic)
	if err != nil {
		t.Fatalf("republish failed: %v", err)
	}
	if len(f.created) != 4 || f.updates != 0 {
		t.Fatalf("republish created or modified issues: %d create requests, %d updates", len(f.created), f.updates)
	}
	second := issueNumbers(again, againChildren)
	if fmt.Sprint(second) != fmt.Sprint(first) {
		t.Fatalf("republish resolved other issues: first %v, second %v", first, second)
	}
	for _, res := range append([]*forge.IssueUpsertResult{again}, againChildren...) {
		if res.Outcome != forge.IssueUnchanged {
			t.Errorf("issue #%d: expected outcome %q, got %q", res.Number, forge.IssueUnchanged, res.Outcome)
		}
	}
}

// TestPublishPreMigrationEpic_TitleConflictFailsBeforeMutation rejects every batch
// whose identity is ambiguous before the first write.
func TestPublishPreMigrationEpic_TitleConflictFailsBeforeMutation(t *testing.T) {
	ctx := context.Background()
	epic := testEpic()

	ambiguous := &fakeForge{existing: []forge.IssueSpec{
		{ID: 1, Title: epic.ParentEpic.Title}, {ID: 2, Title: " " + epic.ParentEpic.Title},
	}}
	_, _, err := PublishPreMigrationEpic(ctx, ambiguous, epic)
	var conflict *forge.IssueTitleConflictError
	if !errors.As(err, &conflict) || conflict.Source != "existing" || len(ambiguous.created) != 0 {
		t.Fatalf("ambiguous existing epic: err=%v, %d create requests", err, len(ambiguous.created))
	}

	duplicate := testEpic()
	duplicate.ChildIssues[2].Title = duplicate.ChildIssues[0].Title
	planned := &fakeForge{}
	if _, _, err := PublishPreMigrationEpic(ctx, planned, duplicate); !errors.As(err, &conflict) || len(planned.created) != 0 {
		t.Fatalf("duplicate planned title: err=%v, %d create requests", err, len(planned.created))
	}

	sentinel := errors.New("inventory unavailable")
	unlisted := &fakeForge{listErr: sentinel}
	if _, _, err := PublishPreMigrationEpic(ctx, unlisted, epic); !errors.Is(err, sentinel) || len(unlisted.created) != 0 {
		t.Fatalf("listing failure must stop the publish before writes: err=%v, %d create requests", err, len(unlisted.created))
	}
}

// TestPublishPreMigrationEpic_ResumesPartialPublish publishes an epic whose third issue
// failed last time: the parent and first task are reused, only the rest is created, and
// the chain continues from the real number of the reused task.
func TestPublishPreMigrationEpic_ResumesPartialPublish(t *testing.T) {
	ctx := context.Background()
	f := &fakeForge{failOn: 3}
	epic := testEpic()

	parent, children, err := PublishPreMigrationEpic(ctx, f, epic)
	if err == nil || parent == nil || len(children) != 1 {
		t.Fatalf("expected the second task to fail after the parent and first task: err=%v children=%d", err, len(children))
	}

	f.failOn = 0
	resumed, resumedChildren, err := PublishPreMigrationEpic(ctx, f, epic)
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if resumed.Number != parent.Number || resumed.Outcome != forge.IssueUnchanged {
		t.Fatalf("resume did not reuse parent #%d: %+v", parent.Number, resumed)
	}
	if len(resumedChildren) != 3 || resumedChildren[0].Number != children[0].Number || resumedChildren[0].Outcome != forge.IssueUnchanged {
		t.Fatalf("resume did not reuse task #%d: %+v", children[0].Number, resumedChildren)
	}
	if len(f.existing) != 4 {
		t.Fatalf("expected 4 issues on the forge after resuming, got %d", len(f.existing))
	}

	next := f.created[len(f.created)-2] // the resumed second task
	wantDep := fmt.Sprintf("test/repo#%d", children[0].Number)
	if len(next.DependsOn) != 1 || next.DependsOn[0] != wantDep {
		t.Errorf("resumed task chains onto %v, want %q", next.DependsOn, wantDep)
	}
	// The reused parent comes from the inventory, which carries no URL.
	if want := fmt.Sprintf("*Part of Epic #%d*\n", parent.Number); !strings.HasSuffix(next.Body, want) {
		t.Errorf("resumed task body lacks backlink %q: %s", want, next.Body)
	}
}

// setupFleetEpicRoot builds a fleet root holding two prepared repositories and one
// directory that carries no repository marker.
func setupFleetEpicRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	repo1 := filepath.Join(root, "org1", "repo1")
	repo2 := filepath.Join(root, "org2", "repo2")
	bare := filepath.Join(root, "org3", "not-a-repo")
	for _, d := range []string{filepath.Join(repo1, ".git"), repo2, bare} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	writes := map[string]string{
		// A checkout is recognised by its HEAD: an empty .git directory is stray
		// metadata, not a repository.
		filepath.Join(repo1, ".git", "HEAD"):    "ref: refs/heads/main\n",
		filepath.Join(repo1, "go.mod"):          "module github.com/org1/repo1\ngo 1.27\n",
		filepath.Join(repo2, "package.json"):    "{\"name\": \"repo2\", \"version\": \"1.0.0\"}\n",
		filepath.Join(repo2, ".standards.yaml"): "repository:\n  name: repo2\n  owner: org2\n",
		filepath.Join(bare, "go.mod"):           "module github.com/org3/not-a-repo\ngo 1.27\n",
	}
	for path, content := range writes {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestRegenerateFleetEpics_DryRunWritesNothing(t *testing.T) {
	ctx := context.Background()
	root := setupFleetEpicRoot(t)

	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil {
		t.Fatalf("dry-run regeneration failed: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 prepared repositories, got %d", len(epics))
	}
	for _, ep := range epics {
		if ep.OutputPath == "" {
			t.Fatalf("dry run must name the file it would write for %s", ep.RepoName)
		}
		if _, statErr := os.Stat(ep.OutputPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Errorf("dry run wrote %s", ep.OutputPath)
		}
	}
}

func TestRegenerateFleetEpics_Positive(t *testing.T) {
	ctx := context.Background()
	root := setupFleetEpicRoot(t)

	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{FrameworkPath: "github.com/acme/otherkit"})
	if err != nil {
		t.Fatalf("regenerate fleet epics failed: %v", err)
	}
	if len(epics) != 2 {
		t.Fatalf("expected 2 regenerated epics, got %d", len(epics))
	}
	for _, ep := range epics {
		if _, statErr := os.Stat(ep.OutputPath); statErr != nil {
			t.Errorf("expected %s to exist: %v", ep.OutputPath, statErr)
		}
		if !strings.HasPrefix(ep.TargetFramework, "github.com/acme/otherkit") {
			t.Errorf("fleet epic ignored the requested framework: %s", ep.TargetFramework)
		}
	}
}

// TestRegenerateFleetEpics_ReportsSkippedDirectories pins that a discovered directory
// without a repository marker is reported with its reason instead of vanishing.
func TestRegenerateFleetEpics_ReportsSkippedDirectories(t *testing.T) {
	ctx := context.Background()
	root := setupFleetEpicRoot(t)

	epics, skips, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil {
		t.Fatalf("regeneration failed: %v", err)
	}
	if len(epics) != 2 || len(skips) != 1 {
		t.Fatalf("expected 2 epics and 1 skip, got %d epics and skips %+v", len(epics), skips)
	}
	if want := filepath.Join(root, "org3", "not-a-repo"); skips[0].RepoDir != want {
		t.Errorf("skipped %q, want %q", skips[0].RepoDir, want)
	}
	if !strings.Contains(skips[0].Reason, "no repository marker") {
		t.Errorf("skip carries no reason: %q", skips[0].Reason)
	}

	_, emptySkips, err := RegenerateFleetEpics(ctx, t.TempDir(), FleetEpicOptions{DryRun: true})
	if err != nil || len(emptySkips) != 0 {
		t.Fatalf("an empty fleet root must report no skips: err=%v skips=%+v", err, emptySkips)
	}
}

func TestRegenerateFleetEpics_ReportsWriteFailures(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	repo := filepath.Join(root, "org", "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module github.com/org/repo\ngo 1.27\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A regular file where the epic directory belongs makes every write fail.
	if err := os.WriteFile(filepath.Join(repo, ".workingdir"), []byte("blocked"), 0o600); err != nil {
		t.Fatal(err)
	}

	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{})
	if err == nil {
		t.Fatal("expected the per-repository write failure to be reported")
	}
	if len(epics) != 0 {
		t.Fatalf("expected no epic to be reported as written, got %d", len(epics))
	}
	if !strings.Contains(err.Error(), repo) {
		t.Errorf("error does not name the failing repository: %v", err)
	}
}

func TestRegenerateFleetEpics_Boundary(t *testing.T) {
	ctx := context.Background()

	epics, _, err := RegenerateFleetEpics(ctx, t.TempDir(), FleetEpicOptions{})
	if err != nil {
		t.Fatalf("regenerate on empty dir failed: %v", err)
	}
	if len(epics) != 0 {
		t.Fatalf("expected 0 epics for empty directory, got %d", len(epics))
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := RegenerateFleetEpics(cancelled, setupFleetEpicRoot(t), FleetEpicOptions{}); err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

// writeRepoFile creates path's parent directories and writes content.
func writeRepoFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRepoIsPrepared(t *testing.T) {
	root := t.TempDir()

	checkout := filepath.Join(root, "checkout")
	writeRepoFile(t, filepath.Join(checkout, ".git", "HEAD"), "ref: refs/heads/main\n")
	worktree := filepath.Join(root, "worktree")
	worktreeGitDir := filepath.Join(checkout, ".git", "worktrees", "wt")
	writeRepoFile(t, filepath.Join(worktreeGitDir, "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(worktree, ".git"), "gitdir: "+worktreeGitDir+"\n")
	submodule := filepath.Join(root, "submodule")
	writeRepoFile(t, filepath.Join(root, ".git", "modules", "sub", "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(submodule, ".git"), "gitdir: ../.git/modules/sub\n")
	manifest := filepath.Join(root, "manifest")
	writeRepoFile(t, filepath.Join(manifest, ".needs.yaml"), "repository:\n  name: manifest\n")
	standards := filepath.Join(root, "standards")
	writeRepoFile(t, filepath.Join(standards, ".standards.yaml"), "repository:\n  name: standards\n")

	empty := filepath.Join(root, "empty")
	if err := os.MkdirAll(empty, 0o750); err != nil {
		t.Fatal(err)
	}
	stray := filepath.Join(root, "stray")
	if err := os.MkdirAll(filepath.Join(stray, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}
	headless := filepath.Join(root, "headless")
	if err := os.MkdirAll(filepath.Join(headless, ".git", "HEAD"), 0o750); err != nil {
		t.Fatal(err)
	}
	emptyGitlink := filepath.Join(root, "empty-gitlink")
	writeRepoFile(t, filepath.Join(emptyGitlink, ".git"), "")
	garbageGitlink := filepath.Join(root, "garbage-gitlink")
	writeRepoFile(t, filepath.Join(garbageGitlink, ".git"), "not a gitlink\n")
	danglingGitlink := filepath.Join(root, "dangling-gitlink")
	writeRepoFile(t, filepath.Join(danglingGitlink, ".git"), "gitdir: "+filepath.Join(root, "absent-gitdir")+"\n")

	cases := []struct {
		name string
		dir  string
		want bool
	}{
		{"normal checkout", checkout, true},
		{"linked worktree gitlink", worktree, true},
		{"submodule gitlink", submodule, true},
		{"needs manifest only", manifest, true},
		{"standards manifest only", standards, true},
		{"empty directory", empty, false},
		{"stray empty .git directory", stray, false},
		{"HEAD is a directory", headless, false},
		{"empty .git file", emptyGitlink, false},
		{"non-gitlink .git file", garbageGitlink, false},
		{"dangling gitlink", danglingGitlink, false},
		{"absent directory", filepath.Join(root, "absent"), false},
	}
	for _, tc := range cases {
		if got := repoIsPrepared(tc.dir); got != tc.want {
			t.Errorf("%s: repoIsPrepared = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRepoIsPrepared_EmptyPathIgnoresWorkingDirectory pins the empty-path answer against the
// working directory instead of against the checkout layout. The process is moved into a
// directory carrying every marker the predicate looks for, so an implementation that lets
// filepath.Join resolve "" relatively answers true here and fails.
func TestRepoIsPrepared_EmptyPathIgnoresWorkingDirectory(t *testing.T) {
	prepared := t.TempDir()
	writeRepoFile(t, filepath.Join(prepared, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(prepared, ".standards.yaml"), "repository:\n  name: cwd\n")
	writeRepoFile(t, filepath.Join(prepared, ".needs.yaml"), "repository:\n  name: cwd\n")
	t.Chdir(prepared)

	if !repoIsPrepared(".") {
		t.Fatal("fixture is not a prepared repository, so the guard assertion would prove nothing")
	}
	if repoIsPrepared("") {
		t.Error("an empty path was resolved against the working directory")
	}
}

func TestRegenerateFleetEpics_LinkedWorktreeAndStrayGit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	worktree := filepath.Join(root, "org", "worktree")
	worktreeGitDir := filepath.Join(root, ".git", "worktrees", "wt")
	writeRepoFile(t, filepath.Join(worktreeGitDir, "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(worktree, ".git"), "gitdir: "+worktreeGitDir+"\n")
	writeRepoFile(t, filepath.Join(worktree, "go.mod"), "module github.com/org/worktree\ngo 1.27\n")

	stray := filepath.Join(root, "org", "stray")
	writeRepoFile(t, filepath.Join(stray, "go.mod"), "module github.com/org/stray\ngo 1.27\n")
	if err := os.MkdirAll(filepath.Join(stray, ".git"), 0o750); err != nil {
		t.Fatal(err)
	}

	epics, skips, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil {
		t.Fatalf("regeneration failed: %v", err)
	}
	if len(epics) != 1 {
		t.Fatalf("expected only the linked worktree to be regenerated, got %d epics", len(epics))
	}
	if len(skips) != 1 || skips[0].RepoDir != stray {
		t.Errorf("expected the stray .git directory to be reported as a skip, got %+v", skips)
	}
	if !strings.HasPrefix(epics[0].OutputPath, worktree) {
		t.Errorf("regenerated the wrong repository: %s", epics[0].OutputPath)
	}
}

func TestRegenerateFleetEpics_FleetRootIsRepository(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	writeRepoFile(t, filepath.Join(root, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeRepoFile(t, filepath.Join(root, "go.mod"), "module github.com/org/root\ngo 1.27\n")

	epics, _, err := RegenerateFleetEpics(ctx, root, FleetEpicOptions{DryRun: true})
	if err != nil {
		t.Fatalf("regeneration failed: %v", err)
	}
	if len(epics) != 1 {
		t.Fatalf("expected the fleet root itself to be regenerated, got %d epics", len(epics))
	}
}

// newFakeIssueForge serves the GitHub issue listing and creation endpoints locally, so
// the test stays hermetic and exercises the driver's real HTTP path. The listing
// returns every issue created so far.
func newFakeIssueForge(t *testing.T) *forge.GitHubDriver {
	t.Helper()
	var issues []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			listed := append([]map[string]any{}, issues...)
			if err := json.NewEncoder(w).Encode(listed); err != nil {
				t.Errorf("failed encoding fake listing: %v", err)
			}
			return
		}
		var req struct {
			Title string `json:"title"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("failed decoding create request: %v", err)
		}
		payload := map[string]any{"number": len(issues) + 1, "title": req.Title, "url": "https://forge.invalid/issues", "state": "open"}
		issues = append(issues, payload)
		w.WriteHeader(http.StatusCreated)
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			t.Errorf("failed encoding fake response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	gh := forge.NewGitHubDriver("forge-token", srv.URL)
	gh.SetRepository("test", "repo")
	return gh
}

func TestPublishPreMigrationEpic_HTTP(t *testing.T) {
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

	// Publishing again through the real driver resolves the listed issues.
	again, againChildren, err := PublishPreMigrationEpic(ctx, gh, epic)
	if err != nil {
		t.Fatalf("republishing the epic failed: %v", err)
	}
	if again.Number != 1 || again.Outcome != forge.IssueUnchanged || len(againChildren) != 2 || againChildren[1].Number != 3 {
		t.Fatalf("republish did not resolve the existing issues: %+v %+v", again, againChildren)
	}
}
