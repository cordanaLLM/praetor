package main

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

// staleRegisterBlock rewrites the line after the start marker, which is what a hand edit
// between the markers looks like.
func staleRegisterBlock(t *testing.T, dir string) {
	t.Helper()
	source := readFixtureFile(t, dir, "AGENTS.md")
	marker := config.RegisterBlockStart + "\n"
	if strings.Count(source, marker) != 1 {
		t.Fatalf("fixture AGENTS.md must carry the register block once:\n%s", source)
	}
	writeFixtureFile(t, dir, "AGENTS.md", strings.Replace(source, marker, marker+"Write however you like.\n", 1))
}

func TestCompileContextRegister_Positive(t *testing.T) {
	dir := newContextFixture(t, false)
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n")

	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "[SPLICED]", "text register")
	for _, rel := range []string{"AGENTS.md", "CLAUDE.md", ".codex/rules.md"} {
		content := readFixtureFile(t, dir, rel)
		if strings.Count(content, config.RegisterBlockStart) != 1 || !strings.Contains(content, "ci_debugging") {
			t.Errorf("%s must carry the block rendered from the manifest once:\n%s", rel, content)
		}
	}
	if out, err = runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify after compile: %v\n%s", err, out)
	}
	// A second compile finds nothing to splice.
	if out, err = runCompileContextCmd(t, dir); err != nil || strings.Contains(out, "[SPLICED]") {
		t.Fatalf("second compile must not splice again: %v\n%s", err, out)
	}
}

func TestCompileContextRegister_Negative(t *testing.T) {
	dir := newContextFixture(t, false)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v", err)
	}
	staleRegisterBlock(t, dir)
	edited := readFixtureFile(t, dir, "AGENTS.md")

	_, err := runCompileContextCmd(t, dir, "--verify")
	mustErrContain(t, err, "AGENTS.md")
	if !errors.Is(err, compiler.ErrRegisterBlockOutOfSync) {
		t.Fatalf("expected ErrRegisterBlockOutOfSync, got %v", err)
	}
	if readFixtureFile(t, dir, "AGENTS.md") != edited {
		t.Fatal("--verify must never write the source")
	}

	// An undeclared task label stops the compile before anything is written.
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  tasks:\n    deploy_prod: docs\n")
	_, err = runCompileContextCmd(t, dir)
	mustErrContain(t, err, `register task "deploy_prod" is not a declared target_tasks label`)
	if readFixtureFile(t, dir, "AGENTS.md") != edited {
		t.Fatal("a rejected manifest must leave the source untouched")
	}
}

func TestCompileContextRegister_Boundary(t *testing.T) {
	// No manifest: the defaults render, and --verify accepts exactly that block.
	dir := newContextFixture(t, false)
	if _, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile without a manifest: %v", err)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify without a manifest: %v\n%s", err, out)
	}
	if !strings.Contains(readFixtureFile(t, dir, "AGENTS.md"), "every other label and any brief without one = internal.") {
		t.Fatal("the default block must name the internal fallback")
	}

	// A source that never received the block is drift, not a pass.
	bare := t.TempDir()
	writeFixtureFile(t, bare, "AGENTS.md", fixtureAgentsMD)
	_, err := captureStdout(t, func() error {
		return dispatchCommand("compile-context", []string{"--verify", "--source=" + filepath.Join(bare, "AGENTS.md"), "--target-dir=" + bare})
	})
	if !errors.Is(err, compiler.ErrRegisterBlockOutOfSync) {
		t.Fatalf("missing block: expected ErrRegisterBlockOutOfSync, got %v", err)
	}
}
