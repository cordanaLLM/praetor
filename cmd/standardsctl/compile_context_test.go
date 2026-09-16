package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const fixturePersona = "# Gatekeeper\n\nBlock merges without receipts.\n"

// newContextFixture builds a directory with AGENTS.md, one canonical persona and,
// optionally, the praetor plugin manifest.
func newContextFixture(t *testing.T, withPlugin bool) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "AGENTS.md", fixtureAgentsMD)
	writeFixtureFile(t, dir, ".agents/agents/praetor-gatekeeper.md", fixturePersona)
	if withPlugin {
		writeFixtureFile(t, dir, pluginManifestRel, `{"name":"praetor"}`+"\n")
	}
	return dir
}

func compileContextArgs(dir string, extra ...string) []string {
	return append([]string{"--source=" + filepath.Join(dir, "AGENTS.md"), "--target-dir=" + dir}, extra...)
}

func runCompileContextCmd(t *testing.T, dir string, extra ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("compile-context", compileContextArgs(dir, extra...)) })
}

func TestCompileContext_Positive(t *testing.T) {
	dir := newContextFixture(t, true)

	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[COMPILED] 4 autonomous agent vendor projections", "[COMPILED] 1 plugin agent projections", "completed successfully")
	if !util.FileExists(filepath.Join(dir, "CLAUDE.md")) {
		t.Fatal("expected the transpiled CLAUDE.md to be written")
	}
	for _, rel := range append(vendorAgentDirs(), pluginAgentsRel) {
		if got := readFixtureFile(t, dir, rel+"/praetor-gatekeeper.md"); got != fixturePersona {
			t.Fatalf("projection %s differs from the canonical persona:\n%s", rel, got)
		}
	}

	// --verify passes and counts every projection; the audit gate agrees.
	out, err = runCompileContextCmd(t, dir, "--verify")
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	mustContain(t, out, "5 persona projections verified")
	if n, err := verifyAgentProjections(dir); err != nil || n != 5 {
		t.Fatalf("verifyAgentProjections = %d, %v; want 5, nil", n, err)
	}
}

func TestCompileContext_Negative(t *testing.T) {
	dir := newContextFixture(t, true)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v", err)
	}

	// A loosened vendor copy is detected by --verify and by the audit gate.
	writeFixtureFile(t, dir, ".github/agents/praetor-gatekeeper.md", "# Gatekeeper\n\nMerges may skip receipts.\n")
	_, err := runCompileContextCmd(t, dir, "--verify")
	mustErrContain(t, err, ".github/agents/praetor-gatekeeper.md")
	if !errors.Is(err, ErrAgentProjectionDrift) {
		t.Fatalf("expected ErrAgentProjectionDrift, got %v", err)
	}
	if err := auditAgentProjections(dir); !errors.Is(err, ErrAgentProjectionDrift) {
		t.Fatalf("audit gate: expected ErrAgentProjectionDrift, got %v", err)
	}

	// Regeneration repairs it; a stale plugin copy and a missing vendor copy are caught.
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if _, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify after regenerate: %v", err)
	}
	writeFixtureFile(t, dir, pluginAgentsRel+"/praetor-gatekeeper.md", "stale\n")
	_, err = runCompileContextCmd(t, dir, "--verify")
	mustErrContain(t, err, pluginAgentsRel)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("regenerate plugin copy: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, ".claude", "agents", "praetor-gatekeeper.md")); err != nil {
		t.Fatal(err)
	}
	_, err = runCompileContextCmd(t, dir, "--verify")
	mustErrContain(t, err, "missing or unreadable")

	// Flag misuse and a missing source are errors.
	_, err = runCompileContextCmd(t, dir, "extra")
	mustErrContain(t, err, "no positional arguments")
	_, err = captureStdout(t, func() error {
		return dispatchCommand("compile-context", []string{"--source=" + filepath.Join(dir, "missing.md"), "--target-dir=" + dir})
	})
	mustErrContain(t, err, "compilation failed")
}

func TestCompileContext_Negative_ProjectionFailureIsAnError(t *testing.T) {
	testsupport.SkipIfFileModeUnenforced(t)
	dir := newContextFixture(t, false)
	claudeDir := filepath.Join(dir, ".claude", "agents")
	if err := os.MkdirAll(claudeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(claudeDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(claudeDir, 0o700); err != nil {
			t.Log(err)
		}
	})

	out, err := runCompileContextCmd(t, dir)
	mustErrContain(t, err, "agent projection failed")
	if strings.Contains(out, "completed successfully") {
		t.Fatalf("a failed projection must not print success:\n%s", out)
	}
}

func TestCompileContext_Boundary(t *testing.T) {
	// No personas at all: nothing is projected and --verify counts zero.
	bare := t.TempDir()
	writeFixtureFile(t, bare, "AGENTS.md", fixtureAgentsMD)
	out, err := runCompileContextCmd(t, bare)
	if err != nil {
		t.Fatalf("bare compile: %v", err)
	}
	if strings.Contains(out, "agent vendor projections") {
		t.Fatalf("no personas must mean no projection line:\n%s", out)
	}
	out, err = runCompileContextCmd(t, bare, "--verify")
	if err != nil {
		t.Fatalf("bare verify: %v", err)
	}
	mustContain(t, out, "0 persona projections verified")

	// Without a plugin manifest the four vendor directories are the only targets, and
	// non-persona entries in .agents/agents are ignored.
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".agents/agents/notes.txt", "not a persona\n")
	if err := os.MkdirAll(filepath.Join(dir, ".agents", "agents", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile without plugin: %v", err)
	}
	if n, err := verifyAgentProjections(dir); err != nil || n != 4 {
		t.Fatalf("verifyAgentProjections = %d, %v; want 4, nil", n, err)
	}
	if util.DirExists(filepath.Join(dir, filepath.FromSlash(pluginAgentsRel))) {
		t.Fatal("plugin agents must not be projected without a plugin manifest")
	}
}
