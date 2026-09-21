package adr

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const verifierCapacityFixture = "# ADR capacity fixture\n\n" +
	"```adr-constraint\n" +
	"id: capacity-fixture\n" +
	"kind: forbidden-path\n" +
	"forbids: [\"forbidden.txt\"]\n" +
	"rationale: Every loaded constraint must be replayed.\n" +
	"```\n"

func writeDecisionRecords(t *testing.T, root string, count int) {
	t.Helper()
	if count < 0 || count > maxRecords+1 {
		t.Fatalf("unsafe decision-record fixture count: %d", count)
	}
	directory := filepath.Join(root, recordDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatalf("creating decision-record directory: %v", err)
	}
	for i := 0; i < count; i++ {
		name := filepath.Join(directory, fmt.Sprintf("%04d-capacity.md", i))
		if err := os.WriteFile(name, []byte(verifierCapacityFixture), 0o600); err != nil {
			t.Fatalf("writing decision record %d: %v", i, err)
		}
	}
}

func runVerifierGit(t *testing.T, root string, args ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := util.RunGit(ctx, root, args...); err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
}

func TestVerifyPositiveReplaysMoreThanLegacyRecordLimit(t *testing.T) {
	const records = 513
	root := t.TempDir()
	writeDecisionRecords(t, root, records)
	if err := os.WriteFile(filepath.Join(root, "forbidden.txt"), []byte("present\n"), 0o600); err != nil {
		t.Fatalf("writing forbidden fixture: %v", err)
	}
	runVerifierGit(t, root, "init")
	runVerifierGit(t, root, "add", "docs/adr", "forbidden.txt")

	report, err := Verify(context.Background(), root)
	if err != nil {
		t.Fatalf("verifying %d records: %v", records, err)
	}
	if report.Records != records || report.Constraints != records || len(report.Findings) != records {
		t.Fatalf("verification truncated: records=%d constraints=%d findings=%d",
			report.Records, report.Constraints, len(report.Findings))
	}
}

func TestVerifyNegativeRequiresContext(t *testing.T) {
	var nilContext context.Context
	if _, err := Verify(nilContext, t.TempDir()); err == nil {
		t.Fatal("nil context was accepted")
	}
}

func TestVerifyBoundaryRejectsTooManyDecisionRecords(t *testing.T) {
	root := t.TempDir()
	writeDecisionRecords(t, root, maxRecords+1)
	_, _, err := loadConstraints(context.Background(), root)
	if err == nil || !strings.Contains(err.Error(), "decision record inventory exceeds") {
		t.Fatalf("oversized decision-record inventory was accepted: %v", err)
	}
}
