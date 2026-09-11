package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestInitWorkingDir_Positive(t *testing.T) {
	tmp := t.TempDir()
	if err := InitWorkingDir(tmp); err != nil {
		t.Fatalf("InitWorkingDir failed: %v", err)
	}

	wDir := filepath.Join(tmp, WorkingDirName)
	if !util.DirExists(wDir) {
		t.Fatalf("expected %s to exist", wDir)
	}

	expectedFiles := []string{"STATE.md", "OPEN.md", "BACKLOG.md", "BUGS.md", "QUESTIONS.md"}
	for _, f := range expectedFiles {
		if !util.FileExists(filepath.Join(wDir, f)) {
			t.Errorf("expected file %s to exist", f)
		}
	}
}

func TestAddAndListBugs_Positive(t *testing.T) {
	tmp := t.TempDir()
	bug, err := AddBug(tmp, BugEntry{
		Title:    "Memory buffer overflow in parser",
		Severity: "p1",
		Location: "internal/parser/parser.go:42",
		Context:  "High load test trigger",
	})
	if err != nil {
		t.Fatalf("AddBug failed: %v", err)
	}
	if bug.ID != "BUG-001" || bug.Status != "open" {
		t.Errorf("unexpected bug attributes: %+v", bug)
	}

	openBugs, err := ListBugs(tmp, "open")
	if err != nil {
		t.Fatalf("ListBugs failed: %v", err)
	}
	if len(openBugs) != 1 {
		t.Fatalf("expected 1 open bug, got %d", len(openBugs))
	}

	if err := ResolveBug(tmp, "BUG-001", "Fixed by bounding slice capacity"); err != nil {
		t.Fatalf("ResolveBug failed: %v", err)
	}

	resolvedBugs, err := ListBugs(tmp, "resolved")
	if err != nil {
		t.Fatalf("ListBugs resolved failed: %v", err)
	}
	if len(resolvedBugs) != 1 || resolvedBugs[0].Resolution == "" {
		t.Errorf("expected resolved bug with resolution, got %+v", resolvedBugs)
	}
}

func TestAddAndDecideQuestions_Positive(t *testing.T) {
	tmp := t.TempDir()
	q, err := AddQuestion(tmp, QuestionEntry{
		Question: "Should telemetry be enabled by default?",
		Options:  []string{"Yes", "No", "Opt-in only"},
	})
	if err != nil {
		t.Fatalf("AddQuestion failed: %v", err)
	}
	if q.ID != "Q-001" || q.Status != "pending" {
		t.Errorf("unexpected question attributes: %+v", q)
	}

	pending, err := ListQuestions(tmp, "pending")
	if err != nil {
		t.Fatalf("ListQuestions failed: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending question, got %d", len(pending))
	}

	if err := DecideQuestion(tmp, "Q-001", "Opt-in only"); err != nil {
		t.Fatalf("DecideQuestion failed: %v", err)
	}

	decided, err := ListQuestions(tmp, "decided")
	if err != nil {
		t.Fatalf("ListQuestions decided failed: %v", err)
	}
	if len(decided) != 1 || decided[0].SelectedAnswer != "Opt-in only" {
		t.Errorf("expected decided question, got %+v", decided)
	}
}

func TestSyncState_Positive(t *testing.T) {
	tmp := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	snap, err := SyncState(ctx, tmp, "Testing session sync")
	if err != nil {
		t.Fatalf("SyncState failed: %v", err)
	}
	if snap == nil {
		t.Fatal("expected non-nil snapshot")
	}

	stateMD := filepath.Join(tmp, WorkingDirName, "STATE.md")
	if !util.FileExists(stateMD) {
		t.Errorf("expected STATE.md to be created")
	}
}

func TestResolveBug_Negative_NotFound(t *testing.T) {
	tmp := t.TempDir()
	if err := InitWorkingDir(tmp); err != nil {
		t.Fatal(err)
	}
	err := ResolveBug(tmp, "BUG-999", "No resolution")
	if err == nil {
		t.Errorf("expected error for non-existent bug")
	}
}

func TestDecideQuestion_Negative_NotFound(t *testing.T) {
	tmp := t.TempDir()
	if err := InitWorkingDir(tmp); err != nil {
		t.Fatal(err)
	}
	err := DecideQuestion(tmp, "Q-999", "Yes")
	if err == nil {
		t.Errorf("expected error for non-existent question")
	}
}

func TestAuditWorkingDir_Negative_MissingDir(t *testing.T) {
	tmp := t.TempDir()
	rep, err := AuditWorkingDir(tmp)
	if err != nil {
		t.Fatalf("AuditWorkingDir failed: %v", err)
	}
	if rep.Valid || rep.WorkingDirExists {
		t.Errorf("expected invalid report for missing working dir")
	}
}

func TestListBugs_Boundary_Empty(t *testing.T) {
	tmp := t.TempDir()
	bugs, err := ListBugs(tmp, "all")
	if err != nil {
		t.Fatalf("ListBugs failed: %v", err)
	}
	if len(bugs) != 0 {
		t.Errorf("expected 0 bugs, got %d", len(bugs))
	}
}

func TestListQuestions_Boundary_Empty(t *testing.T) {
	tmp := t.TempDir()
	qs, err := ListQuestions(tmp, "all")
	if err != nil {
		t.Fatalf("ListQuestions failed: %v", err)
	}
	if len(qs) != 0 {
		t.Errorf("expected 0 questions, got %d", len(qs))
	}
}

func TestAuditWorkingDir_Boundary_P0BlockerViolation(t *testing.T) {
	tmp := t.TempDir()
	if err := InitWorkingDir(tmp); err != nil {
		t.Fatal(err)
	}
	_, err := AddBug(tmp, BugEntry{
		Title:    "Critical security auth bypass",
		Severity: "p0",
	})
	if err != nil {
		t.Fatal(err)
	}

	rep, err := AuditWorkingDir(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Valid {
		t.Errorf("expected audit to be invalid due to open P0 blocker")
	}
	if rep.P0Bugs != 1 {
		t.Errorf("expected 1 P0 bug, got %d", rep.P0Bugs)
	}
}
