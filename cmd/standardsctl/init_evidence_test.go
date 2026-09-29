package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// initContextRepo is a Git work tree holding agents as AGENTS.md and, unless rules is empty, a
// .gitignore holding rules. It returns the directory and the manifest path init writes.
func initContextRepo(t *testing.T, agents, rules string) (dir, manifest string) {
	t.Helper()
	dir = t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, dir, "https://github.com/acme/kit.git")
	writeFixtureFile(t, dir, "AGENTS.md", agents)
	if rules != "" {
		writeFixtureFile(t, dir, ".gitignore", rules)
	}
	return dir, filepath.Join(dir, ".standards.yaml")
}

// Positive: init renders the register block's evidence rule, so in a work tree without an
// ignore rule it merges the managed block into .gitignore, says so, and the repository it
// onboards passes compile-context --verify at once.
func TestInitEvidence_Positive_WritesTheManagedBlock(t *testing.T) {
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest, "--facets=security:high")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mustContain(t, out, "Added the Praetor private-artifact block to .gitignore", "[TRANSPILED]", "successfully onboarded")
	if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
		t.Fatalf(".gitignore = %q; want the managed block", got)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("a freshly initialised work tree must verify: %v\n%s", err, out)
	}
}

// Negative: init runs the write's lint, so an AGENTS.md the gate rejects fails init after the
// targets are written, naming the findings and the command to rerun, never reporting success.
// An unmergeable .gitignore fails init before any agent context file is written.
func TestInitEvidence_Negative_FailsOnLintAndIgnore(t *testing.T) {
	dir, manifest := initContextRepo(t, proseAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest)
	if !errors.Is(err, compiler.ErrContextProse) {
		t.Fatalf("init over prose must fail on the lint, got %v\n%s", err, out)
	}
	for _, want := range []string{"context written, but compile-context --verify will fail", "run 'praetorctl compile-context'"} {
		mustErrContain(t, err, want)
	}
	if strings.Contains(out, "successfully onboarded") || strings.Contains(out, "[TRANSPILED]") {
		t.Fatalf("a failed lint must not report an onboarded repository:\n%s", out)
	}
	if !util.FileExists(filepath.Join(dir, "CLAUDE.md")) {
		t.Fatal("init must still write the targets it lints")
	}

	// The managed block's begin marker with no end marker: the merge refuses to guess where it ends.
	begin, _, _ := strings.Cut(adopt.ManagedGitIgnoreBlock(), "\n")
	unterminated := begin + "\n/.workingdir2/\n"
	broken, brokenManifest := initContextRepo(t, fixtureAgentsMD, unterminated)
	_, err = runInitCmd(t, "--output="+brokenManifest)
	mustErrContain(t, err, "could not make Git ignore .workingdir/evidence/")
	if got := readFixtureFile(t, broken, ".gitignore"); got != unterminated {
		t.Fatalf("a refused reconciliation changed .gitignore: %q", got)
	}
	if _, statErr := os.Stat(filepath.Join(broken, "CLAUDE.md")); !os.IsNotExist(statErr) {
		t.Fatalf("a refused reconciliation still compiled CLAUDE.md: %v", statErr)
	}
}

// Boundary: a rule that already excludes the directory is left byte for byte and prints no
// notice, and a canonical persona is projected too, so --verify passes on the persona copies
// init used to leave unwritten.
func TestInitEvidence_Boundary_IgnoredDirectoryAndPersonas(t *testing.T) {
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "/.workingdir/\n")
	writeFixtureFile(t, dir, ".agents/agents/praetor-gatekeeper.md", fixturePersona)
	out, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	if strings.Contains(out, "private-artifact block") || strings.Contains(out, "Warning:") {
		t.Fatalf("an ignored directory must print no notice:\n%s", out)
	}
	if got := readFixtureFile(t, dir, ".gitignore"); got != "/.workingdir/\n" {
		t.Fatalf(".gitignore rewritten: %q", got)
	}
	if got := readFixtureFile(t, dir, ".claude/agents/praetor-gatekeeper.md"); got != fixturePersona {
		t.Fatalf("init must project the canonical persona, got %q", got)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify after init: %v\n%s", err, out)
	}
}
