package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
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

// existingLedgerRepo is a Git work tree holding a ledger an earlier command, or an older
// binary, created without making Git ignore it, plus the given .gitignore when non-empty.
func existingLedgerRepo(t *testing.T, gitignore string) string {
	t.Helper()
	dir := gitRepoDir(t)
	if err := state.InitWorkingDir(dir); err != nil {
		t.Fatal(err)
	}
	if gitignore != "" {
		writeFixtureFile(t, dir, ".gitignore", gitignore)
	}
	return dir
}

// ledgerFiles returns every regular file directly under .workingdir, by name.
func ledgerFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dir, state.WorkingDirName))
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	files := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.Type().IsRegular() {
			files[entry.Name()] = readFixtureFile(t, dir, filepath.Join(state.WorkingDirName, entry.Name()))
		}
	}
	return files
}

// Positive: a ledger that exists but that Git does not ignore - the population an older
// binary, or a run before `git init`, left behind - is reconciled by the next command that
// writes into it, not only by an operator who knows to run `state init`.
func TestLedgerCreatorsReconcileAnExistingUnignoredLedger(t *testing.T) {
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := existingLedgerRepo(t, "")
			out, err := runLedgerCreator(t, creator, dir)
			checkCreatorResult(t, creator, err)
			if !strings.Contains(out, "Added the Praetor private-artifact block to .gitignore") {
				t.Fatalf("existing unignored ledger not reconciled by %s: %q", name, out)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
				t.Fatalf(".gitignore = %q; want the managed block alone", got)
			}
		})
	}
}

// Boundary: an existing ledger Git already ignores costs every creator one probe and no
// write, so adopted repositories see .gitignore byte for byte as they left it.
func TestLedgerCreatorsLeaveAnEffectiveRuleAlone(t *testing.T) {
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := existingLedgerRepo(t, "/.workingdir/\n")
			out, err := runLedgerCreator(t, creator, dir)
			checkCreatorResult(t, creator, err)
			if strings.Contains(out, "Added the Praetor") {
				t.Fatalf("effective rule rewritten by %s: %q", name, out)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != "/.workingdir/\n" {
				t.Fatalf("%s rewrote an effective .gitignore: %q", name, got)
			}
		})
	}
}

// Boundary: a manifest that declines git-ignore is warned about when the command seeds the
// ledger, and not again on every later command that writes into it.
func TestDeclinedIgnoreIsWarnedOnceWhenTheLedgerIsSeeded(t *testing.T) {
	const warning = "Warning: Git does not ignore .workingdir/"
	dir := gitRepoDir(t)
	writeFixtureFile(t, dir, ".standards.yaml", "adoption:\n  decline:\n    - git-ignore\n")
	add := func(desc string) (string, error) {
		return captureStderr(t, func() error {
			_, err := captureStdout(t, func() error {
				return dispatchCommand("state", []string{"task", "add", desc, "--dir=" + dir})
			})
			return err
		})
	}
	if errOut, err := add("seeding task"); err != nil || !strings.Contains(errOut, warning) {
		t.Fatalf("seeding run: stderr %q, %v; want the decline warning", errOut, err)
	}
	if errOut, err := add("later task"); err != nil || strings.Contains(errOut, warning) {
		t.Fatalf("later run: stderr %q, %v; want no repeated warning", errOut, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("declined git-ignore still wrote .gitignore: %v", err)
	}
}

// Negative: an existing ledger over an unmergeable .gitignore refuses every creator before
// it writes, so the ledger stays byte for byte and a retry cannot record a change twice.
func TestLedgerCreatorsRefuseBeforeWritingIntoAnUnignoredLedger(t *testing.T) {
	broken := "# BEGIN praetor private artifacts (praetorctl adopt)\n/.workingdir2/\n"
	for name, creator := range ledgerCreators {
		t.Run(name, func(t *testing.T) {
			dir := existingLedgerRepo(t, broken)
			before := ledgerFiles(t, dir)
			_, err := runLedgerCreator(t, creator, dir)
			if err == nil || !strings.Contains(err.Error(), "could not make Git ignore") || !strings.Contains(err.Error(), "nothing was written") {
				t.Fatalf("%s wrote into an unignored ledger: %v", name, err)
			}
			if after := ledgerFiles(t, dir); !reflect.DeepEqual(before, after) {
				t.Fatalf("%s changed the ledger it refused:\nbefore %q\nafter  %q", name, before, after)
			}
			if got := readFixtureFile(t, dir, ".gitignore"); got != broken {
				t.Fatalf("refused run changed .gitignore: %q", got)
			}
		})
	}
}

// Negative: the first run that seeds the ledger over an unmergeable .gitignore says its
// task was recorded; the retry is refused before it writes, so the task is recorded once,
// and once .gitignore is repaired the next command makes Git ignore the ledger.
func TestFailedIgnoreStepIsRetriedWithoutDuplicatingTheChange(t *testing.T) {
	dir := gitRepoDir(t)
	writeFixtureFile(t, dir, ".gitignore", "# BEGIN praetor private artifacts (praetorctl adopt)\n/.workingdir2/\n")
	add := func(desc string) error {
		_, err := captureStdout(t, func() error {
			return dispatchCommand("state", []string{"task", "add", desc, "--dir=" + dir})
		})
		return err
	}
	if err := add("only once"); err == nil || !strings.Contains(err.Error(), "do not repeat it") {
		t.Fatalf("first run: %v; want the recorded-change notice", err)
	}
	if err := add("only once"); err == nil || !strings.Contains(err.Error(), "nothing was written") {
		t.Fatalf("retry: %v; want a refusal before writing", err)
	}
	if err := dispatchCommand("state", []string{"sync", dir}); err == nil {
		t.Fatal("state sync accepted a ledger Git does not ignore")
	}
	if got := strings.Count(readFixtureFile(t, dir, ".workingdir/OPEN.md"), "only once"); got != 1 {
		t.Fatalf("task recorded %d times, want 1", got)
	}
	writeFixtureFile(t, dir, ".gitignore", "")
	if err := add("after repair"); err != nil {
		t.Fatalf("after repair: %v", err)
	}
	if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
		t.Fatalf(".gitignore after repair = %q; want the managed block", got)
	}
}

