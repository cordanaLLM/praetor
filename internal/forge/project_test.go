package forge

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

func setupTestProjectDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	wDir := filepath.Join(dir, state.WorkingDirName)
	if err := os.MkdirAll(wDir, 0755); err != nil {
		t.Fatalf("failed creating test workingdir: %v", err)
	}
	return dir
}

func TestProject_Positive_LocalLifecycle(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := NewProjectManager("cordanaLLM", "", "")

	// Add an issue item to project #1
	item, err := pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42")
	if err != nil {
		t.Fatalf("AddItem failed: %v", err)
	}
	if item.URL != "https://github.com/cordanaLLM/praetor/issues/42" {
		t.Errorf("unexpected item URL: %s", item.URL)
	}

	// List projects from local cache
	projects, err := pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects failed: %v", err)
	}
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
	if projects[0].Number != 1 || len(projects[0].Items) != 1 {
		t.Errorf("unexpected project details: %+v", projects[0])
	}
}

func TestProject_Negative_EmptyURL(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := NewProjectManager("cordanaLLM", "", "")

	_, err := pm.AddItem(context.Background(), dir, 1, "   ")
	if err == nil {
		t.Fatalf("expected error for empty URL, got nil")
	}

	// Corrupt cache file
	corruptPath := filepath.Join(dir, state.WorkingDirName, ProjectCacheFile)
	if err := os.WriteFile(corruptPath, []byte("NOT_JSON"), 0644); err != nil {
		t.Fatalf("failed writing corrupt cache: %v", err)
	}

	_, err = pm.ListProjects(context.Background(), dir)
	if err == nil {
		t.Fatalf("expected error reading corrupt cache, got nil")
	}
}

func TestProject_Boundary_MultipleItemsAndEmpty(t *testing.T) {
	dir := setupTestProjectDir(t)
	pm := NewProjectManager("cordanaLLM", "", "")

	// Empty projects list
	projects, err := pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects on empty failed: %v", err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}

	// Add 3 items across 2 projects
	_, _ = pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/1")
	_, _ = pm.AddItem(context.Background(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/2")
	_, _ = pm.AddItem(context.Background(), dir, 2, "https://github.com/cordanaLLM/praetor/pull/3")

	projects, err = pm.ListProjects(context.Background(), dir)
	if err != nil {
		t.Fatalf("ListProjects after additions failed: %v", err)
	}
	if len(projects) != 2 {
		t.Fatalf("expected 2 projects, got %d", len(projects))
	}
	if projects[0].TotalItems != 2 {
		t.Errorf("expected project 1 to have 2 items, got %d", projects[0].TotalItems)
	}
	if projects[1].TotalItems != 1 {
		t.Errorf("expected project 2 to have 1 item, got %d", projects[1].TotalItems)
	}
}
