package milestone

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// TestMilestoneLedger_Positive_CreatedOwnerOnly pins BUG-839: the milestone store and the
// backlog it synchronizes are private working state, so they are created owner-only.
func TestMilestoneLedger_Positive_CreatedOwnerOnly(t *testing.T) {
	root := t.TempDir()
	if _, err := CreateMilestone(t.Context(), root, "v1", "first", nil); err != nil {
		t.Fatalf("CreateMilestone: %v", err)
	}
	working := filepath.Join(root, state.WorkingDirName)
	testsupport.RequireCreatedMode(t, working, 0o700)
	testsupport.RequireCreatedMode(t, filepath.Join(working, MilestonesFile), 0o600)
	testsupport.RequireCreatedMode(t, filepath.Join(working, BacklogFile), 0o600)
}

// TestMilestoneLedger_Boundary_WideWorkingDirIsTightened: a working directory an older build
// left group- and world-readable is narrowed to owner-only on the next write, never kept wide.
func TestMilestoneLedger_Boundary_WideWorkingDirIsTightened(t *testing.T) {
	root := t.TempDir()
	working := filepath.Join(root, state.WorkingDirName)
	if err := os.Mkdir(working, util.TrackedDirPerm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(working, util.TrackedDirPerm); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateMilestone(t.Context(), root, "v1", "first", nil); err != nil {
		t.Fatalf("CreateMilestone: %v", err)
	}
	testsupport.RequireCreatedMode(t, working, 0o700)
}
