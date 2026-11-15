package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

// TestStateStatusFileCaps reports each live file and refuses growth beyond the baseline.
func TestStateStatusFileCaps(t *testing.T) {
	root := t.TempDir()
	if err := state.InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".workingdir/OPEN.md", strings.Repeat("- [ ] a\n", 150))
	if _, err := state.SyncState(t.Context(), root, "baseline"); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".workingdir/OPEN.md", strings.Repeat("- [ ] a\n", 151))
	out, err := captureStdout(t, func() error { return runStateStatus([]string{root}) })
	if err == nil || !strings.Contains(err.Error(), "OPEN.md") {
		t.Errorf("status accepted overflow: %v", err)
	}
	for _, name := range []string{"OPEN.md", "BACKLOG.md", "BUGS.md"} {
		if !strings.Contains(out, name) || !strings.Contains(out, "bytes") {
			t.Errorf("status omitted file measurements for %s: %s", name, out)
		}
	}
}

// TestStateTaskArchiveDryRunDefault proves preview leaves live records in place.
func TestStateTaskArchiveDryRunDefault(t *testing.T) {
	root := t.TempDir()
	if err := state.InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	const done = "# Open\n- [x] archived task (completed: 2026-09-12)\n"
	writeFixtureFile(t, root, ".workingdir/OPEN.md", done)
	out, err := captureStdout(t, func() error { return runTaskArchive([]string{root}) })
	if err != nil {
		t.Fatal(err)
	}
	if got := readStateFixtureDeadline(t, root, ".workingdir/OPEN.md"); got != done {
		t.Errorf("dry run changed OPEN.md: %q", got)
	}
	if !strings.Contains(out, "dry-run") || !strings.Contains(out, "OPEN.md: 1") || !strings.Contains(out, "bytes") {
		t.Errorf("missing archive preview: %s", out)
	}
}
