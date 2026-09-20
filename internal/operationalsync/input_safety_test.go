package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunRejectsLocalFiltersBeforeStatus(t *testing.T) {
	for _, scope := range []string{"local", "included", "worktree"} {
		t.Run(scope, func(t *testing.T) {
			f := newSyncFixture(t)
			sentinel := filepath.Join(t.TempDir(), "executed")
			command := "printf unsafe > '" + sentinel + "'; cat"
			configureFilter(t, f, scope, command)
			testWrite(t, f.opts.OwnerPath, "engine.txt", "base engine\n")
			f.opts.Destination = ""
			if _, err := Run(context.Background(), "plan", f.opts); err == nil {
				t.Fatal("local filter accepted")
			}
			if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
				t.Fatal("local filter executed")
			}
		})
	}
}

func configureFilter(t *testing.T, f syncFixture, scope, command string) {
	t.Helper()
	switch scope {
	case "included":
		root := t.TempDir()
		testWrite(t, root, "included.config", "[filter \"probe\"]\n clean = "+command+"\n")
		testGit(t, f.git, f.opts.OwnerPath, "config", "include.path", filepath.Join(root, "included.config"))
	case "worktree":
		testGit(t, f.git, f.opts.OwnerPath, "config", "extensions.worktreeConfig", "true")
		testGit(t, f.git, f.opts.OwnerPath, "config", "--worktree", "filter.probe.clean", command)
	default:
		testGit(t, f.git, f.opts.OwnerPath, "config", "filter.probe.clean", command)
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
