package dogfood

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDogfoodDryRunOverridesApplyAdoption(t *testing.T) {
	targets := t.TempDir()
	repo := newTargetRepo(t, targets, "target")
	opts := hermeticOptions(newHostFixture(t, "# Fixture\n"))
	opts.TargetReposDir, opts.ApplyAdoption, opts.DryRun = targets, true, true
	report, err := RunDogfood(context.Background(), opts)
	if err != nil || report.TargetsEvaluated != 1 {
		t.Fatalf("dry run failed: %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".standards.yaml")); !os.IsNotExist(err) {
		t.Fatalf("legacy DryRun was overridden: %v", err)
	}
}
