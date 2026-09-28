package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/workstation"
)

const fixtureEngineModule = "example.com/engine"

// commitEngineFixture turns the fixture server's root into a committed checkout of
// fixtureEngineModule and returns its HEAD.
func commitEngineFixture(t *testing.T, root string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH; the engine-build check compares revisions through git on this leg")
	}
	writeFixtureFile(t, root, "go.mod", "module "+fixtureEngineModule+"\n\ngo 1.27\n")
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.email=test@example.com", "-c", "user.name=test", "commit", "-q", "-m", "fixture"},
	} {
		if _, err := util.RunGit(t.Context(), root, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	head, err := util.RunGit(t.Context(), root, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// useEngineBuild makes the server see a fake installed binary stamped with revision.
func useEngineBuild(t *testing.T, revision string) {
	t.Helper()
	previous := engineBuild
	t.Cleanup(func() { engineBuild = previous })
	engineBuild = func() workstation.Build {
		return workstation.Build{Module: fixtureEngineModule, Revision: revision,
			Executable: filepath.Join(t.TempDir(), "praetor-mcp"), ModTime: time.Now()}
	}
}

// Positive: a server built from the checkout's own revision writes as before.
func TestServer_CompileContextEngineBuild_Positive_CurrentBuildWrites(t *testing.T) {
	srv, root := newFixtureServer(t)
	useEngineBuild(t, commitEngineFixture(t, root))
	written := callTool(t, srv, "standards_compile_context", nil)
	expectText(t, "current build", written, "[COMPILED] CLAUDE.md")
}

// Negative: an installed server older than a Go change in the checkout it serves writes
// nothing and says why (BUG-1004).
func TestServer_CompileContextEngineBuild_Negative_StaleBuildWritesNothing(t *testing.T) {
	srv, root := newFixtureServer(t)
	installed := commitEngineFixture(t, root)
	writeFixtureFile(t, root, "internal/render/render.go", "package render\n")
	commitEngineFixture(t, root)
	useEngineBuild(t, installed)
	agents := filepath.Join(root, "AGENTS.md")
	before, err := os.ReadFile(agents)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}

	written := callTool(t, srv, "standards_compile_context", nil)
	expectError(t, "stale build", written, "engine build does not match this checkout")
	if util.FileExists(filepath.Join(root, "CLAUDE.md")) {
		t.Fatal("a stale server must not write vendor files")
	}
	if after, err := os.ReadFile(agents); err != nil || string(after) != string(before) {
		t.Fatalf("a stale server rewrote AGENTS.md: %v", err)
	}
}

// Boundary: verify_only writes nothing, so a stale server still verifies.
func TestServer_CompileContextEngineBuild_Boundary_VerifyOnlyIsNotRefused(t *testing.T) {
	srv, root := newFixtureServer(t)
	installed := commitEngineFixture(t, root)
	writeFixtureFile(t, root, "internal/render/render.go", "package render\n")
	commitEngineFixture(t, root)
	useEngineBuild(t, installed)
	verify := callTool(t, srv, "standards_compile_context", map[string]any{"verify_only": true})
	expectText(t, "verify with a stale build", verify, "100% in sync")
}
