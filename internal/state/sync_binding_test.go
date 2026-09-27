package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func syncFixture(t *testing.T) string {
	t.Helper()
	return syncFixtureAt(t, t.TempDir())
}

// syncFixtureAt builds the fixture repository at a caller-chosen root, so a case can put it
// under a directory it controls -- an aliased ancestor, for instance -- instead of directly
// under the test's own temporary directory.
func syncFixtureAt(t *testing.T, root string) string {
	t.Helper()
	stateFixtureGit(t, root, "init", "-b", "main")
	writeIntegrityFile(t, filepath.Join(root, ".gitignore"), "/.workingdir/\n")
	writeIntegrityFile(t, filepath.Join(root, "tracked.txt"), "initial\n")
	stateFixtureGit(t, root, "add", ".")
	stateFixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-m", "fixture")
	if _, err := SyncState(t.Context(), root, "fixture"); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestVerifyStateSyncReadOnlyAndPrivateEvidenceExcluded(t *testing.T) {
	root := syncFixture(t)
	path := filepath.Join(root, WorkingDirName, "STATE.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "evidence", "ignored.bin"), strings.Repeat("x", contextopt.MaxSourceBytes+1))
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	assertIntegrityFile(t, path, string(before))
	if !strings.Contains(string(before), " | git available clean\n") {
		t.Fatal("sync did not persist working tree observation")
	}
}

func TestVerifyStateSyncRejectsChangedInputs(t *testing.T) {
	for _, kind := range []string{"tracked", "staged", "untracked", "untracked-same-size", "ledger", "history", "branch", "head"} {
		t.Run(kind, func(t *testing.T) {
			root := syncFixture(t)
			if kind == "untracked-same-size" {
				writeIntegrityFile(t, filepath.Join(root, "new.bin"), "\x00one")
				if _, err := SyncState(t.Context(), root, "binary input"); err != nil {
					t.Fatal(err)
				}
			}
			changeSyncInput(t, root, kind)
			if err := VerifyStateSync(t.Context(), root); err == nil {
				t.Fatalf("changed %s input accepted", kind)
			}
			if _, err := SyncState(t.Context(), root, "explicit reconciliation"); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestStateSyncIgnoresAutocrlfIndexRefreshNoise(t *testing.T) {
	root := t.TempDir()
	stateFixtureGit(t, root, "init", "-b", "main")
	writeIntegrityFile(t, filepath.Join(root, ".gitignore"), "/.workingdir/\r\n")
	tracked := filepath.Join(root, "tracked.txt")
	writeIntegrityFile(t, tracked, "initial\r\n")
	stateFixtureGit(t, root, "-c", "core.autocrlf=true", "add", ".")
	stateFixtureGit(t, root, "-c", "core.autocrlf=true", "-c", "user.name=Test",
		"-c", "user.email=test@example.invalid", "commit", "-m", "CRLF fixture")

	// Make the worktree observation non-racy after Git cached the checkout produced under
	// autocrlf=true. The bytes stay unchanged; only the stat metadata is invalidated.
	stableTime := time.Unix(1_700_000_000, 0)
	if err := os.Chtimes(tracked, stableTime, stableTime); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncState(t.Context(), root, "CRLF checkout"); err != nil {
		t.Fatal(err)
	}

	// Git may refresh the live index through the operator's core.autocrlf setting between
	// sync and Stop. That stat-cache write must not change the state binding when neither
	// the index nor the worktree bytes changed.
	stateFixtureGit(t, root, "-c", "core.autocrlf=true", "update-index", "--really-refresh")
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("an autocrlf index refresh made unchanged state stale: %v", err)
	}

	// Content still binds after line-ending normalization; only CRLF-vs-LF noise is ignored.
	writeIntegrityFile(t, tracked, "changed\r\n")
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("changed tracked content survived the normalized state binding")
	}
}

func changeSyncInput(t *testing.T, root, kind string) {
	t.Helper()
	switch kind {
	case "tracked", "staged":
		writeIntegrityFile(t, filepath.Join(root, "tracked.txt"), "changed\n")
		if kind == "staged" {
			stateFixtureGit(t, root, "add", "tracked.txt")
			writeIntegrityFile(t, filepath.Join(root, "tracked.txt"), "initial\n")
		}
	case "untracked", "untracked-same-size":
		writeIntegrityFile(t, filepath.Join(root, "new.bin"), "\x00two")
	case "ledger":
		if err := AddTask(root, "maintain the ledger"); err != nil {
			t.Fatal(err)
		}
	case "history":
		writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "STATE.md"), "edited history\n")
	case "branch":
		stateFixtureGit(t, root, "checkout", "-b", "other")
	case "head":
		stateFixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "new head")
	}
}

func TestStateSyncMissingLedgerDoesNotRepairExistingDirectory(t *testing.T) {
	root := syncFixture(t)
	path := filepath.Join(root, WorkingDirName, "BACKLOG.md")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, err := SyncState(t.Context(), root, "must not repair"); err == nil {
		t.Fatal("sync silently repaired an incomplete existing ledger")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("missing ledger was recreated: %v", err)
	}
}

func TestVerifyStateSyncRejectsAbsentCancelledAndMovedRoots(t *testing.T) {
	if err := VerifyStateSync(t.Context(), t.TempDir()); err == nil {
		t.Fatal("absent state accepted")
	}
	root := syncFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	for _, ctx := range []context.Context{ctx, nil} {
		if err := VerifyStateSync(ctx, root); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	moved := filepath.Join(t.TempDir(), "moved")
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), moved); err == nil {
		t.Fatal("state binding accepted another worktree root")
	}
}

