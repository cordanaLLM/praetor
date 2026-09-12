package forge

import (
	"context"
	"fmt"
	"strings"
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
		Title:     "Integrate CUDA VMAFx",
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

// ============================================================================
// ParseCheckboxDependencies: negative and boundary
// ============================================================================

func TestParseCheckboxDependencies_Negative_NoCheckboxes(t *testing.T) {
	for _, body := range []string{"", "   \n\t\n", "## Heading\nplain text\n- not a box\n- [] malformed\n- [y] wrong mark"} {
		if boxes := ParseCheckboxDependencies(body); len(boxes) != 0 {
			t.Errorf("expected no checkbox deps for %q, got %+v", body, boxes)
		}
	}
}

func TestParseCheckboxDependencies_Boundary_MarkersAndLineBound(t *testing.T) {
	boxes := ParseCheckboxDependencies("- [X] Upper case mark\n-  [ ]  spaced out\n- [x] no reference here")
	if len(boxes) != 3 {
		t.Fatalf("expected 3 checkbox items, got %d", len(boxes))
	}
	if !boxes[0].IsChecked {
		t.Error("expected an upper-case [X] to count as checked")
	}
	if boxes[1].IsChecked {
		t.Error("expected a spaced [ ] to count as unchecked")
	}
	if boxes[2].TargetRef.Number != 0 {
		t.Errorf("expected no target reference, got %+v", boxes[2].TargetRef)
	}

	var sb strings.Builder
	for i := 0; i < MaxLinesLimit+50; i++ {
		sb.WriteString("- [ ] task\n")
	}
	if got := len(ParseCheckboxDependencies(sb.String())); got != MaxLinesLimit {
		t.Errorf("expected the scan to stop at the %d line bound, got %d", MaxLinesLimit, got)
	}
}

// ============================================================================
// Dependency resolution: negative and boundary
// ============================================================================

func TestReconcileEngine_Negative_LowercaseDependsOnTag(t *testing.T) {
	engine := NewReconcileEngine("cordanaLLM")
	engine.TrackIssue("golusoris", IssueSpec{ID: 42, State: "closed"})
	engine.TrackIssue("vmafx", IssueSpec{
		ID:        15,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"depends-on: golusoris#42"},
	})

	report, err := engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if len(report.UnblockedIssues) != 1 {
		t.Fatalf("a lowercase Depends-On tag was not resolved: %+v", report)
	}
	if report.UnblockedIssues[0].ResolvedPrereqs[0] != "golusoris#42" {
		t.Errorf("unexpected resolved prerequisite: %+v", report.UnblockedIssues[0])
	}
}

func TestReconcileEngine_Negative_CrossOrgDependencyIsNotConflated(t *testing.T) {
	engine := NewReconcileEngine("cordanaLLM")
	engine.TrackIssue("cordanaLLM/praetor", IssueSpec{ID: 5, State: "closed"})
	engine.TrackIssue("golusoris/golusoris", IssueSpec{
		ID:        9,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"Depends-On: lusoris/praetor#5"},
	})

	report, err := engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if len(report.UnblockedIssues) != 0 {
		t.Fatalf("a dependency on lusoris/praetor was satisfied by cordanaLLM/praetor: %+v", report.UnblockedIssues)
	}
	if len(report.StillBlocked) != 1 || report.StillBlocked[0].PendingPrereqs[0] != "lusoris/praetor#5" {
		t.Errorf("unexpected blocked summary: %+v", report.StillBlocked)
	}
}

func TestReconcileEngine_Boundary_AmbiguousBareRepoStaysBlocked(t *testing.T) {
	engine := NewReconcileEngine("cordanaLLM")
	engine.TrackIssue("acme/core", IssueSpec{ID: 7, State: "open"})
	engine.TrackIssue("beta/core", IssueSpec{ID: 7, State: "closed"})
	engine.TrackIssue("acme/core", IssueSpec{
		ID:        12,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"Depends-On: core#7"},
	})

	for run := 0; run < 5; run++ {
		report, err := engine.Reconcile(context.Background())
		if err != nil {
			t.Fatalf("reconciliation failed: %v", err)
		}
		if len(report.UnblockedIssues) != 0 {
			t.Fatalf("run %d resolved an ambiguous bare repo name: %+v", run, report.UnblockedIssues)
		}
	}

	// The referencing issue's own owner wins over any other tracked repository.
	owned := NewReconcileEngine("cordanaLLM")
	owned.TrackIssue("acme/core", IssueSpec{ID: 7, State: "closed"})
	owned.TrackIssue("beta/core", IssueSpec{ID: 7, State: "open"})
	owned.TrackIssue("acme/web", IssueSpec{
		ID:        1,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"Depends-On: core#7"},
	})
	report, err := owned.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if len(report.UnblockedIssues) != 1 || report.UnblockedIssues[0].ResolvedPrereqs[0] != "acme/core#7" {
		t.Errorf("expected the referencing issue's own owner to win: %+v", report)
	}
}

