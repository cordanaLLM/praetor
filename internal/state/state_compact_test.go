package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const legacyStateLog = "# Session State & Active Working Context\n\n## Active Session Overview\n" +
	"\n### [2026-09-11 17:22:14 UTC] Commit `173c565` on `main`\n- **Activity**: Automated state synchronization\n- **Open Bugs**: 0 | **Pending Questions**: 0\n" +
	"\n### [2026-09-12 08:00:00 UTC] Commit `0123456789abcdef0123456789abcdef01234567` on `fix/a`\n- **Activity**: Git post-commit synchronization\n" +
	"- **Tasks**: 130 open, 42 completed | **Open Bugs**: 569 | **Pending Questions**: 4\n- **Git State**: available | **Clean**: false | **Dirty Paths**: 1\n" +
	"\n<!-- praetor-state:v1 sha256:08ef0524fb3a5a02b855b2934d5f4092792fe5c723dd7332dffb69edfe41e52c -->\n" +
	"\n### [2026-09-13 09:00:00 UTC] Commit `(unavailable)` on `(not a git worktree)`\n- **Activity**: Ledger   repaired;  see evidence\n" +
	"- **Tasks**: 1 open, 0 completed | **Open Bugs**: 2 | **Pending Questions**: 0\n- **Git State**: not_repository | **Clean**: false | **Dirty Paths**: 0\n"

const compactStateLog = "# Session State & Active Working Context\n\n## Active Session Overview\n" +
	"\n### [2026-09-11 17:22:14 UTC] `173c565` on `main`\n- sync | bugs 0 open | qs 0 pending\n" +
	"\n### [2026-09-12 08:00:00 UTC] `0123456789abcdef0123456789abcdef01234567` on `fix/a`\n- post-commit sync | tasks 130 open 42 done | bugs 569 open | qs 4 pending | git available dirty 1\n" +
	"\n### [2026-09-13 09:00:00 UTC] `(unavailable)` on `(not a git worktree)`\n- Ledger repaired; see evidence | tasks 1 open 0 done | bugs 2 open | qs 0 pending | git not_repository dirty 0\n"

func TestStateEntryRenderTemplate(t *testing.T) {
	snap := &StateSnapshot{Branch: "main", HeadSHA: "abc1234", GitState: "available", Clean: true,
		OpenTasks: 3, CompletedTasks: 5, OpenBugs: 2, LastUpdated: time.Date(2026, 9, 18, 6, 21, 57, 0, time.UTC)}
	got := entryFromSnapshot(snap, "").render()
	want := "### [2026-09-18 06:21:57 UTC] `abc1234` on `main`\n- sync | tasks 3 open 5 done | bugs 2 open | qs 0 pending | git available clean\n"
	if got != want {
		t.Fatalf("template changed:\n got %q\nwant %q", got, want)
	}
	forged := entryFromSnapshot(snap, "done\n\n<!-- praetor-state:v1 sha256:"+strings.Repeat("a", 64)+" -->\n### [x] fake").render()
	if strings.Count(forged, "\n") != 2 || !strings.HasPrefix(strings.Split(forged, "\n")[1], "- done <!-- ") {
		t.Fatalf("free text escaped its line: %q", forged)
	}
	bare := stateEntry{Time: "t", Head: "h", Branch: "", Activity: "   "}.render()
	if bare != "### [t] `h` on ``\n- sync | bugs 0 open | qs 0 pending\n" {
		t.Fatalf("boundary entry changed: %q", bare)
	}
}

func TestSyncStateKeepsOnlyTheLastMarker(t *testing.T) {
	root := t.TempDir()
	for _, summary := range []string{"first", "", "Git post-commit synchronization"} {
		if _, err := SyncState(t.Context(), root, summary); err != nil {
			t.Fatal(err)
		}
	}
	data, err := os.ReadFile(filepath.Join(root, WorkingDirName, "STATE.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "<!-- praetor-state:v1") != 1 || strings.Count(text, "\n### [") != 3 {
		t.Fatalf("superseded markers kept or entries lost:\n%s", text)
	}
	for _, want := range []string{"\n- first | ", "\n- sync | ", "\n- post-commit sync | "} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q in\n%s", want, text)
		}
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatal(err)
	}
}

