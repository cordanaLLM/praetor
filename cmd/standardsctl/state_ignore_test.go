package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// stateInitModes are the two standalone initializations; both write private files.
var stateInitModes = map[string][]string{
	"init":           {"init"},
	"init-if-absent": {"init", "--if-absent"},
}

// gitRepoDir is an empty Git work tree with no .gitignore.
func gitRepoDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := util.RunGit(t.Context(), dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	return dir
}

func runStateInitMode(t *testing.T, mode []string, dir string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error {
		return dispatchCommand("state", append(append([]string{}, mode...), dir))
	})
}

// Positive: standalone `state init` in a repository that does not ignore the ledger adds
// the canonical private-artifact block, so Git excludes the files it just wrote; a second
// run finds the rule effective and leaves .gitignore alone.
func TestStateInitIgnoresTheLedgerItWrites(t *testing.T) {
	for name, mode := range stateInitModes {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			out, err := runStateInitMode(t, mode, dir)
			if err != nil {
				t.Fatalf("state %s: %v", strings.Join(mode, " "), err)
			}
			if !strings.Contains(out, "Added the Praetor private-artifact block to .gitignore") {
				t.Fatalf("written ignore block not reported: %q", out)
			}
			ignore := filepath.Join(dir, ".gitignore")
			got, err := os.ReadFile(ignore)
			if err != nil || string(got) != adopt.ManagedGitIgnoreBlock() {
				t.Fatalf(".gitignore = %q, %v; want the managed block alone", got, err)
			}
			if _, err := util.RunGit(t.Context(), dir, "check-ignore", "--no-index", "--", ".workingdir/STATE.md"); err != nil {
				t.Fatalf("seeded ledger is not ignored: %v", err)
			}
			out, err = runStateInitMode(t, mode, dir)
			if err != nil || strings.Contains(out, "Added the Praetor") {
				t.Fatalf("second run: %q, %v; want no ignore write", out, err)
			}
			if again := readFixtureFile(t, dir, ".gitignore"); again != string(got) {
				t.Fatalf("second run rewrote .gitignore: %q", again)
			}
		})
	}
}

// Negative: an operator .gitignore that cannot be merged safely fails the command instead
// of reporting an initialized ledger Git would publish, and is left as it was.
func TestStateInitRefusesAnUnmergeableGitIgnore(t *testing.T) {
	for name, mode := range stateInitModes {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			broken := "# BEGIN praetor private artifacts (praetorctl adopt)\n/.workingdir2/\n"
			writeFixtureFile(t, dir, ".gitignore", broken)
			if _, err := runStateInitMode(t, mode, dir); err == nil || !strings.Contains(err.Error(), "could not make Git ignore") {
				t.Fatalf("unmergeable .gitignore accepted: %v", err)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != broken {
				t.Fatalf("refused run changed .gitignore: %q", got)
			}
		})
	}
}

// Boundary: outside any Git work tree nothing can publish the ledger, so no .gitignore is
// created; an existing rule in any spelling that already excludes the directory is kept.
func TestStateInitIgnoreBoundaries(t *testing.T) {
	for name, mode := range stateInitModes {
		t.Run(name+"/outside-repository", func(t *testing.T) {
			dir := t.TempDir()
			if present, err := util.GitWorktreePresent(t.Context(), dir); err != nil || present {
				t.Skipf("temporary directory sits inside a Git work tree (%v, %v)", present, err)
			}
			if _, err := runStateInitMode(t, mode, dir); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
				t.Fatalf("state init created .gitignore outside a repository: %v", err)
			}
		})
		t.Run(name+"/existing-rule", func(t *testing.T) {
			dir := gitRepoDir(t)
			writeFixtureFile(t, dir, ".gitignore", "/.workingdir/\n")
			if out, err := runStateInitMode(t, mode, dir); err != nil || strings.Contains(out, "Added the Praetor") {
				t.Fatalf("effective rule not recognised: %q, %v", out, err)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != "/.workingdir/\n" {
				t.Fatalf("effective .gitignore rewritten: %q", got)
			}
		})
	}
}
