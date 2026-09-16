package adopt

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Positive (#138): declining to record a baseline writes no baseline. The previous behaviour wrote
// total_infractions: 0 with a fresh timestamp, indistinguishable from a real zero-debt scan.
func TestReconcileBaseline_Positive_NotRecordingWritesNothing(t *testing.T) {
	root := t.TempDir()
	s := &adoptSession{repoPath: root, opts: AdoptOptions{RecordBaseline: false}, report: &AdoptReport{}}
	if err := reconcileBaseline(context.Background(), s); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, baselineFile)); !os.IsNotExist(err) {
		t.Fatalf("a baseline was written although recording was declined: %v", err)
	}
	if s.report.BaselineStatus != "skipped" {
		t.Errorf("status %q, want skipped", s.report.BaselineStatus)
	}
}

// Negative: declining to record must not destroy a baseline the repository already has. Existing
// recorded debt is kept and verified, exactly as before.
func TestReconcileBaseline_Negative_AnExistingBaselineIsKept(t *testing.T) {
	root := t.TempDir()
	existing := []byte(`{"version":1,"generated_at":"2026-01-01T00:00:00Z","total_infractions":3,"infractions":[]}`)
	path := filepath.Join(root, baselineFile)
	if err := os.WriteFile(path, existing, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &adoptSession{repoPath: root, opts: AdoptOptions{RecordBaseline: false}, report: &AdoptReport{}}
	if err := reconcileBaseline(context.Background(), s); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(existing) {
		t.Fatalf("an existing baseline was rewritten: %q %v", got, err)
	}
	if s.report.LegacyDebtCount != 3 {
		t.Errorf("existing debt count not reported: %d", s.report.LegacyDebtCount)
	}
}