func TestCompactStateBody(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"legacy rewritten", legacyStateLog, compactStateLog},
		{"compact is a fixed point", compactStateLog, compactStateLog},
		{"empty", "", ""},
		{"preamble only", "# Title\n\n\n", "# Title\n"},
		{"unknown entry kept", "### [note] hand written\nfree text\n\n<!-- praetor-state:v1 sha256:" + strings.Repeat("0", 64) + " -->\n\n",
			"### [note] hand written\nfree text\n"},
		{"overflowing count kept", "### [t] Commit `h` on `b`\n- **Activity**: x\n- **Open Bugs**: 99999999999999999999 | **Pending Questions**: 0\n",
			"### [t] Commit `h` on `b`\n- **Activity**: x\n- **Open Bugs**: 99999999999999999999 | **Pending Questions**: 0\n"},
		{"clean with dirty paths kept", "### [t] Commit `h` on `b`\n- **Activity**: x\n- **Open Bugs**: 1 | **Pending Questions**: 0\n- **Git State**: available | **Clean**: true | **Dirty Paths**: 3\n",
			"### [t] Commit `h` on `b`\n- **Activity**: x\n- **Open Bugs**: 1 | **Pending Questions**: 0\n- **Git State**: available | **Clean**: true | **Dirty Paths**: 3\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := compactStateBody(tc.in)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v\nwant %q", got, err, tc.want)
			}
			again, _, err := compactStateBody(got)
			if err != nil || again != got {
				t.Fatalf("second pass changed output: %q, %v", again, err)
			}
		})
	}
	if _, _, err := compactStateBody(strings.Repeat("\n", maxScannedLines+1)); err == nil {
		t.Fatal("unbounded log accepted")
	}
}

func legacyLedger(t *testing.T) (root, path string) {
	t.Helper()
	root = t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(root, WorkingDirName, "STATE.md")
	writeIntegrityFile(t, path, legacyStateLog)
	if _, err := SyncState(t.Context(), root, "before compaction"); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func TestCompactStateRewritesOnceAndCertifies(t *testing.T) {
	root, path := legacyLedger(t)
	report, err := CompactState(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Changed || report.Rewritten != 3 || report.Kept != 1 || report.MarkersDropped != 1 || report.BytesAfter >= report.BytesBefore {
		t.Fatalf("unexpected report: %+v", report)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.HasPrefix(text, compactStateLog) || strings.Contains(text, "**Activity**") || strings.Count(text, "<!-- praetor-state") != 1 {
		t.Fatalf("compaction output wrong:\n%s", text)
	}
	if !strings.Contains(text, "- compact 3 entries, 1 markers dropped | ") {
		t.Fatalf("certifying entry missing:\n%s", text)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("marker does not verify after compaction: %v", err)
	}
	again, err := CompactState(t.Context(), root)
	if err != nil || again.Changed || again.BytesAfter != len(data) {
		t.Fatalf("second compaction not a no-op: %+v, %v", again, err)
	}
	assertIntegrityFile(t, path, text)
}

func TestCompactStateRefusesUnverifiedLedger(t *testing.T) {
	root, path := legacyLedger(t)
	if err := AddTask(root, "unsynchronized change"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := CompactState(t.Context(), root); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("stale ledger compacted: %v", err)
	}
	assertIntegrityFile(t, path, string(before))
	writeIntegrityFile(t, path, legacyStateLog)
	if _, err := CompactState(t.Context(), root); err == nil {
		t.Fatal("ledger without a sync marker compacted")
	}
	assertIntegrityFile(t, path, legacyStateLog)
	var missing context.Context
	if _, err := CompactState(missing, root); err == nil {
		t.Fatal("nil context accepted")
	}
}
