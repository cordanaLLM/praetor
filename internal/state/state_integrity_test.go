package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestStateIntegrityPositivePreservesHistoryAndNonGitRoot(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, WorkingDirName, "STATE.md")
	const history = "# Human history\nRetain this exact text.\n"
	writeIntegrityFile(t, path, history)
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	assertIntegrityFile(t, path, history)
	snap, err := SyncState(context.Background(), root, "new activity")
	if err != nil || snap == nil {
		t.Fatalf("non-Git sync failed: snapshot=%v error=%v", snap, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(data), history) || !strings.Contains(string(data), "new activity") {
		t.Fatalf("history or appended activity missing: %q", data)
	}
	report, err := AuditWorkingDir(root)
	if err != nil || !report.Valid {
		t.Fatalf("valid ledger audit failed: report=%+v error=%v", report, err)
	}
}

func TestStateIntegrityRejectsInvalidExistingLedgerPaths(t *testing.T) {
	for _, name := range []string{"STATE.md", "OPEN.md", "BACKLOG.md", "BUGS.md", "QUESTIONS.md"} {
		for _, kind := range []string{"directory", "loop", "dangling"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				root := t.TempDir()
				if err := InitWorkingDir(root); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(root, WorkingDirName, name)
				makeInvalidIntegrityPath(t, path, kind)
				if err := InitWorkingDir(root); err == nil {
					t.Fatal("initialization accepted an invalid existing ledger")
				}
				if _, err := SyncState(context.Background(), root, "must not append"); err == nil {
					t.Fatal("sync accepted an invalid existing ledger")
				}
				report, err := AuditWorkingDir(root)
				if err == nil || report == nil || report.Valid {
					t.Fatalf("invalid ledger audit did not fail: report=%+v error=%v", report, err)
				}
				assertInvalidIntegrityPath(t, path, kind)
			})
		}
	}
}

func TestStateIntegrityAppendRejectsUnreadableOrMissingHistory(t *testing.T) {
	for _, kind := range []string{"missing", "directory", "loop", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, WorkingDirName, "STATE.md")
			makeInvalidIntegrityPath(t, path, kind)
			if err := appendStateLog(context.Background(), root, &StateSnapshot{}, "must not replace"); err == nil {
				t.Fatal("append accepted an unreadable or absent history")
			}
			assertInvalidIntegrityPath(t, path, kind)
		})
	}
}

func TestStateIntegrityTaskReadFailureLeavesHistoryIntact(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, WorkingDirName, "STATE.md")
	const history = "# Preserve history\n"
	writeIntegrityFile(t, path, history)
	writeIntegrityFile(t, filepath.Join(root, WorkingDirName, "OPEN.md"), strings.Repeat("x", 1<<16))
	_, err := SyncState(context.Background(), root, "must not append")
	if err == nil || !strings.Contains(err.Error(), "list tasks") {
		t.Fatalf("task reader failure not propagated: %v", err)
	}
	assertIntegrityFile(t, path, history)
}

func TestStateIntegrityReadersRejectInvalidPaths(t *testing.T) {
	for _, kind := range []string{"directory", "loop", "dangling"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			if err := InitWorkingDir(root); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"OPEN.md", "QUESTIONS.md"} {
				makeInvalidIntegrityPath(t, filepath.Join(root, WorkingDirName, name), kind)
			}
			if _, err := ListTasks(root); err == nil {
				t.Fatal("task reader reported invalid path as empty")
			}
			if _, err := ListQuestions(root, "all"); err == nil {
				t.Fatal("question reader reported invalid path as empty")
			}
		})
	}
}

func TestStateIntegrityReadAndAppendByteBoundaries(t *testing.T) {
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, WorkingDirName, "QUESTIONS.md")
	boundary := strings.Repeat("x", contextopt.MaxSourceBytes)
	writeIntegrityFile(t, path, boundary)
	if _, err := ListQuestions(root, "all"); err != nil {
		t.Fatalf("exact byte boundary rejected: %v", err)
	}
	writeIntegrityFile(t, path, boundary+"x")
	report, err := AuditWorkingDir(root)
	if err == nil || report.Valid {
		t.Fatalf("oversized question ledger audited successfully: %+v, %v", report, err)
	}
	statePath := filepath.Join(root, WorkingDirName, "STATE.md")
	const history = "# Unchanged state\n"
	writeIntegrityFile(t, statePath, history)
	if _, err := SyncState(context.Background(), root, "must not append"); err == nil {
		t.Fatal("sync ignored oversized question ledger")
	}
	assertIntegrityFile(t, statePath, history)
	writeIntegrityFile(t, statePath, boundary)
	if err := appendStateLog(context.Background(), root, &StateSnapshot{}, "overflow"); err == nil {
		t.Fatal("append wrote a history too large for the next snapshot")
	}
	assertIntegrityFile(t, statePath, boundary)
}

