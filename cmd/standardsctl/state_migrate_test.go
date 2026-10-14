package main

import (
	"strings"
	"testing"
)

// runStateMigrateCLI runs `state migrate` with args and returns its standard output.
func runStateMigrateCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return dispatchCommand("state", append([]string{"migrate"}, args...)) })
}

// seedLegacyCLIFixture initializes .workingdir and plants a legacy ledger and evidence file.
func seedLegacyCLIFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".workingdir2/OPEN.md", "- [ ] legacy task\n")
	writeFixtureFile(t, dir, ".workingdir2/evidence/log.txt", "log\n")
	return dir
}

// Negative (#1233): `state migrate` is a dry run by default, refuses a differing ledger
// without --merge, writes nothing, and rejects an unknown flag.
func TestStateMigrateCommand_Negative_RefusesWithoutMerge(t *testing.T) {
	dir := seedLegacyCLIFixture(t)
	out, err := runStateMigrateCLI(t, dir)
	if err == nil || !strings.Contains(out, "dry run") || !strings.Contains(out, "conflict  OPEN.md") {
		t.Fatalf("differing ledger not refused: %q, %v", out, err)
	}
	if got := readFixtureFile(t, dir, ".workingdir2/OPEN.md"); got != "- [ ] legacy task\n" {
		t.Fatalf("refused migration changed the legacy ledger: %q", got)
	}
	if _, err := runStateMigrateCLI(t, "--bogus"); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

// Positive and boundary: --apply --merge moves the folder, and a second run is a no-op.
func TestStateMigrateCommand_Positive_AppliesThenNoOp(t *testing.T) {
	dir := seedLegacyCLIFixture(t)
	out, err := runStateMigrateCLI(t, "--apply", "--merge", "--dir="+dir)
	if err != nil || !strings.Contains(out, "move      evidence/log.txt") || !strings.Contains(out, ".workingdir2/ removed") {
		t.Fatalf("migration not applied: %q, %v", out, err)
	}
	if got := readFixtureFile(t, dir, ".workingdir/OPEN.md"); got != "- [ ] legacy task\n" {
		t.Fatalf("template ledger not replaced: %q", got)
	}
	out, err = runStateMigrateCLI(t, dir, "--apply")
	if err != nil || !strings.Contains(out, "nothing to migrate") {
		t.Fatalf("second run not a no-op: %q, %v", out, err)
	}
}
