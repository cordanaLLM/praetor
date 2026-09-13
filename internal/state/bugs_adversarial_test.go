// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package state

import (
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBugAdversarialConcurrentWriters(t *testing.T) {
	root := t.TempDir()
	if _, err := AddBug(root, BugEntry{Title: "initial"}); err != nil {
		t.Fatal(err)
	}
	const count = 24
	type outcome struct {
		bug *BugEntry
		err error
	}
	results := make(chan outcome, count)
	var wg sync.WaitGroup
	wg.Add(count)
	for i := 0; i < count; i++ {
		go func() {
			defer wg.Done()
			bug, err := AddBug(root, BugEntry{Title: "concurrent"})
			results <- outcome{bug, err}
		}()
	}
	wg.Wait()
	close(results)
	success := map[string]bool{}
	for result := range results {
		if result.err != nil {
			if !strings.Contains(result.err.Error(), "busy") {
				t.Errorf("writer failed for a reason other than contention: %v", result.err)
			}
			continue
		}
		if success[result.bug.ID] {
			t.Errorf("duplicate ID returned successfully: %s", result.bug.ID)
		}
		success[result.bug.ID] = true
	}
	if len(success) == 0 {
		t.Fatal("no writer succeeded")
	}
	bugs, err := ListBugs(root, "all")
	if err != nil || len(bugs) != len(success)+1 {
		t.Fatalf("successful writes lost: success=%v bugs=%v err=%v", success, bugs, err)
	}
	for _, bug := range bugs[1:] {
		if !success[bug.ID] {
			t.Errorf("unknown written record: %s", bug.ID)
		}
	}
}

func TestBugAdversarialMetadataContract(t *testing.T) {
	const row = "| `BUG-001` | title | p1 | open | file |  | "
	const created = `"created_at":"2026-09-12T10:00:00Z"`
	const resolved = `"resolved_at":"0001-01-01T00:00:00Z"`
	const header = "# Bug Ledger\n\n| ID | Title | Severity | Status | Location | Resolution |\n| --- | --- | --- | --- | --- | --- |\n"
	markdown := func(payload string) string {
		return header + row + "<!-- praetor-bug:v1 " + base64.StdEncoding.EncodeToString([]byte(payload)) + " -->\n"
	}
	valid := `{"context":"evidence",` + created + `,` + resolved + `}`
	bugs, err := ParseBugsMarkdownStrict(markdown(valid))
	if err != nil || len(bugs) != 1 || bugs[0].Context != "evidence" {
		t.Fatalf("complete metadata must remain readable: %+v, %v", bugs, err)
	}
	for _, payload := range []string{
		`{}`,
		`{"context":null,` + created + `,` + resolved + `}`,
		`{"context":"",` + created + `}`,
		`{"context":"",` + created + `,` + resolved + `,"unknown":1}`,
		`{"context":"one","context":"two",` + created + `,` + resolved + `}`,
		`{"Context":"wrong case",` + created + `,` + resolved + `}`,
		`{"context":"",` + created + `,` + resolved + `,"Created_at":"2026-09-13T10:00:00Z"}`,
		`null`,
		`[]`,
	} {
		t.Run(payload, func(t *testing.T) {
			bugs, err := ParseBugsMarkdownStrict(markdown(payload))
			if err == nil || bugs != nil {
				t.Fatalf("invalid metadata accepted: %+v, %v", bugs, err)
			}
		})
	}
}

func TestBugAdversarialMissingRootIsNotAnEmptyLedger(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing-repository")
	bugs, err := ListBugsContext(context.Background(), root, "all")
	if err == nil || bugs != nil {
		t.Fatalf("missing repository became empty successful evidence: %+v, %v", bugs, err)
	}
}
