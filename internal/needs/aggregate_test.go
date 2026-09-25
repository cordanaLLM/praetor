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

// TestDiscoverFleetReposRecognisesEveryAnalyzerManifest pins BUG-864: a CMake-only and a
// setup.py-only repository are discovered, because the native and Python analyzers
// already detect them; root-level scratch/ and dot-directories stay out of the walk.
func TestDiscoverFleetReposRecognisesEveryAnalyzerManifest(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("native", "CMakeLists.txt"), "find_package(OpenSSL REQUIRED)\n")
	writeFixture(t, root, filepath.Join("pylib", "setup.py"), "setup(install_requires=['requests'])\n")
	writeFixture(t, root, filepath.Join("scratch", "trial", "go.mod"), "module example.com/trial\n")
	writeFixture(t, root, filepath.Join(".workingdir", "go.mod"), "module example.com/ledger\n")

	dirs, err := discoverFleetRepos(context.Background(), root)
	if err != nil {
		t.Fatalf("discoverFleetRepos() error = %v", err)
	}
	want := map[string]bool{filepath.Join(root, "native"): true, filepath.Join(root, "pylib"): true}
	if len(dirs) != len(want) {
		t.Fatalf("discovered %v, want exactly %v", dirs, want)
	}
	for _, dir := range dirs {
		if !want[dir] {
			t.Errorf("unexpected repository %s", dir)
		}
	}
}

// TestAggregateFleetWithHarvestSkipsUnsupportedLanguage: a harvested repository with no
// language signal is counted and listed as skipped, never scored as a Go repository.
func TestAggregateFleetWithHarvestSkipsUnsupportedLanguage(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, filepath.Join("svc", "go.mod"), "module example.com/svc\ngo 1.24\n")
	harvest := t.TempDir()
	writeFixture(t, harvest, filepath.Join("dev-inventory", "dev-inventory.json"),
		`[{"Name": "plain-service", "Type": "Git"}, {"Name": "plain-service", "Type": "Git"}]`)

	report, err := AggregateFleetWithHarvest(context.Background(), root, "", harvest)
	if err != nil {
		t.Fatalf("AggregateFleetWithHarvest() error = %v", err)
	}
	if report.ScannedRepositories != 1 || report.TotalRepositories != 2 {
		t.Fatalf("scanned/total = %d/%d, want 1/2", report.ScannedRepositories, report.TotalRepositories)
	}
	if len(report.SkippedRepositories) != 1 || report.SkippedRepositories[0] != "plain-service" {
		t.Fatalf("skipped = %v, want [plain-service] once", report.SkippedRepositories)
	}
	for _, entry := range report.Leaderboard {
		if entry.Repository == "plain-service" {
			t.Fatal("a repository of unknown language must not appear on the leaderboard")
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
	if report.CoverageKnown || report.OverallFleetCoverage != 0 {
		t.Fatalf("expected unknown coverage for an empty fleet, got known=%v value=%.1f", report.CoverageKnown, report.OverallFleetCoverage)
	}
}
