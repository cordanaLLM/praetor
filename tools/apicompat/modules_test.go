// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package apicompat

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// moduleRepo returns a git work tree holding files, of which git tracks those named in tracked.
func moduleRepo(t *testing.T, files []string, tracked ...string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git is not installed: %v", err)
	}
	root := t.TempDir()
	moduleGit(t, root, "init", "--quiet")
	for _, name := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("module example.com/widget\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if len(tracked) > 0 {
		moduleGit(t, root, append([]string{"add", "--"}, tracked...)...)
	}
	return root
}

func moduleGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	if out, err := util.RunGit(ctx, dir, args...); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
}

// Positive: a go.mod at the root or in any directory a ./... pattern builds is a module file.
// Negative: one below testdata, vendor, a dot or an underscore directory is not, nor is a file
// that only resembles one. Boundary: the gate's depth bound of 256 directory elements holds.
func TestIsModuleFile(t *testing.T) {
	for _, name := range []string{"go.mod", "lib/go.mod", "a/b/c/go.mod", "v2/go.mod"} {
		if !IsModuleFile(name) {
			t.Errorf("%s is not a module file", name)
		}
	}
	for _, name := range []string{
		"testdata/go.mod", "a/testdata/b/go.mod", "vendor/dep/go.mod", ".hidden/go.mod", "_scratch/go.mod",
		"go.mod.bak", "notgo.mod", "lib/go.sum", "go.work", "",
	} {
		if IsModuleFile(name) {
			t.Errorf("%q is a module file", name)
		}
	}
	deep := strings.Repeat("d/", maxModulePathElements)
	if !IsModuleFile(deep + "go.mod") {
		t.Errorf("a module %d directories deep is not a module file", maxModulePathElements)
	}
	if IsModuleFile("d/" + deep + "go.mod") {
		t.Errorf("a module past %d directories is a module file", maxModulePathElements)
	}
}

// Positive: the go.mod files git tracks at the root and nested are listed, sorted, and a
// repository holding one tracks a module. Negative: an untracked go.mod and one in a skipped
// directory are not listed, and a directory outside any git work tree, or one git answers is no
// repository, tracks none. Boundary: a read that did not complete is an error rather than an
// empty listing, and a nil context is refused.
func TestTrackedModuleFiles(t *testing.T) {
	files := []string{"go.mod", "tools/go/go.mod", "lib/go.mod", "testdata/fixture/go.mod", "scratch/go.mod"}
	root := moduleRepo(t, files, "go.mod", "tools/go/go.mod", "lib/go.mod", "testdata/fixture/go.mod")
	got, err := TrackedModuleFiles(t.Context(), root)
	if err != nil || !slices.Equal(got, []string{"go.mod", "lib/go.mod", "tools/go/go.mod"}) {
		t.Fatalf("tracked modules = %q, %v", got, err)
	}
	if tracks, err := TracksModule(t.Context(), root); err != nil || !tracks {
		t.Fatalf("TracksModule = %v, %v", tracks, err)
	}

	untracked := moduleRepo(t, []string{"go.mod", "testdata/go.mod"}, "testdata/go.mod")
	if tracks, err := TracksModule(t.Context(), untracked); err != nil || tracks {
		t.Fatalf("an untracked go.mod: TracksModule = %v, %v", tracks, err)
	}
	if got, err := TrackedModuleFiles(t.Context(), t.TempDir()); err != nil || got != nil {
		t.Fatalf("outside a work tree: %q, %v", got, err)
	}

	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, ".git"), []byte("gitdir: "+filepath.Join(broken, "missing")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "go.mod"), []byte("module example.com/widget\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := TrackedModuleFiles(t.Context(), broken); err != nil || got != nil {
		t.Fatalf("a .git that git rejects: %q, %v", got, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if tracks, err := TracksModule(cancelled, root); err == nil || tracks {
		t.Fatalf("an unanswered git read: TracksModule = %v, %v", tracks, err)
	}
	var nilContext context.Context
	if _, err := TrackedModuleFiles(nilContext, root); err == nil {
		t.Fatal("a nil context was accepted")
	}
}
