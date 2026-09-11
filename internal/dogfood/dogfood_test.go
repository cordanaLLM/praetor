package dogfood

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDogfood_Positive(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	reportFile := filepath.Join(tmpDir, "dogfood-report.json")
	opts := DogfoodOptions{
		HostRepoPath:   "../..",
		ReportPath:     reportFile,
		MaxScanTargets: 5,
	}

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("RunDogfood failed: %v", err)
	}

	if !rep.ContextSyncPassed {
		t.Fatal("expected context sync to pass on host repo")
	}
	if !rep.SelfAuditPassed {
		t.Fatal("expected self audit to pass on host repo")
	}
	if !rep.OverallPassed {
		t.Fatal("expected overall dogfood run to pass")
	}
}

func TestDogfood_Negative_CancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	opts := DogfoodOptions{
		HostRepoPath: "../..",
	}

	_, err := RunDogfood(ctx, opts)
	if err == nil {
		t.Fatal("expected error on cancelled context, got nil")
	}
}

func TestDogfood_Boundary_EmptyTargets(t *testing.T) {
	ctx := context.Background()
	tmpDir := t.TempDir()

	opts := DogfoodOptions{
		HostRepoPath:   "../..",
		TargetReposDir: tmpDir,
	}

	rep, err := RunDogfood(ctx, opts)
	if err != nil {
		t.Fatalf("expected nil error on empty targets dir, got: %v", err)
	}
	if rep.TargetsEvaluated != 0 {
		t.Fatalf("expected 0 targets evaluated, got: %d", rep.TargetsEvaluated)
	}
}

func TestDogfood_Remote_ReadinessGrades(t *testing.T) {
	tests := []struct {
		debt      int
		hiss      int
		wantGrade string
	}{
		{debt: 2, hiss: 0, wantGrade: "A"},
		{debt: 15, hiss: 5, wantGrade: "B"},
		{debt: 30, hiss: 25, wantGrade: "C"},
		{debt: 100, hiss: 60, wantGrade: "F"},
	}

	for _, tc := range tests {
		got := calculateReadinessGrade(tc.debt, tc.hiss)
		if got != tc.wantGrade {
			t.Errorf("calculateReadinessGrade(%d, %d) = %s, want %s", tc.debt, tc.hiss, got, tc.wantGrade)
		}
	}
}

func TestDogfood_Remote_InvalidURL(t *testing.T) {
	ctx := context.Background()
	res, err := testSingleRemoteAdoption(ctx, "https://invalid.example.com/nonexistent/repo.git")
	if err != nil {
		t.Fatalf("unexpected fatal error: %v", err)
	}
	if res.Passed {
		t.Fatal("expected failure on invalid URL")
	}
	if res.Error == "" {
		t.Fatal("expected error description in result")
	}
}
