package operationalsync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/adr"
)

// maxShippedRecords bounds the scan of the engine's own decision records (HISS-02).
const maxShippedRecords = 4096

// ownerOnlyProbe is a tracked path an operational fork may carry under prefix: a file inside a
// directory prefix, the file itself otherwise. It is the probe TestOwnerOnlyPrefixesAreIgnoredByTheEngine
// uses, so both tests describe the same fork tree.
func ownerOnlyProbe(prefix string) string {
	if strings.HasSuffix(prefix, "/") {
		return prefix + "nested/operator.yaml"
	}
	return prefix
}

// shippedDecisionRecords reads every decision record the engine ships, keyed by its name.
func shippedDecisionRecords(t *testing.T) map[string]string {
	t.Helper()
	directory := filepath.Join("..", "..", "docs", "adr")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("reading shipped decision records: %v", err)
	}
	if len(entries) > maxShippedRecords {
		t.Fatalf("shipped decision records exceed %d entries", maxShippedRecords)
	}
	records := make(map[string]string, len(entries))
	for i := 0; i < len(entries) && i < maxShippedRecords; i++ {
		name := entries[i].Name()
		if entries[i].IsDir() || !strings.HasSuffix(name, ".md") || name == "README.md" {
			continue
		}
		body, readErr := os.ReadFile(filepath.Join(directory, name))
		if readErr != nil {
			t.Fatalf("reading %s: %v", name, readErr)
		}
		records[name] = string(body)
	}
	return records
}

// verifyForkShapedRecords replays records the way an operational fork's `make verify-all` does:
// `adr verify` over a tree that tracks the records and one probe per owner-only prefix.
func verifyForkShapedRecords(t *testing.T, records map[string]string) *adr.Report {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	g, err := newGit(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	testGit(t, g, "", "init", "--template=", repo)
	for name, body := range records {
		testWrite(t, repo, filepath.Join("docs", "adr", name), body)
	}
	for _, prefix := range ownerOnlyPrefixes {
		testWrite(t, repo, ownerOnlyProbe(prefix), "operator: data\n")
	}
	testGit(t, g, repo, "add", "--all")
	report, err := adr.Verify(ctx, repo)
	if err != nil {
		t.Fatalf("replaying decision records in a fork-shaped tree: %v", err)
	}
	return report
}

func forbiddingRecord(pattern string) map[string]string {
	return map[string]string{"0001-fixture.md": "# ADR-0001: Fixture\n\n```adr-constraint\n" +
		"id: fixture-forbidden-path\nkind: forbidden-path\nforbids:\n  - \"" + pattern + "\"\n" +
		"rationale: Fixture for the fork-shaped replay.\n```\n"}
}

// Every shipped decision record reaches the operational fork through the upstream sync, and the
// fork's `make verify-all` replays it against a tree that tracks the owner-only paths. A record
// that forbids one of those paths passes in the engine and fails every fork CI run after the sync.
func TestShippedDecisionRecordsAllowOwnerOnlyPaths(t *testing.T) {
	records := shippedDecisionRecords(t)
	if len(records) == 0 {
		t.Fatal("no shipped decision record was read")
	}
	report := verifyForkShapedRecords(t, records)
	if report.Records != len(records) {
		t.Fatalf("replayed %d records, want %d", report.Records, len(records))
	}
	for _, finding := range report.Findings {
		t.Errorf("shipped record contradicts the operational fork's owner-only paths: %s", finding)
	}
}

// Negative: the fork-shaped replay reports a record that forbids any owner-only prefix, so the
// shipped-record test above cannot pass because the probes are missing.
func TestForkShapedReplayReportsAForbiddenOwnerOnlyPath(t *testing.T) {
	for _, prefix := range ownerOnlyPrefixes {
		report := verifyForkShapedRecords(t, forbiddingRecord(prefix))
		if len(report.Findings) != 1 || !strings.Contains(report.Findings[0].Detail, ownerOnlyProbe(prefix)) {
			t.Errorf("record forbidding %q: findings %v, want one on %s", prefix, report.Findings, ownerOnlyProbe(prefix))
		}
	}
}

// Boundary: a record forbidding a path next to an owner-only prefix, which the fork does not
// carry, replays clean in the fork-shaped tree.
func TestForkShapedReplayAllowsAPathBesideOwnerOnlyPrefixes(t *testing.T) {
	for _, neighbour := range []string{"deploy/helm/", ".config/orgsx/", ".config/fleet.yml"} {
		report := verifyForkShapedRecords(t, forbiddingRecord(neighbour))
		if report.Constraints != 1 || len(report.Findings) != 0 {
			t.Errorf("record forbidding %q: constraints %d, findings %v, want 1 and none", neighbour, report.Constraints, report.Findings)
		}
	}
}
