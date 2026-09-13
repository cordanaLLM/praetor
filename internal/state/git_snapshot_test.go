package state

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestInspectStateNonGitAndCancellation(t *testing.T) {
	dir := t.TempDir()
	snapshot, err := InspectState(t.Context(), dir)
	if err != nil || snapshot.GitState != "not_repository" || snapshot.Clean {
		t.Fatalf("non-Git status must be explicit and cannot be clean: %+v, %v", snapshot, err)
	}
	if _, err := os.Stat(filepath.Join(dir, WorkingDirName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read-only inspection created state files: %v", err)
	}
	var absent context.Context
	if snapshot, err := InspectState(absent, dir); err == nil || snapshot != nil {
		t.Fatalf("missing context must fail: %+v, %v", snapshot, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := InspectState(ctx, dir); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation must propagate: %v", err)
	}
}

func TestSyncStateGitErrorsPreserveHistory(t *testing.T) {
	for _, kind := range []string{"invalid-metadata"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			if err := InitWorkingDir(dir); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, WorkingDirName, "STATE.md")
			const history = "# Preserved history\n"
			writeIntegrityFile(t, path, history)
			if kind == "invalid-metadata" {
				writeIntegrityFile(t, filepath.Join(dir, ".git"), "gitdir: nonexistent\n")
			} else {
				stateFixtureGit(t, dir, "init")
			}
			if snapshot, err := SyncState(t.Context(), dir, "must not append"); err == nil || snapshot != nil {
				t.Fatalf("Git failure must abort sync: %+v, %v", snapshot, err)
			}
			assertIntegrityFile(t, path, history)
		})
	}
}

func TestInspectStateTracksGitWorkingTree(t *testing.T) {
	dir := t.TempDir()
	stateFixtureGit(t, dir, "init")
	stateFixtureGit(t, dir, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	snapshot, err := InspectState(t.Context(), dir)
	if err != nil || snapshot.GitState != "available" || !snapshot.Clean || snapshot.HeadSHA == "" {
		t.Fatalf("clean repository state unavailable: %+v, %v", snapshot, err)
	}
	writeIntegrityFile(t, filepath.Join(dir, "dirty.txt"), "dirty\n")
	snapshot, err = InspectState(t.Context(), dir)
	if err != nil || snapshot.Clean || snapshot.DirtyCount != 1 {
		t.Fatalf("dirty state incorrect: %+v, %v", snapshot, err)
	}
	stateFixtureGit(t, dir, "checkout", "--detach", "HEAD")
	snapshot, err = InspectState(t.Context(), dir)
	if err != nil || snapshot.Branch != "" || snapshot.HeadSHA == "" {
		t.Fatalf("detached HEAD is valid: %+v, %v", snapshot, err)
	}
}

func stateFixtureGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	if output, err := util.RunGit(t.Context(), dir, args...); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
