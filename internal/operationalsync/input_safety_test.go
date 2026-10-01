package operationalsync

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// stockLFSFilter is the block `git lfs install --local` writes into a repository's own
// configuration and Git for Windows into its system file. The sync fixture's attributes select
// only the probe driver, so in an input checkout this block names a driver no tracked path
// selects (#640, #667).
var stockLFSFilter = [][2]string{
	{"filter.lfs.clean", "git-lfs clean -- %f"},
	{"filter.lfs.smudge", "git-lfs smudge -- %f"},
	{"filter.lfs.process", "git-lfs filter-process"},
	{"filter.lfs.required", "true"},
}

// sentinelFilter is a filter command that records its run in sentinel and prints output in
// place of the file. The path is quoted for the shell git runs filter commands through, with
// forward slashes, which Git for Windows' shell reads as well.
func sentinelFilter(sentinel, output string) string {
	return "printf unsafe > '" + filepath.ToSlash(sentinel) + "'; printf " + output
}

func filterRan(t *testing.T, sentinel string) bool {
	t.Helper()
	_, err := os.Stat(sentinel)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// TestRunInputFilters_Positive_UnusedStockLFSBlockPasses: a stock filter.lfs block in the
// inputs' own configuration selects no tracked path, so status and checkout never run it, and
// prepare and init complete where the former any-filter.*-key rule refused them (#667).
func TestRunInputFilters_Positive_UnusedStockLFSBlockPasses(t *testing.T) {
	f := newSyncFixture(t)
	configureLFS := func(dirs ...string) {
		for _, dir := range dirs {
			for _, entry := range stockLFSFilter {
				testGit(t, f.git, dir, "config", entry[0], entry[1])
			}
		}
	}
	configureLFS(f.opts.OwnerPath, f.opts.SourcePath)
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatalf("prepare refused an unused filter.lfs block: %v", err)
	}
	if !r.MergePending || r.Status != "prepared" {
		t.Fatalf("prepare beside an unused filter.lfs block: %+v", r)
	}
	_, opts := newInitFixture(t)
	configureLFS(opts.OwnerPath)
	if r, err := Run(context.Background(), "init", opts); err != nil || r.Status != "initialized" {
		t.Fatalf("init refused an unused filter.lfs block: %+v %v", r, err)
	}
}

// TestRunInputFilters_Negative_SelectedStatusFilterRefusedBeforeStatus: a clean or process
// filter that an input configures -- locally, through an include or in worktree configuration,
// in the owner or the source checkout -- and engine.txt's tracked attribute selects is refused
// through util.RefuseGitStatusFilters before status could run it on the rewritten file.
func TestRunInputFilters_Negative_SelectedStatusFilterRefusedBeforeStatus(t *testing.T) {
	cases := []struct{ scope, key string }{
		{"local", "clean"}, {"included", "clean"}, {"worktree", "clean"}, {"source", "clean"}, {"local", "process"},
	}
	for _, tc := range cases {
		t.Run(tc.scope+"-"+tc.key, func(t *testing.T) {
			f := newSyncFixture(t)
			sentinel := filepath.Join(t.TempDir(), "executed")
			configureFilter(t, f, tc.scope, tc.key, sentinelFilter(sentinel, "filtered"))
			testWrite(t, f.opts.OwnerPath, "engine.txt", "base engine\n")
			f.opts.Destination = ""
			_, err := Run(context.Background(), "plan", f.opts)
			if !errors.Is(err, util.ErrGitStatusFilters) {
				t.Fatalf("a selected %s filter was not refused by the shared check: %v", tc.key, err)
			}
			if want := "filter.probe." + tc.key + " (filter=probe on engine.txt)"; !strings.Contains(err.Error(), want) {
				t.Fatalf("refusal %q does not name %q", err, want)
			}
			if filterRan(t, sentinel) {
				t.Fatal("a selected filter executed before the refusal")
			}
		})
	}
}

