package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/state"
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

// ledgerCreator is a command other than `state init` that seeds the private ledger on
// first use. failing marks one that seeds it and then fails: `bug resolve` of an unknown
// ID creates the ledger before it finds nothing to resolve.
type ledgerCreator struct {
	command string
	args    func(dir string) []string
	failing bool
}

var ledgerCreators = map[string]ledgerCreator{
	"state sync":         {"state", func(d string) []string { return []string{"sync", d} }, false},
	"state task add":     {"state", func(d string) []string { return []string{"task", "add", "first task", "--dir=" + d} }, false},
	"state bug add":      {"state", func(d string) []string { return []string{"bug", "add", "--title=first bug", "--dir=" + d} }, false},
	"state bug resolve":  {"state", func(d string) []string { return []string{"bug", "resolve", "BUG-001", "fixed", "--dir=" + d} }, true},
	"state question add": {"state", func(d string) []string { return []string{"question", "add", "--prompt=first question", "--dir=" + d} }, false},
	"flavor apply":       {"flavor", func(d string) []string { return []string{"apply", "--flavor=infra-k8s", d} }, false},
}

func runLedgerCreator(t *testing.T, creator ledgerCreator, dir string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand(creator.command, creator.args(dir)) })
}

// checkCreatorResult fails the test when a creator's error does not match its contract.
func checkCreatorResult(t *testing.T, creator ledgerCreator, err error) {
	t.Helper()
	if (err != nil) != creator.failing {
		t.Fatalf("%s %v: error = %v, want failure %v", creator.command, creator.args("<dir>"), err, creator.failing)
	}
}

// Positive: every command that seeds the ledger on first use leaves Git ignoring it, the
// failing `bug resolve` included, through the same block standalone `state init` writes.
func TestLedgerCreatorsIgnoreTheLedgerTheySeed(t *testing.T) {
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			out, err := runLedgerCreator(t, creator, dir)
			checkCreatorResult(t, creator, err)
			if !strings.Contains(out, "Added the Praetor private-artifact block to .gitignore") {
				t.Fatalf("written ignore block not reported: %q", out)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
				t.Fatalf(".gitignore = %q; want the managed block alone", got)
			}
			if _, err := util.RunGit(t.Context(), dir, "check-ignore", "--no-index", "--", ".workingdir/STATE.md"); err != nil {
				t.Fatalf("seeded ledger is not ignored: %v", err)
			}
		})
	}
}

// Positive: the first `state sync` writes .gitignore before it records the working tree,
// so the snapshot it records is current and the commit-msg hook's verify accepts it.
func TestFirstStateSyncRecordsTheIgnoreItWrites(t *testing.T) {
	dir := gitRepoDir(t)
	if _, err := runStateInitMode(t, []string{"sync"}, dir); err != nil {
		t.Fatalf("state sync: %v", err)
	}
	if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
		t.Fatalf(".gitignore = %q; want the managed block alone", got)
	}
	if err := dispatchCommand("state", []string{"sync", "--verify", dir}); err != nil {
		t.Fatalf("snapshot recorded by the first sync is stale: %v", err)
	}
}

// Negative: a .gitignore that cannot be merged fails every creator, instead of reporting
// success over a ledger Git would publish, and is left as it was.
func TestLedgerCreatorsRefuseAnUnmergeableGitIgnore(t *testing.T) {
	broken := "# BEGIN praetor private artifacts (praetorctl adopt)\n/.workingdir2/\n"
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			writeFixtureFile(t, dir, ".gitignore", broken)
			if _, err := runLedgerCreator(t, creator, dir); err == nil || !strings.Contains(err.Error(), "could not make Git ignore") {
				t.Fatalf("unmergeable .gitignore accepted: %v", err)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != broken {
				t.Fatalf("refused run changed .gitignore: %q", got)
			}
		})
	}
}

// Boundary: a ledger that existed before the command ran is not its doing, so no creator
// writes .gitignore for it; `state init` is what reconciles an existing ledger.
func TestLedgerCreatorsLeaveAnExistingLedgerToStateInit(t *testing.T) {
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			if err := state.InitWorkingDir(dir); err != nil {
				t.Fatal(err)
			}
			out, err := runLedgerCreator(t, creator, dir)
			checkCreatorResult(t, creator, err)
			if strings.Contains(out, "Added the Praetor") {
				t.Fatalf("existing ledger reconciled by %s: %q", name, out)
			}
			if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
				t.Fatalf("%s wrote .gitignore for a ledger it did not create: %v", name, err)
			}
		})
	}
}

// Boundary: a `.workingdir` that is another repository's working tree gets no ledger from
// `state sync`, so nothing was created and nothing is written to .gitignore.
func TestStateSyncLeavesANestedRepositoryIgnoreAlone(t *testing.T) {
	dir := gitRepoDir(t)
	if _, err := util.RunGit(t.Context(), dir, "init", "-q", ".workingdir"); err != nil {
		t.Fatalf("git init nested: %v", err)
	}
	out, err := runStateInitMode(t, []string{"sync"}, dir)
	if strings.Contains(out, "Added the Praetor") || (err != nil && strings.Contains(err.Error(), "could not make Git ignore")) {
		t.Fatalf("nested repository reached the ignore step: %q, %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("state sync wrote .gitignore without seeding a ledger: %v", err)
	}
}
