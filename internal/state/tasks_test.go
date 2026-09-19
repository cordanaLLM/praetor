package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitWorkingDirContext(t *testing.T) {
	dir := t.TempDir()
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := InitWorkingDirContext(cancelled, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled initialization: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, WorkingDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled initialization wrote working directory: %v", err)
	}
	var absent context.Context
	if err := InitWorkingDirContext(absent, dir); err == nil {
		t.Fatal("nil context must fail")
	}
	if err := InitWorkingDirContext(t.Context(), dir); err != nil {
		t.Fatal(err)
	}
	if _, err := ListTasksContext(t.Context(), dir); err != nil {
		t.Fatalf("initialized ledger unreadable: %v", err)
	}
}

func TestArchiveCompletedTasksPreservesOpenWhenBacklogUnreadable(t *testing.T) {
	dir := t.TempDir()
	if err := InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	open := filepath.Join(dir, WorkingDirName, "OPEN.md")
	original := "# Open\n- [x] Preserve this task\n"
	if err := os.WriteFile(open, []byte(original), 0644); err != nil {
		t.Fatal(err)
	}
	backlog := filepath.Join(dir, WorkingDirName, "BACKLOG.md")
	if err := os.Remove(backlog); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(backlog, 0755); err != nil {
		t.Fatal(err)
	}
	if count, err := ArchiveCompletedTasks(dir, "fixture"); err == nil || count != 0 {
		t.Fatalf("unreadable backlog should fail: %d, %v", count, err)
	}
	content, err := os.ReadFile(open)
	if err != nil || string(content) != original {
		t.Fatalf("archive lost OPEN content: %q, %v", content, err)
	}
}

func TestTasks_Positive_Lifecycle(t *testing.T) {
	tempDir := t.TempDir()

	if err := InitWorkingDir(tempDir); err != nil {
		t.Fatalf("InitWorkingDir failed: %v", err)
	}

	tasks, err := ListTasks(tempDir)
	if err != nil {
		t.Fatalf("ListTasks failed: %v", err)
	}
	initialCount := len(tasks)

	if err = AddTask(tempDir, "Implement Milestone Engine"); err != nil {
		t.Fatalf("AddTask failed: %v", err)
	}

	tasks, err = ListTasks(tempDir)
	if err != nil || len(tasks) != initialCount+1 {
		t.Fatalf("expected %d tasks, got %d (err: %v)", initialCount+1, len(tasks), err)
	}

	if err = CompleteTask(tempDir, "Milestone Engine"); err != nil {
		t.Fatalf("CompleteTask failed: %v", err)
	}

	tasks, err = ListTasks(tempDir)
	if err != nil || !tasks[len(tasks)-1].Completed {
		t.Fatalf("expected completed task: %v", err)
	}

	archived, err := ArchiveCompletedTasks(tempDir, "testsha")
	if err != nil || archived == 0 {
		t.Fatalf("ArchiveCompletedTasks failed (%d archived): %v", archived, err)
	}

	verifyBacklogArchived(t, tempDir, "Milestone Engine")
}

func verifyBacklogArchived(t *testing.T, tempDir, expectedTask string) {
	t.Helper()
	backlogPath := filepath.Join(tempDir, WorkingDirName, "BACKLOG.md")
	content, err := os.ReadFile(backlogPath)
	if err != nil {
		t.Fatalf("read BACKLOG.md failed: %v", err)
	}
	if !strings.Contains(string(content), expectedTask) {
		t.Errorf("expected BACKLOG.md to contain '%s', got: %s", expectedTask, string(content))
	}
}

func TestTasks_Negative_Errors(t *testing.T) {
	tempDir := t.TempDir()

	// Empty description
	if err := AddTask(tempDir, "   "); err == nil {
		t.Error("expected error adding empty task")
	}

	// Complete on nonexistent
	if err := CompleteTask(tempDir, "nonexistent-task"); err == nil {
		t.Error("expected error completing nonexistent task")
	}

	// Empty selector
	if err := CompleteTask(tempDir, ""); err == nil {
		t.Error("expected error completing with empty selector")
	}
}

func TestTasks_Boundary_MultipleAndNumeric(t *testing.T) {
	tempDir := t.TempDir()
	if err := InitWorkingDir(tempDir); err != nil {
		t.Fatal(err)
	}

	// The scaffolded ledger holds no task rows, so numbering starts at 1 here.
	for _, description := range []string{"Alpha", "Beta", "Gamma"} {
		if err := AddTask(tempDir, description); err != nil {
			t.Fatal(err)
		}
	}

	// Complete 2nd task by numeric index "2"
	if err := CompleteTask(tempDir, "2"); err != nil {
		t.Fatalf("CompleteTask by numeric index failed: %v", err)
	}

	tasks, err := ListTasks(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks, got %d", len(tasks))
	}
	if tasks[0].Completed || !tasks[1].Completed || tasks[2].Completed {
		t.Errorf("expected only task 2 (Beta) to be completed: %+v", tasks)
	}
}
