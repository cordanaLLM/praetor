package needs

import (
	"context"
	"path/filepath"
	"testing"
)

func TestAggregateFleetSkipsUnanalyzableRepositories(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("svc", "go.mod"),
		"module example.com/svc\ngo 1.24\nrequire github.com/jackc/pgx v5.0.0\n")
	// A docs repository carrying only a praetor manifest: discovered, but no language
	// analyzer recognises it, so it must never be ranked as a 100% ready Go repository.
	writeFixture(t, root, filepath.Join("docs", ".standards.yaml"),
		"repository:\n  name: docs\n  owner: acme\n")

	report, err := AggregateFleet(context.Background(), root, "")
	if err != nil {
		t.Fatalf("aggregate fleet failed: %v", err)
	}
	if report.ScannedRepositories != 1 {
		t.Fatalf("expected exactly 1 scanned repository, got %d", report.ScannedRepositories)
	}
	if len(report.SkippedRepositories) != 1 {
		t.Fatalf("expected the docs repository to be recorded as skipped, got %v",
			report.SkippedRepositories)
	}
	for _, entry := range report.Leaderboard {
		if entry.Repository == "docs" {
			t.Fatal("an unanalysable repository must not appear on the leaderboard")
		}
	}
}

func TestAggregateFleetNegativeCancelledContext(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("svc", "go.mod"), "module example.com/svc\ngo 1.24\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := AggregateFleet(ctx, root, ""); err == nil {
		t.Fatal("expected a cancelled aggregation to fail rather than report empty repos")
	}
}

func TestAggregateFleetBoundaryEmptyRoot(t *testing.T) {
	report, err := AggregateFleet(context.Background(), t.TempDir(), "")
	if err != nil {
		t.Fatalf("aggregate on an empty root failed: %v", err)
	}
	if report.TotalRepositories != 0 || report.ScannedRepositories != 0 {
		t.Fatalf("expected an empty report, got %d/%d",
			report.ScannedRepositories, report.TotalRepositories)
	}
	if report.OverallFleetCoverage != 100.0 {
		t.Fatalf("expected 100%% coverage for zero dependencies, got %.1f", report.OverallFleetCoverage)
	}
}
