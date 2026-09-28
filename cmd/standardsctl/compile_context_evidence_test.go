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
)

// evidenceContextFixture is newContextFixture inside a Git work tree, holding .gitignore with
// rules, or no .gitignore when rules is empty.
func evidenceContextFixture(t *testing.T, rules string) string {
	t.Helper()
	dir := newContextFixture(t, false)
	testsupport.InitGitRepoWithOrigin(t, dir, "")
	if rules != "" {
		writeFixtureFile(t, dir, ".gitignore", rules)
	}
	return dir
}

// Positive: where Git already ignores .workingdir/, compile-context and --verify behave as before:
// .gitignore stays byte for byte and neither run prints an ignore notice.
func TestCompileContextEvidence_Positive_IgnoredDirectoryUnchanged(t *testing.T) {
	dir := evidenceContextFixture(t, "/.workingdir/\n")
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	if strings.Contains(out, "private-artifact block") || strings.Contains(out, "Warning:") {
		t.Fatalf("an ignored directory must print no notice:\n%s", out)
	}
	if got := readFixtureFile(t, dir, ".gitignore"); got != "/.workingdir/\n" {
		t.Fatalf(".gitignore rewritten: %q", got)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
}

// Negative: without an ignore rule, --verify fails naming the directory; compile-context writes
// the managed block, says so, and the next verify passes.
func TestCompileContextEvidence_Negative_WritesTheManagedBlock(t *testing.T) {
	dir := evidenceContextFixture(t, "")
	if out, err := runCompileContextCmd(t, dir); err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	if err := os.Remove(filepath.Join(dir, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	_, err := runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrEvidenceNotIgnored) {
		t.Fatalf("verify must fail on the unignored directory, got %v", err)
	}
	mustErrContain(t, err, ".workingdir/evidence/")
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "Added the Praetor private-artifact block to .gitignore", "Git now ignores .workingdir/")
	if got := readFixtureFile(t, dir, ".gitignore"); got != adopt.ManagedGitIgnoreBlock() {
		t.Fatalf(".gitignore = %q; want the managed block", got)
	}
	if out, err := runCompileContextCmd(t, dir, "--verify"); err != nil {
		t.Fatalf("verify after the write: %v\n%s", err, out)
	}
}

// Boundary: a declined git-ignore step leaves .gitignore alone and prints the warning state
// prints; verify still fails naming the directory, since the privacy rule is not declinable.
func TestCompileContextEvidence_Boundary_DeclinedWarns(t *testing.T) {
	dir := evidenceContextFixture(t, "")
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nadoption:\n  decline:\n    - git-ignore\n")
	out, err := runCompileContextCmd(t, dir)
	if err != nil {
		t.Fatalf("compile-context: %v\n%s", err, out)
	}
	mustContain(t, out, "Warning: Git does not ignore .workingdir/", "add /.workingdir/ to the operator-owned .gitignore")
	if _, statErr := os.Stat(filepath.Join(dir, ".gitignore")); !os.IsNotExist(statErr) {
		t.Fatalf("a declined git-ignore step wrote .gitignore: %v", statErr)
	}
	_, err = runCompileContextCmd(t, dir, "--verify")
	if !errors.Is(err, compiler.ErrEvidenceNotIgnored) {
		t.Fatalf("verify must fail while Git does not ignore the directory, got %v", err)
	}
	mustErrContain(t, err, "adoption.decline declines git-ignore")
}
