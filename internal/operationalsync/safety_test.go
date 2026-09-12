package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunIgnoresAmbientAndSourceHooks(t *testing.T) {
	f := newSyncFixture(t)
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	sentinel := filepath.Join(root, "executed")
	script := "#!/bin/sh\nprintf unsafe > '" + sentinel + "'\n"
	for _, name := range []string{"post-checkout", "post-merge", "pre-merge-commit"} {
		testWrite(t, hooks, name, script)
		if err := os.Chmod(filepath.Join(hooks, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	testWrite(t, root, "gitconfig", "[core]\n hooksPath = "+hooks+"\n[filter \"hostile\"]\n smudge = "+filepath.Join(hooks, "post-checkout")+"\n required = true\n")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(root, "gitconfig"))
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "core.hooksPath")
	t.Setenv("GIT_CONFIG_VALUE_0", hooks)
	testGit(t, f.git, f.opts.OwnerPath, "config", "core.hooksPath", hooks)
	testWrite(t, f.opts.SourcePath, ".gitattributes", "engine.txt filter=hostile\n")
	f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
	if _, err := Run(context.Background(), "prepare", f.opts); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(sentinel); !os.IsNotExist(err) {
		t.Fatal("source or ambient hook/filter executed")
	}
	if got := testGit(t, f.git, f.opts.OwnerPath, "config", "--get", "core.hooksPath"); got != hooks {
		t.Fatal("original hook config changed")
	}
}

func TestRunRejectsSymlinkAndUnrelatedHistory(t *testing.T) {
	t.Run("input subdirectory", func(t *testing.T) {
		f := newSyncFixture(t)
		f.opts.OwnerPath = filepath.Join(f.opts.OwnerPath, ".paperclip")
		f.opts.Destination = filepath.Join(filepath.Dir(f.opts.OwnerPath), "unsafe-nested-candidate")
		if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
			t.Fatal("input subdirectory accepted")
		}
		if _, err := os.Stat(f.opts.Destination); !os.IsNotExist(err) {
			t.Fatal("nested mutation occurred")
		}
	})
	t.Run("destination ancestor", func(t *testing.T) {
		f := newSyncFixture(t)
		alias := filepath.Join(t.TempDir(), "alias")
		if err := os.Symlink(t.TempDir(), alias); err != nil {
			t.Fatal(err)
		}
		f.opts.Destination = filepath.Join(alias, "candidate")
		if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
			t.Fatal("symlink ancestor accepted")
		}
	})
	t.Run("tracked symlink", func(t *testing.T) {
		f := newSyncFixture(t)
		target := filepath.Join(f.opts.SourcePath, ownerPaths[0])
		if err := os.Remove(target); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("engine.txt", target); err != nil {
			t.Fatal(err)
		}
		f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
		if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
			t.Fatal("symlink manifest accepted")
		}
	})
	t.Run("unrelated", func(t *testing.T) {
		f := newSyncFixture(t)
		testGit(t, f.git, f.opts.SourcePath, "checkout", "--orphan", "unrelated")
		f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
		if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
			t.Fatal("unrelated source accepted")
		}
	})
}

func TestRunRetainsFailureCandidate(t *testing.T) {
	f := newSyncFixture(t)
	// A tracked .gitmodules is just data: prepare neither initializes it nor executes its URL.
	testWrite(t, f.opts.SourcePath, ".gitmodules", "[submodule \"unused\"]\n path = unused\n url = ext::sh -c unsafe\n")
	f.opts.SourceSHA = fixtureCommit(t, f.git, f.opts.SourcePath)
	if _, err := Run(context.Background(), "prepare", f.opts); err != nil {
		t.Fatal(err)
	}
	if _, err := Run(context.Background(), "prepare", f.opts); err == nil {
		t.Fatal("existing prepared candidate overwritten")
	}
	if got := testGit(t, f.git, f.opts.Destination, "rev-parse", "MERGE_HEAD"); got != f.opts.SourceSHA {
		t.Fatal("candidate evidence changed")
	}
}

func TestOverlayRejectsMalformedManifestAndPreservesUnknowns(t *testing.T) {
	for _, raw := range []string{"", "repository: []", "repository: {}\nrepository: {}", "version: 1\n---\nversion: 2"} {
		if _, _, err := manifest([]byte(raw)); err == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
	f := newSyncFixture(t)
	raw, err := os.ReadFile(filepath.Join(f.opts.SourcePath, ownerPaths[0]))
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, []byte("# future comment\nfuture: {nested: [one, two]}\n")...)
	out, err := ownerManifest(raw, identity{"private", "praetor", "private"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "future comment") || !strings.Contains(string(out), "nested: [one, two]") {
		t.Fatal("unknown nodes or comments lost")
	}
}

func TestRemoteBoundary(t *testing.T) {
	for _, raw := range []string{"https://token@github.com/private/praetor.git", "https://evil.invalid/private/praetor", "https://github.com/a/b/extra", "ext::danger"} {
		if _, err := remoteIdentity(raw); err == nil {
			t.Fatal("unsafe remote accepted", raw)
		}
	}
	if got, err := remoteIdentity("git@github.com:private/praetor.git"); err != nil || got != "private/praetor" {
		t.Fatal(got, err)
	}
}
