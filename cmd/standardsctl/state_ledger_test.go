package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

const cliLegacyState = "# Session State\n\n### [2026-09-11 17:22:14 UTC] Commit `173c565` on `main`\n" +
	"- **Activity**: Automated state synchronization\n- **Open Bugs**: 0 | **Pending Questions**: 0\n" +
	"\n<!-- praetor-state:v1 sha256:" + "08ef0524fb3a5a02b855b2934d5f4092792fe5c723dd7332dffb69edfe41e52c" + " -->\n"

func TestStateCompactCommand(t *testing.T) {
	dir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".workingdir/STATE.md", cliLegacyState)
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", dir}) }); err == nil {
		t.Fatal("compact accepted a ledger whose last marker does not verify")
	}
	if got := readFixtureFile(t, dir, ".workingdir/STATE.md"); got != cliLegacyState {
		t.Fatalf("refused compaction changed STATE.md:\n%s", got)
	}
	// Sync supersedes the trailing legacy marker, so compaction finds none left to drop.
	if err := dispatchCommand("state", []string{"sync", dir}); err != nil {
		t.Fatal(err)
	}
	if got := readFixtureFile(t, dir, ".workingdir/STATE.md"); strings.Count(got, "<!-- praetor-state") != 1 {
		t.Fatalf("sync kept a superseded marker:\n%s", got)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", "--dir=" + dir}) })
	if err != nil || !strings.Contains(out, "compact: 1 entries rewritten, 1 kept, 0 markers dropped") {
		t.Fatalf("compact output %q, %v", out, err)
	}
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", dir}) })
	if err != nil || !strings.HasPrefix(out, "compact: already compact, no change") {
		t.Fatalf("second compact not a no-op: %q, %v", out, err)
	}
	if err := dispatchCommand("state", []string{"sync", "--verify", dir}); err != nil {
		t.Fatalf("compacted ledger does not verify: %v", err)
	}
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", "--bogus"}) }); err == nil {
		t.Fatal("unknown flag accepted")
	}
}

func TestStateMigrateBugsCommand(t *testing.T) {
	dir := t.TempDir()
	md, err := state.RenderBugsMarkdownStrict([]state.BugEntry{{ID: "BUG-001", Title: "inline", Severity: "p1", Status: "open", Context: "keep me"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".workingdir/BUGS.md", md)
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"migrate-bugs", dir}) }); err == nil {
		t.Fatal("migrate-bugs accepted an unsynchronized ledger")
	}
	if err := dispatchCommand("state", []string{"sync", dir}); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"migrate-bugs", "--dir=" + dir}) })
	if err != nil || !strings.Contains(out, "migrate-bugs: 1 of 1 rows moved, round trip ok") {
		t.Fatalf("migrate-bugs output %q, %v", out, err)
	}
	if got := readFixtureFile(t, dir, ".workingdir/BUGS.md"); strings.Contains(got, "praetor-bug:v1") {
		t.Fatalf("inline metadata left in BUGS.md:\n%s", got)
	}
	if got := readFixtureFile(t, dir, ".workingdir/bugs.meta.json"); !strings.Contains(got, `"context":"keep me"`) {
		t.Fatalf("sidecar lost the context: %s", got)
	}
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"migrate-bugs", dir}) })
	if err != nil || !strings.HasPrefix(out, "migrate-bugs: nothing to migrate, no change (1 rows)") {
		t.Fatalf("second migrate-bugs not a no-op: %q, %v", out, err)
	}
	if err := dispatchCommand("state", []string{"sync", "--verify", dir}); err != nil {
		t.Fatalf("migrated ledger does not verify: %v", err)
	}
}

// TestStateQuestionContextCommand covers --context end to end: it used to be accepted by
// `state question add` and written nowhere, so `question list` could never show it.
func TestStateQuestionContextCommand(t *testing.T) {
	dir := t.TempDir()
	add := []string{"question", "add", "--prompt=Ship it? | really", "--options=Yes,No", "--context=blocked on review", "--dir=" + dir}
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", add) }); err != nil {
		t.Fatalf("question add: %v", err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"question", "list", dir}) })
	if err != nil || !strings.Contains(out, "[Q-001] Ship it? | really [PENDING]") ||
		!strings.Contains(out, "Options: Yes | No") || !strings.Contains(out, "Context: blocked on review") {
		t.Fatalf("question list lost a field: %q, %v", out, err)
	}

	// Negative: a question with no prompt is refused and records nothing.
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"question", "add", "--context=orphan", "--dir=" + dir})
	}); err == nil {
		t.Fatal("question add without --prompt succeeded")
	}
	// Boundary: without --context the list prints no Context line.
	if _, err := captureStdout(t, func() error {
		return dispatchCommand("state", []string{"question", "add", "--prompt=Plain", "--dir=" + dir})
	}); err != nil {
		t.Fatal(err)
	}
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"question", "list", dir}) })
	if err != nil || strings.Count(out, "Context:") != 1 || strings.Contains(out, "orphan") {
		t.Fatalf("unexpected context lines: %q, %v", out, err)
	}
}
