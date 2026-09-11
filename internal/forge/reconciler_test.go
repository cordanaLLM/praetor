package forge

import (
	"context"
	"testing"
)

func TestParseCheckboxDependencies(t *testing.T) {
	body := `
## Tasks
- [ ] Task 1: Initialize repo
- [x] Task 2: Implement #42 in golusoris
- [ ] Task 3: Migrate downstream Depends-On: vmafx#10
`
	boxes := ParseCheckboxDependencies(body)
	if len(boxes) != 3 {
		t.Fatalf("expected 3 checkbox items, got %d", len(boxes))
	}

	if boxes[0].IsChecked {
		t.Error("expected box 0 to be unchecked")
	}
	if !boxes[1].IsChecked {
		t.Error("expected box 1 to be checked")
	}
	if boxes[1].TargetRef.Number != 42 {
		t.Errorf("expected target issue #42, got %d", boxes[1].TargetRef.Number)
	}
	if boxes[2].IsChecked {
		t.Error("expected box 2 to be unchecked")
	}
}

func TestReconcileEngine_CrossRepoUnblocking(t *testing.T) {
	ctx := context.Background()
	engine := NewReconcileEngine("cordanaLLM")

	// Upstream issue in golusoris
	engine.TrackIssue("golusoris", IssueSpec{
		ID:    42,
		Title: "Add S3 Storage Builder Kit",
		State: "closed", // Upstream is closed/resolved!
	})

	// Downstream issue in vmafx blocked on golusoris#42
	engine.TrackIssue("vmafx", IssueSpec{
		ID:        15,
		Title:     "Adopt S3 Kit for Video Uploads",
		State:     "open",
		Labels:    []string{"task", "status/blocked"},
		DependsOn: []string{"golusoris#42"},
		Body:      "- [x] Upstream S3 implementation verified",
	})

	report, err := engine.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if len(report.UnblockedIssues) != 1 {
		t.Fatalf("expected 1 unblocked issue, got %d", len(report.UnblockedIssues))
	}

	unblocked := report.UnblockedIssues[0]
	if unblocked.Repo != "vmafx" || unblocked.IssueNumber != 15 {
		t.Errorf("unexpected unblocked issue: %+v", unblocked)
	}
	if len(unblocked.ResolvedPrereqs) != 1 {
		t.Errorf("expected 1 resolved prereq, got %d", len(unblocked.ResolvedPrereqs))
	}
}

func TestReconcileEngine_StillBlocked(t *testing.T) {
	ctx := context.Background()
	engine := NewReconcileEngine("cordanaLLM")

	// Upstream issue is still open
	engine.TrackIssue("golusoris", IssueSpec{
		ID:    50,
		Title: "WIP CUDA Kernels",
		State: "open",
	})

	// Downstream issue
	engine.TrackIssue("vmafx", IssueSpec{
		ID:        20,
		Title:     "Integrate CUDA VMAF",
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"golusoris#50"},
	})

	report, err := engine.Reconcile(ctx)
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}

	if len(report.UnblockedIssues) != 0 {
		t.Errorf("expected 0 unblocked issues, got %d", len(report.UnblockedIssues))
	}
	if len(report.StillBlocked) != 1 {
		t.Errorf("expected 1 still blocked issue, got %d", len(report.StillBlocked))
	}
}

func TestReconcileEngine_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	engine := NewReconcileEngine("cordanaLLM")
	_, err := engine.Reconcile(ctx)
	if err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
