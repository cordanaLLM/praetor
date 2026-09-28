// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"debug/buildinfo"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// withEngineCommands commits a main package at every path goBuild compiles, so Install's real
// build runs against the fixture, and returns the new HEAD.
func (c *engineCheckout) withEngineCommands() string {
	c.t.Helper()
	for _, name := range binaryNames {
		c.write(strings.TrimPrefix(buildPackages[name], "./")+"/main.go", "package main\n\nfunc main() {}\n")
	}
	return c.commit("engine commands")
}

// requireGoTool skips a test that runs the real compiler where no go command is available.
func requireGoTool(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go is not on PATH; this test compiles the fixture with the real go build")
	}
}

// installedBuild reads an installed binary the way RunningBuild reads the running one.
func installedBuild(t *testing.T, path string) Build {
	t.Helper()
	info, err := buildinfo.ReadFile(path)
	if err != nil {
		t.Fatalf("read build information of %s: %v", path, err)
	}
	return describeBuild(info, path)
}

// porcelain is `git status --porcelain` in dir, the listing Go stamps vcs.modified from
// (cmd/go/internal/vcs, gitStatus).
func porcelain(t *testing.T, dir string) string {
	t.Helper()
	out, err := util.RunGit(context.Background(), dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status in %s: %v", dir, err)
	}
	return out
}

// Positive: a checkout whose tracked files match HEAD is built from a clean clone of HEAD, so
// an untracked receipt or Go file neither marks the build modified nor enters it, and the
// cleanup removes the clone without touching the checkout.
func TestPrepareBuildSource_Positive_UntrackedFilesStayOut(t *testing.T) {
	c := newEngineCheckout(t)
	head := c.git("rev-parse", "HEAD")
	c.write(".standards-receipt.json", "{}\n")
	c.write("cmd/engine/scratch.go", "package main\n")
	if porcelain(t, c.root) == "" {
		t.Fatal("fixture must carry untracked files Go would stamp as modified")
	}
	source, err := prepareBuildSource(context.Background(), c.root)
	if err != nil {
		t.Fatal(err)
	}
	if source.dir == c.root || source.commit != head {
		t.Fatalf("want a clone of %s, got dir %s commit %s", head, source.dir, source.commit)
	}
	if got := porcelain(t, source.dir); got != "" {
		t.Fatalf("clone is not clean, Go would stamp it modified: %q", got)
	}
	if out, err := util.RunGit(context.Background(), source.dir, "rev-parse", "HEAD"); err != nil || out != head {
		t.Fatalf("clone HEAD %q, %v; want %s", out, err, head)
	}
	for _, rel := range []string{".standards-receipt.json", "cmd/engine/scratch.go"} {
		if _, err := os.Stat(filepath.Join(source.dir, filepath.FromSlash(rel))); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("untracked %s entered the build source: %v", rel, err)
		}
	}
	if err := source.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(source.dir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cleanup left the clone behind: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.root, ".standards-receipt.json")); err != nil {
		t.Fatalf("the checkout itself must stay untouched: %v", err)
	}
}

// Negative: a modified tracked file keeps the work-in-progress build in the checkout itself,
// and a directory that is no git checkout is an error.
func TestPrepareBuildSource_Negative_ModifiedOrNoCheckout(t *testing.T) {
	c := newEngineCheckout(t)
	c.write("cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	source, err := prepareBuildSource(context.Background(), c.root)
	if err != nil || source.dir != c.root {
		t.Fatalf("modified tracked file: want the checkout itself, got %+v, %v", source, err)
	}
	if err := source.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(c.root, "cmd", "engine", "main.go")); err != nil {
		t.Fatalf("cleanup of the checkout source must remove nothing: %v", err)
	}
	if _, err := prepareBuildSource(context.Background(), t.TempDir()); err == nil {
		t.Fatal("a directory without a git checkout must be refused")
	}
}

