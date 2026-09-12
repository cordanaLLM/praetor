// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package hindsight

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDistillBugLedgerIntegrity(t *testing.T) {
	const header = "# Bug Ledger\n\n| ID | Title | Severity | Status | Location | Resolution |\n| --- | --- | --- | --- | --- | --- |\n"
	const resolved = "| `BUG-001` | Preserved finding | p1 | resolved | parser.go | Fixed |\n"
	t.Run("resolved evidence remains available", func(t *testing.T) {
		root := writeDistillerLedger(t, header+resolved)
		facts, err := distillStateFacts(context.Background(), root)
		if err != nil || len(facts) != 1 {
			t.Fatalf("expected one resolved fact, got %+v, %v", facts, err)
		}
		if facts[0].Subject != "BUG-001" || facts[0].Category != CategoryBugRuling {
			t.Fatalf("wrong resolved evidence: %+v", facts[0])
		}
	})
	t.Run("missing ledger remains an empty optional source", func(t *testing.T) {
		facts, err := distillStateFacts(context.Background(), t.TempDir())
		if err != nil || len(facts) != 0 {
			t.Fatalf("missing ledger: %+v, %v", facts, err)
		}
	})
	t.Run("malformed source prevents a partial success report", func(t *testing.T) {
		root := writeDistillerLedger(t, header+resolved+"| `BUG-002` | truncated row |\n")
		facts, err := distillStateFacts(context.Background(), root)
		if err == nil || facts != nil {
			t.Fatalf("malformed ledger became facts: %+v, %v", facts, err)
		}
		report, err := DistillWorkspace(context.Background(), root)
		if err == nil || report != nil {
			t.Fatalf("malformed ledger became a successful report: %+v, %v", report, err)
		}
	})
}

func writeDistillerLedger(t *testing.T, content string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".workingdir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "BUGS.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return root
}