// Boundary: a ledger seeded before `git init` needed no rule then; the first command after
// the repository exists writes it.
func TestLedgerSeededBeforeGitInitIsIgnoredOnceARepositoryExists(t *testing.T) {
	dir := t.TempDir()
	if present, err := util.GitWorktreePresent(t.Context(), dir); err != nil || present {
		t.Skipf("temporary directory sits inside a Git work tree (%v, %v)", present, err)
	}
	creator := ledgerCreators["state task add"]
	if _, err := runLedgerCreator(t, creator, dir); err != nil {
		t.Fatalf("task add outside a repository: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(err) {
		t.Fatalf("task add wrote .gitignore outside a repository: %v", err)
	}
	if _, err := util.RunGit(t.Context(), dir, "init", "-q"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if out, err := runLedgerCreator(t, creator, dir); err != nil || !strings.Contains(out, "Added the Praetor") {
		t.Fatalf("task add after git init: %q, %v; want the ignore block written", out, err)
	}
}

// withLedgerIgnore's probe failures: a first probe that fails is read as an absent ledger,
// so the ignore step still follows a run that creates one; a second probe that fails
// surfaces as its own error, unless the command already failed and names the cause.
func TestWithLedgerIgnoreProbeFailures(t *testing.T) {
	t.Run("first-probe-fails", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "created-by-run")
		out, err := captureStdout(t, func() error {
			return withLedgerIgnore(t.Context(), dir, func() error {
				if _, err := util.RunGit(t.Context(), filepath.Dir(dir), "init", "-q", dir); err != nil {
					return err
				}
				return state.InitWorkingDir(dir)
			})
		})
		if err != nil || !strings.Contains(out, "Added the Praetor") {
			t.Fatalf("ignore step skipped after a failed first probe: %q, %v", out, err)
		}
	})
	sentinel := errors.New("command failed")
	for name, runErr := range map[string]error{"second-probe-fails": nil, "second-probe-and-run-fail": sentinel} {
		t.Run(name, func(t *testing.T) {
			dir := gitRepoDir(t)
			err := withLedgerIgnore(t.Context(), dir, func() error {
				if err := os.RemoveAll(dir); err != nil {
					return err
				}
				return runErr
			})
			switch {
			case runErr != nil && (!errors.Is(err, sentinel) || strings.Contains(err.Error(), "could not tell")):
				t.Fatalf("command error not returned as the cause: %v", err)
			case runErr == nil && (err == nil || !strings.Contains(err.Error(), "could not tell whether Git must ignore")):
				t.Fatalf("failed second probe not reported: %v", err)
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