// Negative: a post-checkout hook from a global hook directory never runs against the scratch
// clone, so preparing a build source executes nothing the operator's configuration names.
func TestPrepareBuildSource_Negative_GlobalHooksDoNotRun(t *testing.T) {
	if runtime.GOOS == goosWindows {
		t.Skip("the fixture hook is a POSIX shell script with an executable bit, which Windows does not honour")
	}
	c := newEngineCheckout(t)
	hooks := t.TempDir()
	marker := filepath.Join(t.TempDir(), "hook-ran")
	hook := "#!/bin/sh\n: > '" + marker + "'\n"
	if err := os.WriteFile(filepath.Join(hooks, "post-checkout"), []byte(hook), 0o700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(global, []byte("[core]\n\thooksPath = "+hooks+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	source, err := prepareBuildSource(context.Background(), c.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := source.cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the global post-checkout hook ran against the scratch clone: %v", err)
	}
}

// Boundary: a staged new file is a tracked change even though HEAD lacks it, so the build
// stays in the checkout and includes the staged file.
func TestPrepareBuildSource_Boundary_StagedFileIsTracked(t *testing.T) {
	c := newEngineCheckout(t)
	c.write("internal/extra/extra.go", "package extra\n")
	c.git("add", "internal/extra/extra.go")
	source, err := prepareBuildSource(context.Background(), c.root)
	if err != nil || source.dir != c.root {
		t.Fatalf("staged file: want the checkout itself, got %+v, %v", source, err)
	}
}

// Positive: the reported composition. After a merge, a refresh from a checkout holding an
// untracked receipt installs a build the engine-build check accepts: clean, at HEAD.
func TestRefresh_Positive_RealBuildPassesEngineBuildCheck(t *testing.T) {
	requireGoTool(t)
	e := newInstalledEngine(t)
	head := e.withEngineCommands()
	e.write(".standards-receipt.json", "{}\n")
	opts := e.refreshOptions(e.branch)
	opts.Install.Build = nil
	result, err := Refresh(context.Background(), opts)
	if err != nil || !result.Refreshed {
		t.Fatalf("refresh: %+v, %v", result, err)
	}
	installed := installedBuild(t, filepath.Join(e.opts.BinDir, "praetorctl"))
	if installed.Module != engineModule || installed.Revision != head || installed.Modified {
		t.Fatalf("installed build %+v; want a clean build of %s", installed, head)
	}
	if err := CheckBuildCurrent(context.Background(), e.root, installed); err != nil {
		t.Fatalf("the refreshed install must pass the engine-build check: %v", err)
	}
}

// Negative: an install of a modified tracked tree carries the edits and a -dirty stamp; it
// lives outside the checkout, so the engine-build check refuses it for context writes.
func TestInstall_Negative_WorkInProgressBuildIsRefusedByCheck(t *testing.T) {
	requireGoTool(t)
	c := newEngineCheckout(t)
	c.withEngineCommands()
	c.write("cmd/standardsctl/main.go", "package main\n\nfunc main() { println() }\n")
	opts := Options{Checkout: c.root, BinDir: filepath.Join(t.TempDir(), "bin"),
		ManifestPath: filepath.Join(t.TempDir(), "install.json"), GOOS: "linux"}
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatal(err)
	}
	installed := installedBuild(t, filepath.Join(opts.BinDir, "praetorctl"))
	if !installed.Modified {
		t.Fatalf("a work-in-progress install must be stamped modified: %+v", installed)
	}
	requireStale(t, CheckBuildCurrent(context.Background(), c.root, installed), "modified tree outside this checkout")
}

// TestCloneCommitCleanupReportsAFailedRemoval: a scratch clone the cleanup cannot remove is
// reported, never dropped (HISS-07); the caller joins it into Install's error.
func TestCloneCommitCleanupReportsAFailedRemoval(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("directory permissions cannot stop removal on Windows or as root")
	}
	c := newEngineCheckout(t)
	dir, cleanup, err := cloneCommit(context.Background(), c.root, c.git("rev-parse", "HEAD"))
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Dir(dir)
	if err := os.Chmod(parent, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(parent, 0o700); err != nil {
			t.Error(err)
		}
		if err := os.RemoveAll(parent); err != nil {
			t.Error(err)
		}
	})
	err = cleanup()
	if err == nil || !strings.Contains(err.Error(), "remove source clone") {
		t.Fatalf("cleanup of an unremovable clone: want a reported failure, got %v", err)
	}
}
