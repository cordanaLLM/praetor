package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A vendor context file that is the compile-context projection of AGENTS.md as HEAD holds it
// is Praetor output nobody edited: AGENTS.md changed after the last compile-context, and the
// agent-harness step synchronizes the vendor file instead of reporting a replaced hand edit.

// commitAll records every file of repoPath as the HEAD commit. It writes the commit with git's
// plumbing, which runs no hook: the adoption installed a pre-commit hook that needs a
// praetorctl this test does not provide.
func commitAll(t *testing.T, repoPath string) {
	t.Helper()
	fixtureGit(t, repoPath, "add", "-A")
	tree := strings.TrimSpace(fixtureGit(t, repoPath, "write-tree"))
	commit := strings.TrimSpace(fixtureGit(t, repoPath, "commit-tree", tree, "-m", "adopted"))
	fixtureGit(t, repoPath, "update-ref", "HEAD", commit)
}

// editAgentsLine changes shared text and adds client-owned text without recompiling.
// The owned section makes the pointer projection stale; shared edits alone leave it current.
func editAgentsLine(t *testing.T, repoPath string) {
	t.Helper()
	agents := filepath.Join(repoPath, agentsFile)
	harness := mustRead(t, agents)
	edited := strings.Replace(harness, "Before concluding any turn:\n", "Before concluding every turn:\n", 1)
	if edited == harness {
		t.Fatal("fixture edit did not apply")
	}
	mustWrite(t, agents, edited+"\n## Claude Code\n- Client policy after the edit.\n")
}

// TestAdopt_Positive_CommittedProjectionIsSynchronizedNotReplaced refreshes a pointer
// with changed client-owned text from HEAD without replacing it or copying shared policy.
func TestAdopt_Positive_CommittedProjectionIsSynchronizedNotReplaced(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-head")
	commitAll(t, repoPath)
	editAgentsLine(t, repoPath)
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	if replaced := rep.Replaced(); len(replaced) != 0 {
		t.Fatalf("projection of the committed AGENTS.md replaced: %+v", replaced)
	}
	if !hasAction(rep, vendorContextFile, actionReconcile) ||
		!strings.Contains(mustRead(t, filepath.Join(repoPath, vendorContextFile)), "- Client policy after the edit.") {
		t.Fatalf("vendor file not synchronized to the edited AGENTS.md: %+v", rep.ActionDetails)
	}
	requireCurrentVendorPointers(t, repoPath)
	if _, err := os.Lstat(filepath.Join(repoPath, filepath.FromSlash(adoptBackupRoot))); !os.IsNotExist(err) {
		t.Fatalf("backup root created without a replace (lstat err=%v)", err)
	}
}

// Negative: a hand edit of CLAUDE.md itself is still a replace with a backup, although HEAD
// holds an AGENTS.md: the file is neither projection.
func TestAdopt_Negative_HandEditOverCommittedProjectionIsReplaced(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-head-edit")
	commitAll(t, repoPath)
	full := filepath.Join(repoPath, vendorContextFile)
	edited := mustRead(t, full) + "Local note an operator added by hand.\n"
	mustWrite(t, full, edited)
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	entry := replacedEntry(t, rep, vendorContextFile)
	if got := mustRead(t, backupNamedIn(t, repoPath, entry.Details)); got != edited {
		t.Fatalf("backup = %q, want the hand-edited file", got)
	}
}

// TestAdopt_Boundary_UnbornHeadFallsBackToReplaceWithBackup preserves the conservative
// backup for a stale client-owned projection when HEAD cannot identify its original bytes.
func TestAdopt_Boundary_UnbornHeadFallsBackToReplaceWithBackup(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-unborn")
	editAgentsLine(t, repoPath)
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	entry := replacedEntry(t, rep, vendorContextFile)
	if !fileExists(backupNamedIn(t, repoPath, entry.Details)) {
		t.Fatalf("no backup of the stale projection: %q", entry.Details)
	}
}