func TestStateIntegrityInitRejectsInvalidWorkingDirectory(t *testing.T) {
	for _, kind := range []string{"file", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			outside := t.TempDir()
			path := filepath.Join(root, WorkingDirName)
			if kind == "file" {
				writeIntegrityFile(t, path, "preserve")
			} else if err := os.Symlink(outside, path); err != nil {
				t.Fatal(err)
			}
			if err := InitWorkingDir(root); err == nil {
				t.Fatal("initialization accepted invalid working directory")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || len(entries) != 0 {
				t.Fatalf("initialization wrote through directory symlink: %v, %v", entries, err)
			}
			if kind == "file" {
				assertIntegrityFile(t, path, "preserve")
			}
		})
	}
}

func TestStateIntegrityRootSymlinkFailsBeforeInitialization(t *testing.T) {
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-project")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	// The project root itself must be a real directory. Accepting a symlink here would let the
	// ledger be written somewhere other than the repository the operator named.
	if err := InitWorkingDir(link); err == nil {
		t.Fatal("initialization accepted a root symlink")
	}
	if _, err := AddBug(link, BugEntry{Title: "must not initialize"}); err == nil {
		t.Fatal("bug mutation accepted a root symlink")
	}
	if _, err := SyncState(context.Background(), link, "must not initialize"); err == nil {
		t.Fatal("sync accepted a root symlink")
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatalf("rejected root wrote to symlink destination: %v, %v", entries, err)
	}
}

// A real project reached through a symlinked ancestor initializes. This assertion is the inverse
// of what it used to be: macOS ships /var and /tmp as symlinks, so refusing a symlinked ancestor
// refused every project under the platform's own temporary directory (#109). The root itself is
// still required to be a real directory, which is the check that actually protects the ledger.
func TestStateIntegrityAcceptsAProjectBehindASymlinkedAncestor(t *testing.T) {
	outside := t.TempDir()
	link := filepath.Join(t.TempDir(), "linked-parent")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	project := filepath.Join(outside, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := InitWorkingDir(filepath.Join(link, "project")); err != nil {
		t.Fatalf("a real project behind a symlinked ancestor must initialize: %v", err)
	}
	if _, err := os.Stat(filepath.Join(project, WorkingDirName)); err != nil {
		t.Fatalf("the ledger must land in the real project directory: %v", err)
	}
}

func TestStateIntegrityReadersRejectUnusableContexts(t *testing.T) {
	for _, kind := range []string{"cancelled", "nil"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			var ctx context.Context
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			if _, err := ListTasksContext(ctx, root); err == nil {
				t.Fatal("task reader accepted unusable context")
			}
			if _, err := ListQuestionsContext(ctx, root, "all"); err == nil {
				t.Fatal("question reader accepted unusable context")
			}
		})
	}
}

func TestStateIntegrityCancelledOrNilContextDoesNotInitialize(t *testing.T) {
	for _, kind := range []string{"cancelled", "nil"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			var ctx context.Context
			if kind == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(context.Background())
				cancel()
			}
			if _, err := SyncState(ctx, root, "must not initialize"); err == nil {
				t.Fatal("sync accepted unusable context")
			}
			if _, err := os.Lstat(filepath.Join(root, WorkingDirName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("sync created state before validating context: %v", err)
			}
		})
	}
}

func makeInvalidIntegrityPath(t *testing.T, path, kind string) {
	t.Helper()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if kind == "missing" {
		return
	}
	if kind == "directory" {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		return
	}
	target := filepath.Base(path)
	if kind == "dangling" {
		target += ".absent"
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
}

func assertInvalidIntegrityPath(t *testing.T, path, kind string) {
	t.Helper()
	info, err := os.Lstat(path)
	if kind == "missing" {
		if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("absent history was replaced: %v", err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	if kind == "directory" {
		if !info.IsDir() {
			t.Fatal("existing directory was replaced")
		}
		return
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatal("existing symlink was replaced")
	}
	if kind == "dangling" {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("dangling target was created: %v", err)
		}
	}
}

func writeIntegrityFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func assertIntegrityFile(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != expected {
		t.Fatalf("history changed: got %q, want %q", data, expected)
	}
}
