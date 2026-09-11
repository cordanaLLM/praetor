package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

	// Clear OPEN.md to test numeric indexes from 1
	openPath := filepath.Join(tempDir, WorkingDirName, "OPEN.md")
	_ = os.WriteFile(openPath, []byte("# Open Items\n"), 0644)

	_ = AddTask(tempDir, "Alpha")
	_ = AddTask(tempDir, "Beta")
	_ = AddTask(tempDir, "Gamma")

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
