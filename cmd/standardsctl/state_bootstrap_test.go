package main

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bootstrapLine runs `state init --if-absent` on dir and returns what it printed.
func bootstrapLine(t *testing.T, dir string) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"init", "--if-absent", dir})
	})
	if err != nil {
		t.Fatalf("bootstrap of %s failed: %v", dir, err)
	}
	return strings.TrimSpace(out)
}

// TestStateInitIfAbsentReportsWhatItDid pins the report line for each outcome. One fixed
// sentence for every non-creating return claimed a ledger had been seeded in the three
// cases that write nothing, including the partial ledger the bootstrap deliberately
// refuses to repair - the exact failure the operator then hits at `state sync`.
func TestStateInitIfAbsentReportsWhatItDid(t *testing.T) {
	fresh := t.TempDir()
	if line := bootstrapLine(t, fresh); !strings.Contains(line, "Initialized private .workingdir/") {
		t.Fatalf("created ledger reported as %q", line)
	}
	if line := bootstrapLine(t, fresh); !strings.Contains(line, "already holds ledger files and was left untouched") {
		t.Fatalf("complete ledger reported as %q", line)
	}

	adopted := t.TempDir()
	if err := os.Mkdir(filepath.Join(adopted, ".workingdir"), 0o700); err != nil {
		t.Fatal(err)
	}
	if line := bootstrapLine(t, adopted); !strings.Contains(line, "Seeded a ledger into the existing .workingdir/") {
		t.Fatalf("seeded ledger reported as %q", line)
	}

	partial := t.TempDir()
	bootstrapLine(t, partial)
	if err := os.Remove(filepath.Join(partial, ".workingdir", "BACKLOG.md")); err != nil {
		t.Fatal(err)
	}
	line := bootstrapLine(t, partial)
	if strings.Contains(line, "seeded") || strings.Contains(line, "Seeded") {
		t.Fatalf("partial ledger reported as seeded: %q", line)
	}
	if !strings.Contains(line, "repair a partial ledger explicitly") {
		t.Fatalf("partial ledger reported as %q", line)
	}

	linked := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(linked, ".workingdir")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if line := bootstrapLine(t, linked); !strings.Contains(line, "is not a directory; nothing was written") {
		t.Fatalf("linked path reported as %q", line)
	}
}

// TestStateInitIfAbsentFlagHelpMatchesTheBehaviour keeps the operator-visible flag help
// honest. `praetorctl state init --help` printed the pre-fix contract - "only when
// .workingdir is entirely absent; never repair existing ledgers" - after the command had
// started seeding an existing directory that holds no ledger file.
func TestStateInitIfAbsentFlagHelpMatchesTheBehaviour(t *testing.T) {
	fs := flag.NewFlagSet("state init", flag.ContinueOnError)
	stateInitFlags(fs)
	entry := fs.Lookup("if-absent")
	if entry == nil {
		t.Fatal("state init does not register --if-absent")
	}
	for _, want := range []string{"seed an existing one that holds no ledger file", "never repair a partial ledger"} {
		if !strings.Contains(entry.Usage, want) {
			t.Fatalf("flag help does not state %q: %s", want, entry.Usage)
		}
	}
	if strings.Contains(entry.Usage, "entirely absent") {
		t.Fatalf("flag help still promises the pre-fix contract: %s", entry.Usage)
	}
}

func TestStateInitIfAbsentCreatesOnlyNewLedger(t *testing.T) {
	dir := t.TempDir()
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err == nil {
		t.Fatal("direct audit accepted absent ledger")
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"init", "--if-absent", dir})
	}); err != nil {
		t.Fatalf("absent private ledger bootstrap failed: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err != nil {
		t.Fatalf("new ledger failed strict audit: %v", err)
	}
	missing := filepath.Join(dir, ".workingdir", "OPEN.md")
	if err := os.Remove(missing); err != nil {
		t.Fatal(err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"init", "--if-absent", dir})
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("bootstrap silently filled a partial ledger: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"audit", dir})
	}); err == nil {
		t.Fatal("partial ledger passed audit after bootstrap")
	}
}
