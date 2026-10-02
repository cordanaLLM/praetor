package state

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func writeBugFixture(t *testing.T, text string) string {
	t.Helper()
	root := t.TempDir()
	if err := InitWorkingDir(root); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, WorkingDirName, "BUGS.md"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestBugIntegrityFullRecordRoundTrip(t *testing.T) {
	want := BugEntry{Title: "  pipes | slashes \\n & &#124;\nUnicode λ\r\nend  ",
		Severity: "p0", Status: "investigating", Location: "path|name:1",
		Context: "context\nwith | evidence", CreatedAt: time.Date(2026, 9, 12, 12, 1, 2, 3, time.UTC)}
	root := t.TempDir()
	added, err := AddBug(root, want)
	if err != nil {
		t.Fatal(err)
	}
	want.ID = added.ID
	got, err := ListBugs(root, "all")
	if err != nil || len(got) != 1 {
		t.Fatalf("lost record: %v, %v", got, err)
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("record changed: got %#v, want %#v", got[0], want)
	}
	if err := ResolveBug(root, want.ID, "fixed | with\nproof"); err != nil {
		t.Fatal(err)
	}
	got, err = ListBugs(root, "resolved")
	if err != nil || len(got) != 1 || got[0].Resolution != "fixed | with\nproof" || got[0].ResolvedAt.IsZero() || got[0].Context != want.Context {
		t.Fatalf("resolution metadata lost: %#v, %v", got, err)
	}
}

func TestBugIntegrityLegacyAndProsePreserved(t *testing.T) {
	legacy := "| `BUG-010` | literal \\n---\\n & &#124; | p1 | open | file:1 |  |\r\n"
	before := "# Notes\r\n\r\n" + strings.ReplaceAll(defaultBugsMD(), "\n", "\r\n") + legacy + "\r\n## Human notes\r\nKeep this exactly."
	root := writeBugFixture(t, before)
	bug, err := AddBug(root, BugEntry{Title: "new record"})
	if err != nil || bug.ID != "BUG-011" {
		t.Fatalf("ID must exceed greatest existing ID: %#v %v", bug, err)
	}
	data, err := os.ReadFile(filepath.Join(root, WorkingDirName, "BUGS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), legacy) || !strings.HasPrefix(string(data), "# Notes\r\n\r\n") || !strings.HasSuffix(string(data), "\r\n## Human notes\r\nKeep this exactly.") {
		t.Fatalf("untouched source spans changed: %q", data)
	}
	bugs, err := ListBugs(root, "all")
	if err != nil || len(bugs) != 2 || bugs[0].Title != "literal \\n---\\n & &#124;" {
		t.Fatalf("legacy escape semantics changed: %#v %v", bugs, err)
	}
}

func TestBugIntegrityRejectsMalformedWithoutWriting(t *testing.T) {
	rows := []string{
		"| `BUG-001` | hidden | P0 | p0 | open | source | |\n",
		"| `BUG-001` | malformed | p0 | open |\n",
		"| `BUG-001` | bad severity | p9 | open | source | |\n",
		"| `BUG-001` | bad status | p0 | invisible | source | |\n",
		"| `BUG-001` | one | p0 | open | source | |\n| `BUG-001` | two | p1 | open | source | |\n",
		"| `BUG-001` | one | p0 | open | source | |\n| `BUG-1` | alias | p1 | open | source | |\n",
	}
	for _, row := range rows {
		t.Run(row, func(t *testing.T) {
			original := defaultBugsMD() + row
			root := writeBugFixture(t, original)
			if bugs, err := ListBugs(root, "all"); err == nil {
				t.Fatalf("malformed ledger accepted: %#v", bugs)
			}
			if _, err := AddBug(root, BugEntry{Title: "new"}); err == nil {
				t.Error("add accepted malformed ledger")
			}
			if err := ResolveBug(root, "BUG-001", "fixed"); err == nil {
				t.Error("resolve accepted malformed ledger")
			}
			data, err := os.ReadFile(filepath.Join(root, WorkingDirName, "BUGS.md"))
			if err != nil || string(data) != original {
				t.Fatalf("failure changed ledger bytes: %v", err)
			}
		})
	}
}

func TestBugRefusesUnterminatedCodeFence(t *testing.T) {
	// Negative: fence between rows hides later rows on unpatched parser and leads to ID reuse.
	const fenceBetweenRows = "# Bug Ledger\n\n> Defects tracked by autonomous agents.\n\n" +
		"| ID | Title | Severity | Status | Location | Resolution |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| `BUG-001` | First | p2 | open | path:1 | |\n" +
		"```sh\nexample\n" +
		"| `BUG-002` | Second | p2 | open | path:2 | |\n"

	root := writeBugFixture(t, fenceBetweenRows)
	bugs, err := ListBugs(root, "all")
	if err == nil {
		t.Fatalf("unterminated fence silently hid rows: %+v", bugs)
	}
	if !strings.Contains(err.Error(), "BUGS.md line 8: code fence opened and never closed") {
		t.Fatalf("error does not locate fence opening line: %v", err)
	}
	if _, err := AddBug(root, BugEntry{Title: "Third"}); err == nil {
		t.Fatal("addition accepted ledger with unterminated fence")
	} else if !strings.Contains(err.Error(), "BUGS.md line 8: code fence opened and never closed") {
		t.Fatalf("addition error does not locate fence: %v", err)
	}
	if err := ResolveBug(root, "BUG-001", "fixed"); err == nil {
		t.Fatal("resolve accepted ledger with unterminated fence")
	} else if !strings.Contains(err.Error(), "BUGS.md line 8: code fence opened and never closed") {
		t.Fatalf("resolve error does not locate fence: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, WorkingDirName, "BUGS.md"))
	if err != nil || string(data) != fenceBetweenRows {
		t.Fatalf("refused addition changed ledger: %v", err)
	}

	// Boundary: trailing unterminated fence after all rows.
	const trailingFence = "# Bug Ledger\n\n> Defects tracked by autonomous agents.\n\n" +
		"| ID | Title | Severity | Status | Location | Resolution |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| `BUG-001` | First | p2 | open | path:1 | |\n" +
		"| `BUG-002` | Second | p2 | open | path:2 | |\n\n" +
		"```markdown\ntrailing unclosed fence\n"

	trailingRoot := writeBugFixture(t, trailingFence)
	if _, err := ListBugs(trailingRoot, "all"); err == nil {
		t.Fatal("trailing unterminated fence was accepted")
	} else if !strings.Contains(err.Error(), "BUGS.md line 10: code fence opened and never closed") {
		t.Fatalf("trailing fence error does not locate line 10: %v", err)
	}
	if _, err := AddBug(trailingRoot, BugEntry{Title: "Third"}); err == nil {
		t.Fatal("addition accepted ledger with trailing unterminated fence")
	}

	// Boundary: unterminated fence opened at line 1 before table header.
	const preambleFence = "```sh\nnotes\n" +
		"# Bug Ledger\n\n| ID | Title | Severity | Status | Location | Resolution |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| `BUG-001` | First | p2 | open | path:1 | |\n"

	preambleRoot := writeBugFixture(t, preambleFence)
	if _, err := ListBugs(preambleRoot, "all"); err == nil {
		t.Fatal("preamble unterminated fence was accepted")
	} else if !strings.Contains(err.Error(), "BUGS.md line 1: code fence opened and never closed") {
		t.Fatalf("preamble fence error does not locate line 1: %v", err)
	}

	// Boundary: closed first fence followed by second unclosed tilde fence.
	const multiFence = "# Bug Ledger\n\n" +
		"```text\nfirst closed block\n```\n\n" +
		"| ID | Title | Severity | Status | Location | Resolution |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| `BUG-001` | First | p2 | open | path:1 | |\n\n" +
		"~~~sh\nsecond unclosed block\n"

	multiRoot := writeBugFixture(t, multiFence)
	if _, err := ListBugs(multiRoot, "all"); err == nil {
		t.Fatal("second unclosed fence accepted")
	} else if !strings.Contains(err.Error(), "BUGS.md line 11: code fence opened and never closed") {
		t.Fatalf("multi fence error does not locate line 11: %v", err)
	}

	// Boundary / Strict: ParseBugsMarkdownStrict also returns the line error.
	if _, err := ParseBugsMarkdownStrict(fenceBetweenRows); err == nil {
		t.Fatal("ParseBugsMarkdownStrict accepted unterminated fence")
	} else if !strings.Contains(err.Error(), "BUGS.md line 8: code fence opened and never closed") {
		t.Fatalf("strict parse error does not locate fence: %v", err)
	}

	// Positive: closed fence inside notes does not hide rows, and next add allocates next ID.
	const closedFence = "# Bug Ledger\n\n> Defects tracked by autonomous agents.\n\n" +
		"| ID | Title | Severity | Status | Location | Resolution |\n" +
		"| :--- | :--- | :--- | :--- | :--- | :--- |\n" +
		"| `BUG-001` | First | p2 | open | path:1 | |\n\n" +
		"```markdown\n| `BUG-999` | example row | p0 | open | | |\n```\n\n" +
		"## Notes\nKeep notes.\n"

	closedRoot := writeBugFixture(t, closedFence)
	closedBugs, err := ListBugs(closedRoot, "all")
	if err != nil || len(closedBugs) != 1 || closedBugs[0].ID != "BUG-001" {
		t.Fatalf("closed fence corrupted bug listing: %v, %v", closedBugs, err)
	}
	added, err := AddBug(closedRoot, BugEntry{Title: "Second"})
	if err != nil {
		t.Fatalf("add bug refused closed fence: %v", err)
	}
	if added.ID != "BUG-002" {
		t.Fatalf("expected BUG-002, got %s", added.ID)
	}
	updatedBugs, err := ListBugs(closedRoot, "all")
	if err != nil || len(updatedBugs) != 2 || updatedBugs[1].ID != "BUG-002" {
		t.Fatalf("updated bugs list unexpected: %v, %v", updatedBugs, err)
	}
}
