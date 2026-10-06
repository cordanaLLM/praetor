package main

import (
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

const fixtureEngineModule = "example.com/engine"

// newEngineContextFixture is newContextFixture inside a committed git checkout of
// fixtureEngineModule, the shape a client wrapper's compile-context meets in the engine's own
// checkout. It returns the directory and its first commit.
func newEngineContextFixture(t *testing.T) (string, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; the engine-build check compares revisions through git on this leg")
	}
	dir := newContextFixture(t, true)
	writeFixtureFile(t, dir, "go.mod", "module "+fixtureEngineModule+"\n\ngo 1.27\n")
	writeFixtureFile(t, dir, "cmd/engine/main.go", "package main\n\nfunc main() {}\n")
	return dir, commitFixture(t, dir)
}

func commitFixture(t *testing.T, dir string) string {
	t.Helper()
	ctx, err := util.WithCommandEnvironment(t.Context(), testsupport.HermeticGitEnv(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"commit", "-q", "-m", "fixture"},
	} {
		if _, err := util.RunGit(ctx, dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	head, err := util.RunGit(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// useEngineBuild makes compile-context see a fake installed binary stamped with revision.
func useEngineBuild(t *testing.T, revision string) {
	t.Helper()
	previous := engineBuild
	t.Cleanup(func() { engineBuild = previous })
	engineBuild = func() workstation.Build {
		return workstation.Build{Module: fixtureEngineModule, Revision: revision,
			Executable: filepath.Join(t.TempDir(), "praetorctl"), ModTime: time.Now()}
	}
}

// addRenderWorktree mirrors ci generated render (internal/generated/render.go): it ignores bin/
// and .standards/worktrees/ as the engine's own .gitignore does, commits that, and checks the new
// HEAD out into a worktree below .standards/worktrees. It returns HEAD and the worktree.
func addRenderWorktree(t *testing.T, dir string) (string, string) {
	t.Helper()
	writeFixtureFile(t, dir, ".gitignore", "/bin/\n/.standards/worktrees/\n")
	head := commitFixture(t, dir)
	render := filepath.Join(dir, ".standards", "worktrees", "render")
	if out, err := runFixtureGit(t, dir, testsupport.HermeticGitEnv(t), "worktree", "add", "-q", "--detach", render, head); err != nil {
		t.Fatalf("git worktree add: %v\n%s", err, out)
	}
	return head, render
}

// useCheckoutBuild makes compile-context see the checkout's own bin/praetorctl, built at
// revision from a modified tree after every change in it.
func useCheckoutBuild(t *testing.T, checkout, revision string) {
	t.Helper()
	previous := engineBuild
	t.Cleanup(func() { engineBuild = previous })
	engineBuild = func() workstation.Build {
		return workstation.Build{Module: fixtureEngineModule, Revision: revision, Modified: true,
			Executable: filepath.Join(checkout, "bin", "praetorctl"), ModTime: time.Now().Add(time.Hour)}
	}
}

// Positive (#760): ci generated render runs compile-context in a worktree of HEAD below the
// checkout. The checkout's own engine, marked modified only by the untracked gate receipt, writes
// the context there.
func TestCompileContextEngineBuild_Positive_RenderWorktreeOfReceiptCheckout(t *testing.T) {
	dir, _ := newEngineContextFixture(t)
	head, render := addRenderWorktree(t, dir)
	writeFixtureFile(t, dir, ".standards-receipt.json", "{}\n")
	useCheckoutBuild(t, dir, head)
	if out, err := runCompileContextCmd(t, render); err != nil {
		t.Fatalf("compile-context in the render worktree: %v\n%s", err, out)
	}
	if !util.FileExists(filepath.Join(render, "CLAUDE.md")) {
		t.Fatal("the checkout's own engine must write the render worktree's vendor files")
	}
}

// Negative (#760): the same engine built with an uncommitted Go edit writes nothing in the render
// worktree, which renders HEAD without that edit.
func TestCompileContextEngineBuild_Negative_RenderWorktreeOfEditedCheckout(t *testing.T) {
	dir, _ := newEngineContextFixture(t)
	head, render := addRenderWorktree(t, dir)
	writeFixtureFile(t, dir, "cmd/engine/main.go", "package main\n\nfunc main() { println() }\n")
	useCheckoutBuild(t, dir, head)
	if _, err := runCompileContextCmd(t, render); !errors.Is(err, workstation.ErrStaleEngine) {
		t.Fatalf("an engine carrying a Go edit: want ErrStaleEngine, got %v", err)
	}
	if util.FileExists(filepath.Join(render, "CLAUDE.md")) {
		t.Fatal("an engine carrying a Go edit must not write the render worktree's vendor files")
	}
}

// Positive: an install built from the checkout's own revision writes the context as before.
func TestCompileContextEngineBuild_Positive_CurrentInstallWrites(t *testing.T) {
	dir, head := newEngineContextFixture(t)
	useEngineBuild(t, head)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context with a current build: %v\n%s", err, out)
	}
	if !util.FileExists(filepath.Join(dir, "CLAUDE.md")) {
		t.Fatal("a current build must write the vendor files")
	}
}

// Negative: the reported defect. An install older than a Go change in the checkout writes
// nothing: no vendor file appears and AGENTS.md keeps its bytes (no register splice).
func TestCompileContextEngineBuild_Negative_StaleInstallWritesNothing(t *testing.T) {
	dir, installed := newEngineContextFixture(t)
	writeFixtureFile(t, dir, "internal/render/render.go", "package render\n")
	commitFixture(t, dir)
	useEngineBuild(t, installed)
	before := readFixtureFile(t, dir, "AGENTS.md")

	_, err := runCompileContextCmd(t, dir)
	if !errors.Is(err, workstation.ErrStaleEngine) {
		t.Fatalf("stale install: want ErrStaleEngine, got %v", err)
	}
	if util.FileExists(filepath.Join(dir, "CLAUDE.md")) {
		t.Fatal("a stale install must not write vendor files")
	}
	if got := readFixtureFile(t, dir, "AGENTS.md"); got != before {
		t.Fatalf("a stale install rewrote AGENTS.md:\n%s", got)
	}
}

// Boundary: verification writes nothing, so a stale install still verifies; the refusal is
// scoped to the write.
func TestCompileContextEngineBuild_Boundary_VerifyIsNotRefused(t *testing.T) {
	dir, head := newEngineContextFixture(t)
	useEngineBuild(t, head)
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	writeFixtureFile(t, dir, "internal/render/render.go", "package render\n")
	commitFixture(t, dir)
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify with a stale install: %v\n%s", err, out)
	}
	if _, err := runCompileContextCmd(t, dir); !errors.Is(err, workstation.ErrStaleEngine) {
		t.Fatalf("write with the same stale install: want ErrStaleEngine, got %v", err)
	}
}