// TestRunInputFilters_Boundary_SelectedSmudgeNeverReachesCheckout: a smudge-only driver that
// engine.txt's tracked attribute selects passes, and checkout and merge in the candidate never
// run it, because the candidate is a fresh clone without the inputs' configuration. The control
// subtest shows the same driver does run where it is configured, so its absence above is the
// candidate's isolation, not a driver this host cannot start.
func TestRunInputFilters_Boundary_SelectedSmudgeNeverReachesCheckout(t *testing.T) {
	f := newSyncFixture(t)
	sentinel := filepath.Join(t.TempDir(), "executed")
	for _, dir := range []string{f.opts.OwnerPath, f.opts.SourcePath} {
		testGit(t, f.git, dir, "config", "filter.probe.smudge", sentinelFilter(sentinel, "smudged"))
	}
	r, err := Run(context.Background(), "prepare", f.opts)
	if err != nil {
		t.Fatalf("a smudge-only driver was refused: %v", err)
	}
	if !r.MergePending || filterRan(t, sentinel) {
		t.Fatalf("prepare ran a smudge driver or did not merge: %+v", r)
	}
	if raw, err := os.ReadFile(filepath.Join(r.Candidate, "engine.txt")); err != nil || string(raw) != "reviewed upstream advance\n" {
		t.Fatalf("candidate engine.txt = %q (%v), want the reviewed blob", raw, err)
	}
	t.Run("control", func(t *testing.T) {
		if err := os.Remove(filepath.Join(f.opts.OwnerPath, "engine.txt")); err != nil {
			t.Fatal(err)
		}
		testGit(t, f.git, f.opts.OwnerPath, "checkout", "--", "engine.txt")
		if !filterRan(t, sentinel) {
			t.Skip("this host's git did not start the configured smudge command, so the candidate assertion above cannot tell isolation from a driver that never starts")
		}
	})
}

func configureFilter(t *testing.T, f syncFixture, scope, key, command string) {
	t.Helper()
	name := "filter.probe." + key
	switch scope {
	case "included":
		root := t.TempDir()
		testWrite(t, root, "included.config", "[filter \"probe\"]\n "+key+" = \""+command+"\"\n")
		testGit(t, f.git, f.opts.OwnerPath, "config", "include.path", filepath.Join(root, "included.config"))
	case "worktree":
		testGit(t, f.git, f.opts.OwnerPath, "config", "extensions.worktreeConfig", "true")
		testGit(t, f.git, f.opts.OwnerPath, "config", "--worktree", name, command)
	case "source":
		testGit(t, f.git, f.opts.SourcePath, "config", name, command)
	default:
		testGit(t, f.git, f.opts.OwnerPath, "config", name, command)
	}
}

func TestRunRejectsReplacementAndGraftAncestry(t *testing.T) {
	for _, kind := range []string{"replace", "graft"} {
		t.Run(kind, func(t *testing.T) {
			f := newSyncFixture(t)
			testGit(t, f.git, f.opts.OwnerPath, "checkout", "--orphan", "orphan")
			f.opts.OwnerSHA = fixtureCommit(t, f.git, f.opts.OwnerPath)
			if kind == "replace" {
				testGit(t, f.git, f.opts.OwnerPath, "-c", "user.name=Fixture", "-c", "user.email=fixture@localhost", "replace", "--graft", f.opts.OwnerSHA, f.opts.BaseSHA)
			} else {
				testWrite(t, f.opts.OwnerPath, ".git/info/grafts", f.opts.OwnerSHA+" "+f.opts.BaseSHA+"\n")
			}
			f.opts.Destination = ""
			if _, err := Run(context.Background(), "plan", f.opts); err == nil {
				t.Fatal("synthetic ancestry accepted")
			}
		})
	}
}

func TestRunRejectsSubmoduleWorktreeBeforeStatus(t *testing.T) {
	f := newSyncFixture(t)
	stageIndexEntry(t, f.git, f.opts.OwnerPath, "160000", f.opts.BaseSHA, "module")
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
		t.Fatal("submodule index accepted")
	}
	if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
		t.Fatal("candidate created")
	}
}