func TestStateSyncUntrackedByteAndPathBounds(t *testing.T) {
	root := syncFixture(t)
	path := filepath.Join(root, "binary.bin")
	writeIntegrityFile(t, path, strings.Repeat("\x00", contextopt.MaxSourceBytes))
	if _, err := SyncState(t.Context(), root, "exact boundary"); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, path, strings.Repeat("\x00", contextopt.MaxSourceBytes+1))
	if _, err := SyncState(t.Context(), root, "oversized"); err == nil {
		t.Fatal("oversized untracked file accepted")
	}
	if _, err := stateUntrackedBinding(t.Context(), root, strings.Repeat("name\x00", maxSyncPaths+1)); err == nil {
		t.Fatal("excessive untracked path count accepted")
	}
}

func TestStateSyncRejectsConfiguredFilterBeforeExecution(t *testing.T) {
	root := syncFixture(t)
	marker := filepath.Join(root, "filter-executed")
	writeIntegrityFile(t, filepath.Join(root, ".gitattributes"), "tracked.txt filter=probe\n")
	stateFixtureGit(t, root, "config", "filter.probe.clean", "touch "+marker)
	if _, err := SyncState(t.Context(), root, "must not run filter"); err == nil {
		t.Fatal("configured filter accepted")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("Git filter executed: %v", err)
	}
}

func TestStateSyncUnbornInitialCommitTransition(t *testing.T) {
	root := t.TempDir()
	stateFixtureGit(t, root, "init", "-b", "main")
	snap, err := SyncState(t.Context(), root, "before first commit")
	if err != nil || snap.GitState != "unborn" || snap.HeadSHA != "(unborn)" {
		t.Fatalf("unborn state unavailable: %+v, %v", snap, err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatal(err)
	}
	stateFixtureGit(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "initial")
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("first commit did not invalidate unborn state")
	}
	if _, err := SyncState(t.Context(), root, "after first commit"); err != nil {
		t.Fatal(err)
	}
}

func TestStateSyncRejectsHiddenIndexFlagsAndSubmodules(t *testing.T) {
	for _, flag := range []string{"--assume-unchanged", "--skip-worktree"} {
		t.Run(flag, func(t *testing.T) {
			root := syncFixture(t)
			stateFixtureGit(t, root, "update-index", flag, "tracked.txt")
			writeIntegrityFile(t, filepath.Join(root, "tracked.txt"), "hidden modification\n")
			if err := VerifyStateSync(t.Context(), root); err == nil {
				t.Fatal("hidden index modification accepted")
			}
			if _, err := SyncState(t.Context(), root, "cannot observe fully"); err == nil {
				t.Fatal("sync certified unsupported hidden index entry")
			}
		})
	}
	root := syncFixture(t)
	head, err := stateGitHead(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	stateFixtureGit(t, root, "update-index", "--add", "--cacheinfo", "160000,"+head+",module")
	if _, err := SyncState(t.Context(), root, "nested worktree unsupported"); err == nil {
		t.Fatal("sync certified unobserved submodule")
	}
}
