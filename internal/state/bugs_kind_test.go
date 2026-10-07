// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"path/filepath"
	"strings"
	"testing"
)

func bugByID(t *testing.T, root, id string) BugEntry {
	t.Helper()
	bugs, err := ListBugs(root, "all")
	if err != nil {
		t.Fatal(err)
	}
	for _, bug := range bugs {
		if bug.ID == id {
			return bug
		}
	}
	t.Fatalf("%s not in %+v", id, bugs)
	return BugEntry{}
}

// Positive: a kind round-trips through the sidecar, and a row without one keeps the sidecar
// bytes it had before the member existed.
func TestBugKind_Positive_RoundTripsThroughTheSidecar(t *testing.T) {
	root := t.TempDir()
	if _, err := AddBug(root, BugEntry{Title: "labelled", Kind: BugKindDefect}); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBug(root, BugEntry{Title: "unlabelled"}); err != nil {
		t.Fatal(err)
	}
	if got := bugByID(t, root, "BUG-001").Kind; got != BugKindDefect {
		t.Fatalf("kind = %q, want defect", got)
	}
	if got := bugByID(t, root, "BUG-002").Kind; got != "" {
		t.Fatalf("unlabelled row read a kind: %q", got)
	}
	sidecar := readLedgerFile(t, filepath.Join(root, WorkingDirName, bugMetaName))
	if strings.Count(sidecar, `"kind"`) != 1 || !strings.Contains(sidecar, `"kind":"defect"`) {
		t.Fatalf("sidecar must carry the one kind and omit the empty one: %s", sidecar)
	}
}

// Positive: SetBugKind labels a legacy row, which has no metadata, by moving it to the sidecar
// form, and leaves every other row untouched.
func TestSetBugKind_Positive_LabelsALegacyRow(t *testing.T) {
	root := t.TempDir()
	legacy := defaultBugsMD() + "| `BUG-001` | old row | p1 | open | core |  |\n| `BUG-002` | other | p2 | open | x.go:1 |  |\n"
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, bugLedgerName), legacy)
	if err := SetBugKind(root, "BUG-001", BugKindScope); err != nil {
		t.Fatal(err)
	}
	if got := bugByID(t, root, "BUG-001"); got.Kind != BugKindScope || got.Title != "old row" || got.Status != "open" {
		t.Fatalf("labelled row = %+v", got)
	}
	ledger := readLedgerFile(t, filepath.Join(root, WorkingDirName, bugLedgerName))
	if !strings.Contains(ledger, "| `BUG-002` | other | p2 | open | x.go:1 |  |\n") {
		t.Fatalf("the other row changed:\n%s", ledger)
	}
	if err := SetBugKind(root, "BUG-001", BugKindDefect); err != nil || bugByID(t, root, "BUG-001").Kind != BugKindDefect {
		t.Fatalf("relabel: %v", err)
	}
}

// Negative: an unknown kind is refused by the writer, the sidecar reader and the inline
// reader; an empty kind member is refused rather than read as unlabelled; an unknown ID fails.
func TestBugKind_Negative_RefusesUnknownKinds(t *testing.T) {
	root := t.TempDir()
	if _, err := AddBug(root, BugEntry{Title: "x", Kind: "feature"}); err == nil {
		t.Fatal("AddBug accepted an unknown kind")
	}
	if _, err := AddBug(root, BugEntry{Title: "x"}); err != nil {
		t.Fatal(err)
	}
	if err := SetBugKind(root, "BUG-001", "feature"); err == nil {
		t.Fatal("SetBugKind accepted an unknown kind")
	}
	if err := SetBugKind(root, "BUG-404", BugKindDefect); err == nil {
		t.Fatal("SetBugKind accepted an unknown ID")
	}
	record := `"context":"","created_at":"2026-10-07T00:00:00Z","resolved_at":"0001-01-01T00:00:00Z"`
	for name, kind := range map[string]string{"unknown": `,"kind":"feature"`, "empty": `,"kind":""`, "null": `,"kind":null`} {
		writeIntegrityFile(t, filepath.Join(root, WorkingDirName, bugMetaName), `{"version":1,"bugs":{"BUG-001":{`+record+kind+`}}}`)
		if bugs, err := ListBugs(root, "all"); err == nil {
			t.Errorf("%s kind accepted: %+v", name, bugs)
		}
	}
}

// Negative: the questions sidecar shares the record codec but refuses a kind.
func TestBugKind_Negative_QuestionsRefuseAKind(t *testing.T) {
	row := defaultQuestionsMD() + "| `Q-001` | q |  | pending |  | " + questionMetadataRef + "\n"
	root := writeQuestionFixture(t, row)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, questionMetaName),
		`{"version":1,"questions":{"Q-001":{"context":"c","created_at":"2026-09-25T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z","kind":"defect"}}}`)
	if qs, err := ListQuestions(root, "all"); err == nil || !strings.Contains(err.Error(), "kind is not a questions member") {
		t.Fatalf("question with a kind: %+v %v", qs, err)
	}
}

// Boundary: the self-contained inline form carries a kind too, and a row of each status is
// unresolved exactly while open or investigating.
func TestBugKind_Boundary_InlineFormAndUnresolvedStatuses(t *testing.T) {
	md, err := RenderBugsMarkdownStrict([]BugEntry{{ID: "BUG-001", Title: "t", Severity: "p1", Status: "open", Kind: BugKindScope}})
	if err != nil {
		t.Fatal(err)
	}
	bugs, err := ParseBugsMarkdownStrict(md)
	if err != nil || len(bugs) != 1 || bugs[0].Kind != BugKindScope {
		t.Fatalf("inline kind: %+v %v", bugs, err)
	}
	for status, want := range map[string]bool{"open": true, "investigating": true, "deferred": false, "resolved": false} {
		if got := (BugEntry{Status: status}).Unresolved(); got != want {
			t.Errorf("status %s: unresolved = %t, want %t", status, got, want)
		}
	}
}

// Positive, negative and boundary: a task records the nearest heading above it; a heading
// inside a fence and a '#' without a space are not headings; a row before any heading has none.
func TestListTasks_SectionIsTheNearestHeading(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	open := "- [ ] before\n## In-Flight Tasks\n- [ ] first\n```\n## fenced\n```\n#nospace\n- [x] done\n### Spikes ###\n- [ ] second\n"
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), open)
	tasks, err := ListTasks(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"before": "", "first": "In-Flight Tasks", "done": "In-Flight Tasks", "second": "Spikes"}
	if len(tasks) != len(want) {
		t.Fatalf("tasks = %+v", tasks)
	}
	for _, task := range tasks {
		if section, ok := want[task.Description]; !ok || section != task.Section {
			t.Errorf("task %q: section %q, want %q", task.Description, task.Section, section)
		}
	}
}
