package needs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/forge"
	"github.com/cordanaLLM/praetor/internal/milestone"
	"github.com/cordanaLLM/praetor/internal/state"
)

// slotEpic is a four-task epic whose third slot is omitted and whose parent body carries
// the checklist an epic is first published with.
func slotEpic() *PreMigrationEpic {
	return &PreMigrationEpic{
		RepoName:        "test/repo",
		TargetFramework: acmeKit,
		ParentEpic: forge.IssueSpec{Title: "[EPIC] Slots", State: "open", Labels: []string{"epic"}, Body: strings.Join([]string{
			"# Pre-Migration Epic: test/repo", "", "## Pre-Migration Tasks", "",
			"- [ ] **Task 1**: [TASK 1/5] Hygiene",
			"- [ ] **Task 2**: [TASK 2/5] Decoupling",
			"  - *Prerequisites*: Depends-On: Task 1/5",
			"- **Task 3**: [TASK 3/5] Substitution",
			"  - *Omitted*: nothing to rewrite.",
			"- [ ] **Task 4**: [TASK 4/5] Verification",
			"- [ ] **Task 5**: [TASK 5/5] Activation",
			"", "## Execution Directives",
		}, "\n")},
		ChildIssues: []forge.IssueSpec{
			{Title: "[TASK 1/5] Hygiene", State: "open"},
			{Title: "[TASK 2/5] Decoupling", State: "open"},
			{Title: "[TASK 4/5] Verification", State: "open"},
			{Title: "[TASK 5/5] Activation", State: "open"},
		},
		OmittedTasks: []OmittedEpicTask{{Task: 3, Title: "[TASK 3/5] Substitution", Reason: "nothing to rewrite"}},
	}
}

// Positive: once the children exist, every slot line of the parent's checklist names its
// child issue, the omitted slot stays as it is, and a closed child is ticked.
func TestPublishPreMigrationEpic_Positive_ParentNamesChildIssues(t *testing.T) {
	epic := slotEpic()
	f := &fakeForge{existing: []forge.IssueSpec{{ID: 7, Title: "[TASK 4/5] Verification", State: "closed"}}}

	parent, children, err := PublishPreMigrationEpic(context.Background(), f, epic)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(f.edits) != 1 || f.edits[0].number != parent.Number {
		t.Fatalf("expected one edit of parent #%d, got %+v", parent.Number, f.edits)
	}
	body := f.edits[0].body
	want := []string{
		"- [ ] #" + itoa(children[0].Number),
		"- [ ] #" + itoa(children[1].Number),
		"  - *Prerequisites*: Depends-On: Task 1/5",
		"- **Task 3**: [TASK 3/5] Substitution",
		"- [x] #7",
		"- [ ] #" + itoa(children[3].Number),
	}
	for _, line := range want {
		if !strings.Contains(body, line+"\n") {
			t.Errorf("parent body lacks %q:\n%s", line, body)
		}
	}
	if strings.Contains(body, "- [ ] **Task") || strings.Contains(body, "## Child Issues") {
		t.Fatalf("every slot line must be replaced in place:\n%s", body)
	}
	items, complete := forge.ParseTaskItems(body)
	if !complete || len(items) != 4 {
		t.Fatalf("the planning sync must read four children, got %+v", items)
	}
	if parent.Outcome != forge.IssueCreated {
		t.Fatalf("a created parent stays created: %s", parent.Outcome)
	}
}

// Negative: a refused body edit fails the publish after the children exist, and a parent
// that already names every child is never edited again.
func TestPublishPreMigrationEpic_Negative_LinkFailureAndRepublish(t *testing.T) {
	f := &fakeForge{editErr: errors.New("edit refused")}
	parent, children, err := PublishPreMigrationEpic(context.Background(), f, slotEpic())
	if err == nil || !strings.Contains(err.Error(), "edit refused") || parent == nil || len(children) != 4 {
		t.Fatalf("a refused edit must be reported with the published issues: err=%v children=%d", err, len(children))
	}

	f = &fakeForge{}
	if _, _, err := PublishPreMigrationEpic(context.Background(), f, slotEpic()); err != nil {
		t.Fatal(err)
	}
	again, _, err := PublishPreMigrationEpic(context.Background(), f, slotEpic())
	if err != nil || len(f.edits) != 1 || again.Outcome != forge.IssueUnchanged {
		t.Fatalf("republish must not edit a linked parent: err=%v edits=%d outcome=%s", err, len(f.edits), again.Outcome)
	}
}

