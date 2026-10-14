package state

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// writeFolderFile writes content to folder/rel under root, creating its directories.
func writeFolderFile(t *testing.T, root, folder, rel, content string) {
	t.Helper()
	path := filepath.Join(root, folder, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, path, content)
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s still exists: %v", path, err)
	}
}

func migrateLegacy(t *testing.T, root string, opts LegacyMigrationOptions) (*LegacyMigrationReport, error) {
	t.Helper()
	return MigrateLegacyWorkingDir(t.Context(), root, opts)
}

const (
	legacyOpen  = "# Open Items\n\n## In-Flight Tasks\n- [ ] legacy one\n- [ ] legacy two\n- [x] legacy done\n"
	currentOpen = "# Open Items & In-Flight Blockers\n\n## In-Flight Tasks\n- [ ] current one\n"
)

// Positive (#1233): a populated legacy folder moves byte for byte, nested evidence included,
// and the folder is gone afterwards.
func TestMigrateLegacyWorkingDir_Positive_MovesContentAndRemovesFolder(t *testing.T) {
	root := t.TempDir()
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	writeFolderFile(t, root, LegacyWorkingDirName, "evidence/run/log.txt", "evidence\n")
	writeFolderFile(t, root, LegacyWorkingDirName, "notes.md", "notes\n")
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true})
	if err != nil || !report.Applied || !report.LegacyRemoved {
		t.Fatalf("migration failed: %+v, %v", report, err)
	}
	if want := []string{"OPEN.md", "evidence/run/log.txt", "notes.md"}; !slices.Equal(report.Moved, want) {
		t.Fatalf("moved %q, want %q", report.Moved, want)
	}
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), legacyOpen)
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "evidence", "run", "log.txt"), "evidence\n")
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "notes.md"), "notes\n")
	assertAbsent(t, filepath.Join(root, LegacyWorkingDirName))
	tasks, err := ListTasks(root)
	if err != nil || len(tasks) != 3 {
		t.Fatalf("migrated ledger reads %d tasks, %v", len(tasks), err)
	}
}

// Positive: --merge appends a legacy OPEN.md to a populated one, keeps every row of both and
// reports the rows the merged file shows.
func TestMigrateLegacyWorkingDir_Positive_MergeAppendsPopulatedLedger(t *testing.T) {
	root := t.TempDir()
	writeFolderFile(t, root, WorkingDirName, "OPEN.md", currentOpen)
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true, Merge: true})
	if err != nil || len(report.Merged) != 1 {
		t.Fatalf("merge failed: %+v, %v", report, err)
	}
	if got := report.Merged[0]; got.Mode != "appended" || got.OpenTasks != 3 || got.CompletedTasks != 1 {
		t.Fatalf("merge reported %+v, want appended with 3 open and 1 completed", got)
	}
	data, err := os.ReadFile(filepath.Join(root, WorkingDirName, "OPEN.md"))
	if err != nil || !strings.HasPrefix(string(data), currentOpen) || !strings.HasSuffix(string(data), legacyOpen) {
		t.Fatalf("merged ledger lost rows: %q, %v", data, err)
	}
	assertAbsent(t, filepath.Join(root, LegacyWorkingDirName))
}

// Positive: a registered worktree inside the legacy folder moves with git worktree move and
// still resolves after the move.
func TestMigrateLegacyWorkingDir_Positive_MovesRegisteredWorktree(t *testing.T) {
	root := t.TempDir()
	stateFixtureGit(t, root, "init")
	stateFixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	stateFixtureGit(t, root, "worktree", "add", "-b", "feature", filepath.Join(root, LegacyWorkingDirName, "wt", "feature"))
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true})
	if err != nil || !slices.Equal(report.Worktrees, []string{"wt/feature"}) || !report.LegacyRemoved {
		t.Fatalf("worktree migration failed: %+v, %v", report, err)
	}
	moved := filepath.Join(root, WorkingDirName, "wt", "feature")
	if branch := stateFixtureGit(t, moved, "rev-parse", "--abbrev-ref", "HEAD"); branch != "feature" {
		t.Fatalf("moved worktree resolves to %q", branch)
	}
	if listed := stateFixtureGit(t, root, "worktree", "list", "--porcelain"); strings.Contains(listed, LegacyWorkingDirName) {
		t.Fatalf("git still registers the legacy path:\n%s", listed)
	}
}

// Negative: a same-name ledger that differs is refused unless a merge is asked for, and the
// refusal writes nothing, in the dry run and with --apply alike.
func TestMigrateLegacyWorkingDir_Negative_ConflictRefusedWithoutMerge(t *testing.T) {
	root := t.TempDir()
	writeFolderFile(t, root, WorkingDirName, "OPEN.md", currentOpen)
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	writeFolderFile(t, root, LegacyWorkingDirName, "evidence/a.txt", "a\n")
	for _, apply := range []bool{false, true} {
		report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: apply})
		if !errors.Is(err, ErrLegacyMigrationConflict) || report.Applied || len(report.Conflicts) != 1 {
			t.Fatalf("apply=%v: conflict not refused: %+v, %v", apply, report, err)
		}
		if !strings.Contains(report.Conflicts[0], "--merge") {
			t.Fatalf("conflict does not name --merge: %q", report.Conflicts[0])
		}
	}
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), currentOpen)
	assertIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName, "OPEN.md"), legacyOpen)
	assertIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName, "evidence", "a.txt"), "a\n")
	assertAbsent(t, filepath.Join(root, WorkingDirName, "evidence", "a.txt"))
}

