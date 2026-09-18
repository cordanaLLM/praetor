package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sidecarRecord = `{"context":"from sidecar","created_at":"2026-09-12T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z"}`

func TestBugSidecarWritersAndMixedForms(t *testing.T) {
	root, ledger := migrationLedger(t, migrationBugs[:2])
	added, err := AddBug(root, BugEntry{Title: "new", Context: "added context"})
	if err != nil {
		t.Fatal(err)
	}
	if err := ResolveBug(root, "BUG-001", "fixed"); err != nil {
		t.Fatal(err)
	}
	text := readLedgerFile(t, ledger)
	if strings.Count(text, bugMetadataRef) != 2 || strings.Count(text, bugMetadataPrefix) != 1 || !strings.Contains(text, legacyBugRow) {
		t.Fatalf("writers must write the sidecar form and leave other rows alone:\n%s", text)
	}
	bugs, err := ListBugs(root, "all")
	if err != nil || len(bugs) != 4 {
		t.Fatalf("mixed ledger unreadable: %+v, %v", bugs, err)
	}
	byID := map[string]BugEntry{}
	for _, bug := range bugs {
		byID[bug.ID] = bug
	}
	if byID[added.ID].Context != "added context" || byID["BUG-001"].Context != migrationBugs[0].Context ||
		byID["BUG-001"].ResolvedAt.IsZero() || byID["BUG-002"].Resolution != "fixed\nproof" || byID["BUG-003"].Title != "literal \\n & &#124;" {
		t.Fatalf("field lost across forms: %+v", byID)
	}
}

func TestBugSidecarRejectsInvalidMetadata(t *testing.T) {
	row := "| `BUG-001` | title | p1 | open | file |  | " + bugMetadataRef + "\n"
	cases := map[string]string{
		"missing sidecar":   "",
		"missing record":    `{"version":1,"bugs":{}}`,
		"wrong version":     `{"version":2,"bugs":{"BUG-001":` + sidecarRecord + `}}`,
		"no version":        `{"bugs":{"BUG-001":` + sidecarRecord + `}}`,
		"null bugs":         `{"version":1,"bugs":null}`,
		"unknown member":    `{"version":1,"bugs":{},"extra":1}`,
		"noncanonical id":   `{"version":1,"bugs":{"BUG-1":` + sidecarRecord + `}}`,
		"duplicate id":      `{"version":1,"bugs":{"BUG-001":` + sidecarRecord + `,"BUG-001":` + sidecarRecord + `}}`,
		"missing field":     `{"version":1,"bugs":{"BUG-001":{"context":"","created_at":"2026-09-12T10:00:00Z"}}}`,
		"null record":       `{"version":1,"bugs":{"BUG-001":null}}`,
		"nul in context":    `{"version":1,"bugs":{"BUG-001":{"context":"a\u0000b","created_at":"2026-09-12T10:00:00Z","resolved_at":"0001-01-01T00:00:00Z"}}}`,
		"not json":          `version 1`,
		"trailing garbage":  `{"version":1,"bugs":{}} x`,
		"oversized sidecar": `{"version":1,"bugs":{}}` + strings.Repeat(" ", maxBugLedgerBytes),
	}
	for name, sidecar := range cases {
		t.Run(name, func(t *testing.T) {
			root := writeBugFixture(t, defaultBugsMD()+row)
			if sidecar != "" {
				writeIntegrityFile(t, filepath.Join(root, WorkingDirName, bugMetaName), sidecar)
			}
			if bugs, err := ListBugs(root, "all"); err == nil {
				t.Fatalf("invalid metadata accepted: %+v", bugs)
			}
			if _, err := AddBug(root, BugEntry{Title: "blocked"}); err == nil {
				t.Fatal("writer accepted invalid metadata")
			}
		})
	}
	if _, err := ParseBugsMarkdownStrict(defaultBugsMD() + row); err == nil {
		t.Fatal("self-contained parser accepted a row whose metadata it cannot see")
	}
}

func TestBugSidecarValidAndBoundBinding(t *testing.T) {
	row := "| `BUG-001` | title | p1 | open | file |  | " + bugMetadataRef + "\n"
	root := writeBugFixture(t, defaultBugsMD()+row)
	path := filepath.Join(root, WorkingDirName, bugMetaName)
	writeIntegrityFile(t, path, `{"version":1,"bugs":{"BUG-001":`+sidecarRecord+`,"BUG-099":`+sidecarRecord+`}}`)
	bugs, err := ListBugs(root, "all")
	if err != nil || len(bugs) != 1 || bugs[0].Context != "from sidecar" {
		t.Fatalf("valid sidecar with an unreferenced record rejected: %+v, %v", bugs, err)
	}
	if _, err := SyncState(t.Context(), root, "bind sidecar"); err != nil {
		t.Fatal(err)
	}
	writeIntegrityFile(t, path, `{"version":1,"bugs":{"BUG-001":`+sidecarRecord+`}}`)
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("sidecar change did not stale the sync marker")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), root); err == nil {
		t.Fatal("sidecar removal did not stale the sync marker")
	}
}
