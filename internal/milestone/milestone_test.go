package milestone

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/state"
)

func setupTestDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wDir := filepath.Join(dir, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		t.Fatalf("failed creating test workingdir: %v", err)
	}
	backlogPath := filepath.Join(wDir, "BACKLOG.md")
	if err := os.WriteFile(backlogPath, []byte("# Project Backlog\n\n## Future Epics\n\n- Epic 1\n"), 0644); err != nil {
		t.Fatalf("failed initializing BACKLOG.md: %v", err)
	}
	return dir
}

func TestMilestone_Positive_Lifecycle(t *testing.T) {
	dir := setupTestDir(t)

	dueDate := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	m1, err := CreateMilestone(dir, "v1.0.0-GA", "General availability release", &dueDate)
	if err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}
	if m1.Number != 1 || m1.State != StateOpen || m1.Title != "v1.0.0-GA" {
		t.Errorf("unexpected milestone fields: %+v", m1)
	}

	m2, err := CreateMilestone(dir, "v1.1.0-Features", "Next iteration", nil)
	if err != nil {
		t.Fatalf("CreateMilestone 2 failed: %v", err)
	}
	if m2.Number != 2 || m2.DueOn != nil {
		t.Errorf("unexpected milestone 2: %+v", m2)
	}

	list, err := ListMilestones(dir, "open")
	if err != nil {
		t.Fatalf("ListMilestones failed: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 open milestones, got %d", len(list))
	}

	// Close milestone 1 by number
	closed, err := CloseMilestone(dir, "1")
	if err != nil {
		t.Fatalf("CloseMilestone failed: %v", err)
	}
	if closed.State != StateClosed || closed.Progress != 100.0 {
		t.Errorf("expected closed milestone state, got %+v", closed)
	}

	// Verify open list is now 1
	openList, err := ListMilestones(dir, "open")
	if err != nil {
		t.Fatalf("ListMilestones after close failed: %v", err)
	}
	if len(openList) != 1 || openList[0].Number != 2 {
		t.Errorf("expected only milestone 2 open, got %+v", openList)
	}

	// Verify BACKLOG.md got updated with Active Milestones section
	backlogContent, err := os.ReadFile(filepath.Join(dir, state.WorkingDirName, "BACKLOG.md"))
	if err != nil {
		t.Fatalf("read BACKLOG.md failed: %v", err)
	}
	if !strings.Contains(string(backlogContent), "## Active Milestones") {
		t.Errorf("BACKLOG.md does not contain Active Milestones section: %s", string(backlogContent))
	}
	if !strings.Contains(string(backlogContent), "v1.0.0-GA") || !strings.Contains(string(backlogContent), "v1.1.0-Features") {
		t.Errorf("BACKLOG.md missing milestone titles: %s", string(backlogContent))
	}
}

func TestMilestone_Negative_ValidationErrors(t *testing.T) {
	dir := setupTestDir(t)

	// Empty title
	_, err := CreateMilestone(dir, "", "empty title", nil)
	if err == nil {
		t.Fatalf("expected error for empty title, got nil")
	}

	// Duplicate open title
	_, err = CreateMilestone(dir, "Duplicate", "first", nil)
	if err != nil {
		t.Fatalf("first creation failed: %v", err)
	}
	_, err = CreateMilestone(dir, "duplicate", "second", nil)
	if err == nil {
		t.Fatalf("expected error for duplicate title, got nil")
	}

	// Close non-existent milestone
	_, err = CloseMilestone(dir, "999")
	if err == nil {
		t.Fatalf("expected error closing non-existent milestone, got nil")
	}

	// Corrupted JSON in milestones.json
	corruptPath := filepath.Join(dir, state.WorkingDirName, MilestonesFile)
	if err := os.WriteFile(corruptPath, []byte("NOT_JSON"), 0644); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	_, err = ListMilestones(dir, "all")
	if err == nil {
		t.Fatalf("expected error loading corrupted milestones store, got nil")
	}
}

func TestMilestone_Boundary_EmptyAndSelectors(t *testing.T) {
	dir := setupTestDir(t)

	// Empty milestones store
	emptyList, err := ListMilestones(dir, "all")
	if err != nil {
		t.Fatalf("ListMilestones on empty store failed: %v", err)
	}
	if len(emptyList) != 0 {
		t.Errorf("expected 0 milestones in new store, got %d", len(emptyList))
	}

	// Rendering with 0 milestones
	emptyMD := RenderMilestonesMarkdown(nil)
	if !strings.Contains(emptyMD, "No tracked milestones") {
		t.Errorf("unexpected empty markdown: %s", emptyMD)
	}

	// Create and close by substring selector
	_, err = CreateMilestone(dir, "Security Hardening Q4", "Audit and pentest", nil)
	if err != nil {
		t.Fatalf("CreateMilestone failed: %v", err)
	}

	closed, err := CloseMilestone(dir, "Hardening")
	if err != nil {
		t.Fatalf("CloseMilestone with substring selector failed: %v", err)
	}
	if closed.Title != "Security Hardening Q4" {
		t.Errorf("expected closed title 'Security Hardening Q4', got '%s'", closed.Title)
	}
}
