package decisions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adr"
)

func TestTransform(t *testing.T) {
	cases := []struct{ name, input, want string }{
		{"options heading", "## Considered Options\n\ntext\n", "## Alternatives Considered\n\ntext\n"},
		{"superseded note", "## Status\n\nSuperseded by ADR-0012 on 2026-09-26.\n\n## Context", "## Status\n\nSuperseded\n\n## Status Note\n\nSuperseded by ADR-0012 on 2026-09-26.\n\n## Context"},
		{"bare accepted", "## Status\n\nAccepted\n", "## Status\n\nAccepted\n"},
		{"no status", "## Context\n\nDecision text\n", "## Context\n\nDecision text\n"},
		{"proposed note", "## Status\n\nproposed for review\n", "## Status\n\nProposed\n\n## Status Note\n\nproposed for review\n"},
		{"deprecated note", "## Status\n\nDeprecated in favor of ADR-0002\n", "## Status\n\nDeprecated\n\n## Status Note\n\nDeprecated in favor of ADR-0002\n"},
		{"unrecognized status", "## Status\n\nUnder Review\n", "## Status\n\nUnder Review\n"},
		{"nonmatching option heading", "## Options and Context\n", "## Options and Context\n"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if got := Transform(test.input); got != test.want {
				t.Fatalf("Transform() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestExportSkipsScaffoldsAndRebuildsCorpus(t *testing.T) {
	repo := t.TempDir()
	adrDir := filepath.Join(repo, filepath.FromSlash(adr.RecordDirectory))
	mustWriteDecision(t, filepath.Join(adrDir, "README.md"), "index")
	mustWriteDecision(t, filepath.Join(adrDir, "0000-template.md"), "template")
	mustWriteDecision(t, filepath.Join(adrDir, "0001-choice.md"), "## Considered Options\n\nA\n")
	out := filepath.Join(repo, ".standards", "cache", "decisions")
	report, err := Export(t.Context(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Written) != 1 || len(report.Skipped) != 2 {
		t.Fatalf("report = %+v", report)
	}
	written, err := os.ReadFile(filepath.Join(out, "decisions", "adr", "adr-0001-choice.md"))
	if err != nil || !strings.Contains(string(written), "## Alternatives Considered") {
		t.Fatalf("exported corpus = %q, err %v", written, err)
	}
	mustWriteDecision(t, filepath.Join(out, "decisions", "stale.md"), "stale")
	if _, err := Export(t.Context(), repo, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(out, "decisions", "stale.md")); !os.IsNotExist(err) {
		t.Fatalf("stale file survived rebuild: %v", err)
	}
}

func TestExportPathLimitsAndCancellation(t *testing.T) {
	repo := t.TempDir()
	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := Export(t.Context(), missing, ""); err == nil {
		t.Fatal("nonexistent repository accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("nonexistent repository was created: %v", err)
	}
	outside := t.TempDir()
	if _, err := Export(t.Context(), repo, ""); err != nil {
		t.Fatalf("default export directory failed: %v", err)
	}
	if _, err := Export(t.Context(), repo, outside); err != nil {
		t.Fatalf("explicit outside directory rejected: %v", err)
	}
	adrDir := filepath.Join(repo, filepath.FromSlash(adr.RecordDirectory))
	mustWriteDecision(t, filepath.Join(adrDir, "0001-large.md"), strings.Repeat("x", MaxRecordBytes+1))
	if _, err := Export(t.Context(), repo, ""); err == nil {
		t.Fatal("oversized ADR accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Export(ctx, repo, ""); err == nil {
		t.Fatal("cancelled export succeeded")
	}
}

func TestExportRecordCountBoundary(t *testing.T) {
	repo := t.TempDir()
	adrDir := filepath.Join(repo, filepath.FromSlash(adr.RecordDirectory))
	for i := 0; i < MaxRecords; i++ {
		mustWriteDecision(t, filepath.Join(adrDir, fmt.Sprintf("%04d.md", i)), "record")
	}
	if report, err := Export(t.Context(), repo, ""); err != nil || len(report.Written) != MaxRecords {
		t.Fatalf("boundary export records=%d err=%v", len(report.Written), err)
	}
	mustWriteDecision(t, filepath.Join(adrDir, "zzzz.md"), "record")
	if _, err := Export(t.Context(), repo, ""); err == nil {
		t.Fatal("record beyond bound accepted")
	}
}

func mustWriteDecision(t *testing.T, name, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
