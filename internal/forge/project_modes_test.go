package forge

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestProjectCache_Positive_CreatedOwnerOnly pins BUG-839: the project cache is private
// working state, so a fresh cache and its working directory are created owner-only.
func TestProjectCache_Positive_CreatedOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	pm := newTestProjectManager(t, "", "")
	if _, err := pm.AddItem(t.Context(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42"); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	working := filepath.Join(dir, state.WorkingDirName)
	testsupport.RequireCreatedMode(t, working, 0o700)
	testsupport.RequireCreatedMode(t, filepath.Join(working, ProjectCacheFile), 0o600)
}

// TestProjectCache_Boundary_WideWorkingDirIsTightened: a pre-existing 0750 working directory,
// the mode this cache used to create, is narrowed to owner-only on the next write.
func TestProjectCache_Boundary_WideWorkingDirIsTightened(t *testing.T) {
	dir := setupTestProjectDir(t)
	working := filepath.Join(dir, state.WorkingDirName)
	if err := os.Chmod(working, 0o750); err != nil {
		t.Fatal(err)
	}
	pm := newTestProjectManager(t, "", "")
	if _, err := pm.AddItem(t.Context(), dir, 1, "https://github.com/cordanaLLM/praetor/issues/42"); err != nil {
		t.Fatalf("AddItem: %v", err)
	}
	testsupport.RequireCreatedMode(t, working, 0o700)
}
