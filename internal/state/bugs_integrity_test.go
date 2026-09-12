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
