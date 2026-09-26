package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestVerifyAll_Positive_PreservesTree(t *testing.T) {
	dir := t.TempDir()
	if _, err := util.RunGit(context.Background(), dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(context.Background(), dir, "commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	// Create a .workingdir with no uncommitted changes
	if err := os.Mkdir(filepath.Join(dir, ".workingdir"), 0755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CI", "true")
	out, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err != nil {
		t.Fatalf("expected success, got error: %v (out: %s)", err, out)
	}
}

func TestVerifyAll_Negative_AbortsOnUncommittedState(t *testing.T) {
	dir := t.TempDir()
	if _, err := util.RunGit(context.Background(), dir, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(context.Background(), dir, "commit", "--allow-empty", "-m", "init"); err != nil {
		t.Fatal(err)
	}

	// Create tracked file, commit it, then modify it to make tree dirty for git diff --quiet
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("foo"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(context.Background(), dir, "add", "tracked.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(context.Background(), dir, "commit", "-m", "add tracked"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("bar"), 0644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("CI", "true")
	_, err := captureStdout(t, func() error {
		return runVerifyAll([]string{dir})
	})
	if err == nil {
		t.Fatal("expected error due to dirty tree in CI, got nil")
	}
	mustErrContain(t, err, "verify-all modified working tree")
}
