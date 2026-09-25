package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

func TestBugIntegrityPendingAndLockPreserveSource(t *testing.T) {
	rootPath := writeBugFixture(t, defaultBugsMD())
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root, err := contextopt.OpenDirectoryIn(ctx, rootPath, WorkingDirName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	unlock, err := lockBugLedger(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AddBug(rootPath, BugEntry{Title: "blocked writer"}); err == nil {
		t.Error("concurrent writer acquired held lock")
	}
	if err := unlock(); err != nil {
		t.Fatal(err)
	}
	if err := root.WriteFile(bugPendingName, []byte("retained interrupted candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := AddBug(rootPath, BugEntry{Title: "must not overwrite"}); err == nil {
		t.Error("overwrote pending candidate")
	}
	data, err := root.ReadFile(bugLedgerName)
	if err != nil || string(data) != defaultBugsMD() {
		t.Fatalf("source changed: %s %v", data, err)
	}
	pending, err := root.ReadFile(bugPendingName)
	if err != nil || string(pending) != "retained interrupted candidate" {
		t.Fatalf("pending evidence changed: %s %v", pending, err)
	}
}

func TestBugIntegrityDetectsChangedSource(t *testing.T) {
	rootPath := writeBugFixture(t, defaultBugsMD())
	path := filepath.Join(rootPath, WorkingDirName, bugLedgerName)
	external := defaultBugsMD() + "\nExternal edit\n"
	err := updateBugLedger(rootPath, func(doc *bugDocument) (string, error) {
		if err := os.WriteFile(path, []byte(external), 0o600); err != nil {
			return "", err
		}
		return doc.text + "\nOur edit\n", nil
	})
	if err == nil {
		t.Fatal("concurrent external edit was overwritten")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != external {
		t.Fatalf("external edit lost: %s %v", got, err)
	}
}

func TestBugIntegrityFieldBoundsAndUnicode(t *testing.T) {
	cases := []string{"\u2003title\u00a0", "\u0085title\v\f", strings.Repeat("\n", maxLedgerFieldBytes)}
	for _, value := range cases {
		bug := BugEntry{ID: "BUG-001", Title: "boundary", Severity: "p1", Status: "open", Context: value, Resolution: value}
		encoded, err := RenderBugsMarkdownStrict([]BugEntry{bug})
		if err != nil {
			t.Fatal(err)
		}
		got, err := ParseBugsMarkdownStrict(encoded)
		if err != nil || len(got) != 1 || got[0].Context != value || got[0].Resolution != value {
			t.Fatalf("boundary round trip failed: %v", err)
		}
	}
	root := writeBugFixture(t, defaultBugsMD())
	if _, err := AddBug(root, BugEntry{Title: strings.Repeat("x", maxLedgerFieldBytes+1)}); err == nil {
		t.Fatal("accepted oversized field")
	}
	if _, err := ParseBugsMarkdownStrict(strings.Repeat("x", maxLedgerBytes+1)); err == nil {
		t.Fatal("accepted oversized ledger")
	}
}

func TestBugIntegrityIgnoresExamplesAndOtherTables(t *testing.T) {
	examples := "```markdown\n| `BUG-777` | not a record | p0 | open | | |\n```\n\n"
	other := "\n| Note | Reference |\n| --- | --- |\n| See BUG-001 | history |\n"
	root := writeBugFixture(t, examples+defaultBugsMD()+other)
	if _, err := AddBug(root, BugEntry{Title: "real record"}); err != nil {
		t.Fatal(err)
	}
	bugs, err := ListBugs(root, "all")
	if err != nil || len(bugs) != 1 || bugs[0].ID != "BUG-001" {
		t.Fatalf("examples became records: %#v %v", bugs, err)
	}
}

func FuzzBugRecordRoundTrip(f *testing.F) {
	for _, seed := range []string{"title | branch", "\\n---\\n", "\u0085 & &#10; λ\r\n"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, value string) {
		bug := BugEntry{ID: "BUG-001", Title: "fuzz", Context: value, Location: value, Resolution: value, Severity: "p0", Status: "open"}
		encoded, err := RenderBugsMarkdownStrict([]BugEntry{bug})
		if err != nil {
			return
		}
		got, err := ParseBugsMarkdownStrict(encoded)
		if err != nil || len(got) != 1 || got[0].Context != value || got[0].Location != value || got[0].Resolution != value {
			t.Fatalf("accepted record failed round trip: %#v %v", got, err)
		}
	})
}
