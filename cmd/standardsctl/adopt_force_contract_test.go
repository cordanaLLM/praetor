package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// forceFlagUsage returns the usage block adopt --help prints for -force: the lines after the
// flag name up to the next flag.
func forceFlagUsage(t *testing.T, help string) string {
	t.Helper()
	_, usage, found := strings.Cut(help, "\n  -force\n")
	if !found {
		t.Fatalf("adopt --help lists no -force flag:\n%s", help)
	}
	usage, _, _ = strings.Cut(usage, "\n  -")
	return usage
}

// TestAdoptHelpStatesTheForceContract (#502): adopt --help states the --force contract the
// adoption steps implement (adopt.ForceContract), and that the flag needs --lock-source-root,
// a dry run included (positive); the reset wording
// earlier releases printed is gone (negative); the flag keeps its false default, so a run without
// it never takes the forced path (boundary: a bool flag prints a default only when it is true).
func TestAdoptHelpStatesTheForceContract(t *testing.T) {
	code, out := praetorctl(t, "adopt", "--help")
	if code != 0 {
		t.Fatalf("adopt --help: exit %d\n%s", code, out)
	}
	usage := forceFlagUsage(t, out)
	for _, phrase := range []string{adopt.ForceContract, "--force needs --lock-source-root, --dry-run included"} {
		if !strings.Contains(usage, phrase) {
			t.Fatalf("adopt --help -force usage lacks %q:\n%s", phrase, usage)
		}
	}
	if strings.Contains(out, "Overwrite existing standards configurations") {
		t.Fatalf("adopt --help still describes --force as a reset:\n%s", usage)
	}
	if strings.Contains(usage, "(default true)") {
		t.Fatalf("adopt --force must default to false:\n%s", usage)
	}
}

// TestAdoptForceNeedsLockSourceDryRunIncluded (#502) holds the last clause of the -force help
// against the run: on an adopted repository a dry run without --lock-source-root passes
// (positive), the same dry run with --force fails naming the missing lock source, because --force
// rebuilds the lock (negative), and a forced dry run given the source passes and writes nothing
// (boundary).
func TestAdoptForceNeedsLockSourceDryRunIncluded(t *testing.T) {
	source, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	initGitFixture(t, root)
	if code, out := praetorctl(t, "adopt", "--path", root, "--profile", "planning-artifacts", "--facets", "security:high",
		"--lock-source-root", source); code != 0 {
		t.Fatalf("adopt: exit %d\n%s", code, out)
	}
	lock := readFixtureFile(t, root, ".standards.lock")
	if code, out := praetorctl(t, "adopt", "--path", root, "--dry-run"); code != 0 {
		t.Fatalf("a plain dry run of an adopted repository needs no lock source: exit %d\n%s", code, out)
	}
	code, out := praetorctl(t, "adopt", "--path", root, "--dry-run", "--force")
	if code == 0 || !strings.Contains(out, adopt.ErrLockSourceRequired.Error()) {
		t.Fatalf("a forced dry run without a lock source must fail naming it: exit %d\n%s", code, out)
	}
	if code, out := praetorctl(t, "adopt", "--path", root, "--dry-run", "--force", "--lock-source-root", source); code != 0 {
		t.Fatalf("a forced dry run with the lock source: exit %d\n%s", code, out)
	}
	if readFixtureFile(t, root, ".standards.lock") != lock {
		t.Fatal("a forced dry run rewrote .standards.lock")
	}
}
