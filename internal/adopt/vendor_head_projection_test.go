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

// editAgentsLine rewrites one line of the adopted AGENTS.md without running compile-context, so
// the vendor files stay the projection of the text before the edit.
func editAgentsLine(t *testing.T, repoPath string) {
	t.Helper()
	agents := filepath.Join(repoPath, agentsFile)
	harness := mustRead(t, agents)
	edited := strings.Replace(harness, "Before concluding any turn:\n", "Before concluding every turn:\n", 1)
	if edited == harness {
		t.Fatal("fixture edit did not apply")
	}
	mustWrite(t, agents, edited)
}

// Positive: after AGENTS.md is edited past its committed text, a plain re-adoption synchronizes
// CLAUDE.md, still the projection of the committed AGENTS.md, to the edited one: no replace,
// no backup.
func TestAdopt_Positive_CommittedProjectionIsSynchronizedNotReplaced(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-head")
	commitAll(t, repoPath)
	editAgentsLine(t, repoPath)
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	if replaced := rep.Replaced(); len(replaced) != 0 {
		t.Fatalf("projection of the committed AGENTS.md replaced: %+v", replaced)
	}
	if !hasAction(rep, vendorContextFile, actionReconcile) ||
		!strings.Contains(mustRead(t, filepath.Join(repoPath, vendorContextFile)), "Before concluding every turn:") {
		t.Fatalf("vendor file not synchronized to the edited AGENTS.md: %+v", rep.ActionDetails)
	}
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

// Boundary: without a commit there is no HEAD text, so the same AGENTS.md edit leaves CLAUDE.md
// matching no projection; the run still succeeds and takes the conservative reading, a replace
// with a backup.
func TestAdopt_Boundary_UnbornHeadFallsBackToReplaceWithBackup(t *testing.T) {
	repoPath, source := adoptedRepo(t, "vendor-unborn")
	editAgentsLine(t, repoPath)
	rep := readoptWith(t, repoPath, source, AdoptOptions{})
	entry := replacedEntry(t, rep, vendorContextFile)
	if !fileExists(backupNamedIn(t, repoPath, entry.Details)) {
		t.Fatalf("no backup of the stale projection: %q", entry.Details)
	}
}