// Boundary: an existing parent whose body names none of its children, with no slot line,
// gets them appended under a heading and is reported as updated.
func TestPublishPreMigrationEpic_Boundary_AppendsUnnamedChildren(t *testing.T) {
	epic := testEpic()
	f := &fakeForge{existing: []forge.IssueSpec{{ID: 5, Title: epic.ParentEpic.Title, Body: "Operator-edited body", State: "open"}}}
	parent, children, err := PublishPreMigrationEpic(context.Background(), f, epic)
	if err != nil {
		t.Fatal(err)
	}
	if parent.Outcome != forge.IssueUpdated || len(f.edits) != 1 {
		t.Fatalf("existing parent: outcome %s, %d edits", parent.Outcome, len(f.edits))
	}
	want := "Operator-edited body\n\n## Child Issues\n\n- [ ] #" + itoa(children[0].Number) + "\n- [ ] #" +
		itoa(children[1].Number) + "\n- [ ] #" + itoa(children[2].Number) + "\n"
	if f.edits[0].body != want {
		t.Fatalf("body = %q, want %q", f.edits[0].body, want)
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

// writeMilestoneStore gives repo a milestone store.
func writeMilestoneStore(t *testing.T, repo string, milestones ...milestone.Milestone) {
	t.Helper()
	data, err := json.Marshal(milestone.MilestoneStore{Milestones: milestones})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(repo, state.WorkingDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, milestone.MilestonesFile), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// Positive: the epic and every task carry the repository's active milestone.
func TestGeneratePreMigrationEpic_Positive_CarriesActiveMilestone(t *testing.T) {
	repo := writeEpicFixtureRepo(t)
	writeMilestoneStore(t, repo,
		milestone.Milestone{Number: 1, Title: "Local only", State: milestone.StateOpen},
		milestone.Milestone{Number: 2, Title: "Hardening", State: milestone.StateOpen, RemoteNumber: 4})
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil)
	if err != nil {
		t.Fatal(err)
	}
	if epic.Milestone != "Hardening" || epic.ParentEpic.Milestone != "Hardening" {
		t.Fatalf("epic milestone = %q / %q", epic.Milestone, epic.ParentEpic.Milestone)
	}
	for _, child := range epic.ChildIssues {
		if child.Milestone != "Hardening" {
			t.Errorf("%s carries milestone %q", child.Title, child.Milestone)
		}
	}
}

// Negative: an unreadable milestone store fails the generation instead of dropping the
// milestone.
func TestGeneratePreMigrationEpic_Negative_UnreadableMilestoneStore(t *testing.T) {
	repo := writeEpicFixtureRepo(t)
	dir := filepath.Join(repo, state.WorkingDirName)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, milestone.MilestonesFile), []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil); err == nil || !strings.Contains(err.Error(), "milestone") {
		t.Fatalf("expected a milestone error, got %v", err)
	}
}

// Boundary: without a store, or with only closed milestones, the epic carries none.
func TestGeneratePreMigrationEpic_Boundary_NoActiveMilestone(t *testing.T) {
	repo := writeEpicFixtureRepo(t)
	epic, err := GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil)
	if err != nil || epic.Milestone != "" || epic.ChildIssues[0].Milestone != "" {
		t.Fatalf("no store: milestone %q, %v", epic.Milestone, err)
	}
	writeMilestoneStore(t, repo, milestone.Milestone{Number: 1, Title: "Done", State: milestone.StateClosed, RemoteNumber: 1})
	epic, err = GeneratePreMigrationEpic(t.Context(), repo, acmeSource(""), nil)
	if err != nil || epic.Milestone != "" {
		t.Fatalf("closed milestone only: milestone %q, %v", epic.Milestone, err)
	}
}