// Negative: --merge never merges a non-ledger file or a populated ledger with IDs or a sync
// marker of its own; both stay conflicts and nothing is overwritten.
func TestMigrateLegacyWorkingDir_Negative_MergeRefusesUnmergeableFiles(t *testing.T) {
	root := t.TempDir()
	writeFolderFile(t, root, WorkingDirName, "STATE.md", "current history\n")
	writeFolderFile(t, root, LegacyWorkingDirName, "STATE.md", "legacy history\n")
	writeFolderFile(t, root, WorkingDirName, "evidence/x.txt", "new\n")
	writeFolderFile(t, root, LegacyWorkingDirName, "evidence/x.txt", "old\n")
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true, Merge: true})
	if !errors.Is(err, ErrLegacyMigrationConflict) || len(report.Conflicts) != 2 || len(report.Merged) != 0 {
		t.Fatalf("unmergeable files not refused: %+v, %v", report, err)
	}
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "STATE.md"), "current history\n")
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "evidence", "x.txt"), "new\n")
	assertIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName, "STATE.md"), "legacy history\n")
}

// Negative: a repository with only .workingdir is a no-op that creates nothing; a dry run
// over a legacy folder writes nothing either.
func TestMigrateLegacyWorkingDir_Negative_NoLegacyFolderAndDryRunWriteNothing(t *testing.T) {
	root := t.TempDir()
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true, Merge: true})
	if err != nil || report.LegacyPresent || report.Applied {
		t.Fatalf("no-op migration reported %+v, %v", report, err)
	}
	assertAbsent(t, filepath.Join(root, WorkingDirName))
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	report, err = migrateLegacy(t, root, LegacyMigrationOptions{})
	if err != nil || report.Applied || !slices.Equal(report.Moved, []string{"OPEN.md"}) {
		t.Fatalf("dry run reported %+v, %v", report, err)
	}
	assertAbsent(t, filepath.Join(root, WorkingDirName))
	assertIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName, "OPEN.md"), legacyOpen)
}

// Boundary: an empty legacy folder is removed with nothing moved, and a second run over the
// migrated repository is a no-op.
func TestMigrateLegacyWorkingDir_Boundary_EmptyFolderAndSecondRun(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, LegacyWorkingDirName, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true})
	if err != nil || !report.LegacyRemoved || len(report.Moved) != 0 {
		t.Fatalf("empty folder migration: %+v, %v", report, err)
	}
	assertAbsent(t, filepath.Join(root, LegacyWorkingDirName))
	report, err = migrateLegacy(t, root, LegacyMigrationOptions{Apply: true})
	if err != nil || report.LegacyPresent {
		t.Fatalf("second run is not a no-op: %+v, %v", report, err)
	}
}

// Boundary: a legacy entry that is a file, not a folder, is refused and left untouched.
func TestMigrateLegacyWorkingDir_Boundary_LegacyFileRefused(t *testing.T) {
	root := t.TempDir()
	writeIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName), "not a folder\n")
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true})
	if err == nil || report.LegacyPresent {
		t.Fatalf("legacy file accepted: %+v, %v", report, err)
	}
	assertIntegrityFile(t, filepath.Join(root, LegacyWorkingDirName), "not a folder\n")
	assertAbsent(t, filepath.Join(root, WorkingDirName))
}

// Boundary: a file identical in both folders drops only its legacy copy, and a ledger that
// still holds its empty template takes the legacy ledger under --merge.
func TestMigrateLegacyWorkingDir_Boundary_DuplicateAndTemplateLedger(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeFolderFile(t, root, WorkingDirName, "evidence/same.txt", "same\n")
	writeFolderFile(t, root, LegacyWorkingDirName, "evidence/same.txt", "same\n")
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	report, err := migrateLegacy(t, root, LegacyMigrationOptions{Apply: true, Merge: true})
	if err != nil || !slices.Equal(report.Duplicates, []string{"evidence/same.txt"}) || len(report.Merged) != 1 {
		t.Fatalf("duplicate/template migration: %+v, %v", report, err)
	}
	if got := report.Merged[0]; got.Mode != "replaced-template" || got.OpenTasks != 2 || got.CompletedTasks != 1 {
		t.Fatalf("template merge reported %+v", got)
	}
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), legacyOpen)
	assertIntegrityFile(t, filepath.Join(root, WorkingDirName, "evidence", "same.txt"), "same\n")
	assertAbsent(t, filepath.Join(root, LegacyWorkingDirName))
}

// Negative (#1233): state audit used to pass while the legacy folder held the real ledger.
// It now reports the split and names both OPEN.md files.
func TestAuditWorkingDir_Negative_ReportsPopulatedLegacyFolder(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeFolderFile(t, root, LegacyWorkingDirName, "OPEN.md", legacyOpen)
	report, err := AuditWorkingDir(root)
	if err != nil || report.Valid || len(report.Violations) != 1 {
		t.Fatalf("split ledger passed the audit: %+v, %v", report, err)
	}
	finding := report.Violations[0]
	for _, want := range []string{".workingdir/OPEN.md 131 bytes, 0 open", ".workingdir2/OPEN.md", "2 open of 3 rows", "state migrate"} {
		if !strings.Contains(finding, want) {
			t.Fatalf("finding %q does not name %q", finding, want)
		}
	}
}

// Boundary: an empty legacy folder or none at all is not a finding.
func TestAuditWorkingDir_Boundary_EmptyOrAbsentLegacyFolder(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	for _, setup := range []func(){func() {}, func() { _ = os.Mkdir(filepath.Join(root, LegacyWorkingDirName), 0o700) }} {
		setup()
		report, err := AuditWorkingDir(root)
		if err != nil || !report.Valid {
			t.Fatalf("audit flagged a repository without legacy content: %+v, %v", report, err)
		}
	}
}