func TestReconcileEngine_Boundary_DeterministicReportOrder(t *testing.T) {
	engine := NewReconcileEngine("cordanaLLM")
	for i := 0; i < 12; i++ {
		repo := fmt.Sprintf("owner%02d/repo", i)
		engine.TrackIssue(repo, IssueSpec{ID: 1, State: "open"})
		engine.TrackIssue(repo, IssueSpec{
			ID:        2,
			State:     "open",
			Labels:    []string{"status/blocked"},
			DependsOn: []string{fmt.Sprintf("Depends-On: %s#1", repo)},
		})
	}

	first, err := engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	for run := 0; run < 5; run++ {
		again, rerr := engine.Reconcile(context.Background())
		if rerr != nil {
			t.Fatalf("reconciliation failed: %v", rerr)
		}
		if len(again.StillBlocked) != len(first.StillBlocked) {
			t.Fatalf("report size changed between runs")
		}
		for i := range first.StillBlocked {
			if again.StillBlocked[i].Repo != first.StillBlocked[i].Repo ||
				again.StillBlocked[i].IssueNumber != first.StillBlocked[i].IssueNumber {
				t.Fatalf("report order is not deterministic at index %d: %q vs %q",
					i, first.StillBlocked[i].Repo, again.StillBlocked[i].Repo)
			}
		}
	}
}

func TestReconcileEngine_Boundary_SetIssueStateSurvivesTrackIssue(t *testing.T) {
	engine := NewReconcileEngine("cordanaLLM")
	engine.SetIssueState("golusoris", 42, "closed")
	engine.TrackIssue("golusoris", IssueSpec{ID: 1, State: "open"})

	engine.TrackIssue("vmafx", IssueSpec{
		ID:        15,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"Depends-On: golusoris#42"},
	})

	report, err := engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if len(report.UnblockedIssues) != 1 {
		t.Fatalf("TrackIssue discarded the state recorded by SetIssueState: %+v", report)
	}
}

func TestReconcileEngine_Boundary_EmptyEngineAndUntrackedTarget(t *testing.T) {
	engine := NewReconcileEngine("")
	report, err := engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if report.EvaluatedCount != 0 || len(report.UnblockedIssues) != 0 || len(report.StillBlocked) != 0 {
		t.Errorf("unexpected report for an empty engine: %+v", report)
	}

	engine.TrackIssue("solo", IssueSpec{ID: 1, State: "closed", Labels: []string{"status/blocked"}})
	engine.TrackIssue("solo", IssueSpec{
		ID:        2,
		State:     "open",
		Labels:    []string{"status/blocked"},
		DependsOn: []string{"Depends-On: nowhere#99", "not a dependency"},
	})

	report, err = engine.Reconcile(context.Background())
	if err != nil {
		t.Fatalf("reconciliation failed: %v", err)
	}
	if report.EvaluatedCount != 2 {
		t.Errorf("expected both issues to be evaluated, got %d", report.EvaluatedCount)
	}
	if len(report.UnblockedIssues) != 0 {
		t.Errorf("an untracked dependency target must not unblock: %+v", report.UnblockedIssues)
	}
	if len(report.StillBlocked) != 1 || report.StillBlocked[0].IssueNumber != 2 {
		t.Errorf("expected only the open issue to be reported blocked: %+v", report.StillBlocked)
	}
	if len(report.StillBlocked[0].PendingPrereqs) != 1 {
		t.Errorf("an unparseable dependency string must not become a repository: %+v", report.StillBlocked[0])
	}
}

func TestParseSingleDepStr_Boundary_Forms(t *testing.T) {
	cases := []struct {
		in    string
		owner string
		repo  string
		num   int
	}{
		{"#12", "", "current", 12},
		{"repo#12", "", "repo", 12},
		{"owner/repo#12", "owner", "repo", 12},
		{"DEPENDS-ON: owner/repo#12", "owner", "repo", 12},
		{"depends-on: repo#12", "", "repo", 12},
		{"nonsense", "", "", 0},
		{"repo#abc", "", "", 0},
	}
	for _, c := range cases {
		got := parseSingleDepStr(c.in, "current")
		if got.Owner != c.owner || got.Repo != c.repo || got.Number != c.num {
			t.Errorf("parseSingleDepStr(%q) = %+v, want owner=%q repo=%q num=%d",
				c.in, got, c.owner, c.repo, c.num)
		}
	}
}
