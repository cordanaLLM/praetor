// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// stubActionsReader answers WorkflowRunHistory from a table and records every question.
type stubActionsReader struct {
	histories map[string]WorkflowRunHistory
	errs      map[string]error
	asked     []string
}

func (s *stubActionsReader) WorkflowPermissions(context.Context) (LiveWorkflowPermissions, error) {
	return LiveWorkflowPermissions{}, ErrNotImplemented
}

func (s *stubActionsReader) WorkflowRunHistory(_ context.Context, workflow, branch string) (WorkflowRunHistory, error) {
	s.asked = append(s.asked, workflow+"@"+branch)
	if err := s.errs[workflow]; err != nil {
		return WorkflowRunHistory{}, err
	}
	return s.histories[workflow], nil
}

// completedRuns builds completed runs on main, newest first, one per conclusion, numbered
// downwards from len(conclusions).
func completedRuns(conclusions ...string) WorkflowRunHistory {
	runs := make([]WorkflowRunRecord, 0, len(conclusions))
	for i, conclusion := range conclusions {
		event, parts := "push", strings.SplitN(conclusion, ":", 2)
		if len(parts) == 2 {
			event, conclusion = parts[0], parts[1]
		}
		runs = append(runs, WorkflowRunRecord{Number: len(conclusions) - i, Event: event, HeadBranch: "main",
			Status: "completed", Conclusion: conclusion, CreatedAt: "2026-09-27T07:46:55Z", URL: "https://forge.test/run"})
	}
	return WorkflowRunHistory{Known: true, Completed: runs}
}

// runHealthRepo writes one workflow per name, triggered by the events on names' value.
func runHealthRepo(t *testing.T, workflows map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, on := range workflows {
		writeWorkflowFixture(t, root, name, "on: "+on+"\njobs:\n  run:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n")
	}
	return root
}

func auditRuns(t *testing.T, root string, reader ActionsReader, policy *config.WorkflowRunsPolicy) (WorkflowRunReport, string) {
	t.Helper()
	report, err := AuditWorkflowRunHealth(t.Context(), root, "main", reader, policy)
	if err != nil {
		t.Fatalf("workflow run check: %v", err)
	}
	return report, strings.Join(report.Lines(), "\n")
}

// Positive (#612): every workflow whose latest decisive run on main succeeded is healthy; the
// report passes and lists each workflow it checked. Pull request runs and cancelled runs are
// passed over on the way to the decisive run.
func TestAuditWorkflowRunHealth_Positive_HealthyWorkflowsAreListed(t *testing.T) {
	root := runHealthRepo(t, map[string]string{"ci.yml": "[push, pull_request]", "docs.yml": "push"})
	reader := &stubActionsReader{histories: map[string]WorkflowRunHistory{
		"ci.yml":   completedRuns("pull_request:failure", "cancelled", "success", "failure"),
		"docs.yml": completedRuns("success"),
	}}
	report, text := auditRuns(t, root, reader, nil)
	if !strings.Contains(text, "[PASS] Workflow runs on main read from the forge: 2 workflows checked, none failing or never run (ci.yml, docs.yml).") {
		t.Fatalf("healthy report:\n%s", text)
	}
	if strings.Contains(text, "[WARN]") || report.Findings[0].Health != WorkflowHealthy || report.Findings[0].Latest.Number != 2 {
		t.Fatalf("healthy findings: %+v\n%s", report.Findings, text)
	}
	if !slices.Equal(reader.asked, []string{"ci.yml@main", "docs.yml@main"}) {
		t.Fatalf("asked = %v", reader.asked)
	}
}

// Negative (#612): a workflow whose last three runs on main failed is reported with the streak
// and the latest failed run, and a workflow with no run anywhere is reported as never run,
// naming the triggers that would start it. Both are counted in a warning summary.
func TestAuditWorkflowRunHealth_Negative_FailingStreakAndNeverRun(t *testing.T) {
	root := runHealthRepo(t, map[string]string{
		"build.yml": "schedule", "dispatch.yml": "[repository_dispatch, workflow_dispatch]", "ok.yml": "push",
	})
	reader := &stubActionsReader{histories: map[string]WorkflowRunHistory{
		"build.yml":    completedRuns("failure", "timed_out", "failure", "success"),
		"dispatch.yml": {Known: true},
		"ok.yml":       completedRuns("success"),
	}}
	report, text := auditRuns(t, root, reader, nil)
	for _, want := range []string{
		"[WARN] Workflow build.yml: failing on main, failed runs in a row: 3 (latest: run #4, push, failure, 2026-09-27T07:46:55Z, https://forge.test/run)",
		"[WARN] Workflow dispatch.yml: never run; started by repository_dispatch, workflow_dispatch",
		"[WARN] Workflow runs on main read from the forge: 2 of 3 workflows failing or never run (build.yml, dispatch.yml); reported, not enforced.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
	if report.Findings[0].StreakOpen || !report.Findings[0].Unhealthy() || !report.Findings[1].Unhealthy() {
		t.Fatalf("findings = %+v", report.Findings)
	}
}

// Boundary (#612): a declaration covers exactly the state it names. A workflow declared
// failing is listed with its reason and not counted; one declared unexercised that fails is
// still counted; one declared failing that now succeeds is told its declaration no longer
// applies; a declaration naming no workflow is warned about.
func TestAuditWorkflowRunHealth_Boundary_DeclarationsCoverOnlyTheirState(t *testing.T) {
	root := runHealthRepo(t, map[string]string{"build.yml": "schedule", "release.yml": "release", "fixed.yml": "push"})
	reader := &stubActionsReader{histories: map[string]WorkflowRunHistory{
		"build.yml":   completedRuns("failure", "failure"),
		"release.yml": completedRuns("failure"),
		"fixed.yml":   completedRuns("success"),
	}}
	policy := &config.WorkflowRunsPolicy{Expected: []config.WorkflowRunExpectation{
		{Workflow: "build.yml", State: config.WorkflowRunFailing, Reason: "compile step not implemented"},
		{Workflow: "release.yml", State: config.WorkflowRunUnexercised, Reason: "no tag yet"},
		{Workflow: "fixed.yml", State: config.WorkflowRunFailing, Reason: "was broken"},
		{Workflow: "gone.yml", State: config.WorkflowRunUnexercised, Reason: "removed"},
	}}
	_, text := auditRuns(t, root, reader, policy)
	for _, want := range []string{
		"[WARN] workflow_runs.expected names gone.yml, which is not a workflow under .github/workflows",
		"[INFO] Workflow build.yml: failing as declared (compile step not implemented): failing on main, failed runs in a row: 2 (latest: run #2",
		"[WARN] Workflow release.yml: failing on main",
		"[INFO] Workflow fixed.yml: declared failing, but latest run on main succeeded",
		"1 of 3 workflows failing or never run (release.yml)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
}

// Boundary (#612): a workflow_call-only workflow is not asked about; a workflow the forge does
// not know, one with no completed run on main, and one whose completed runs were all skipped
// are listed without counting as unhealthy.
func TestAuditWorkflowRunHealth_Boundary_StatesThatSayNothingAboutHealth(t *testing.T) {
	root := runHealthRepo(t, map[string]string{
		"called.yml": "workflow_call", "new.yml": "push", "tag.yml": "push", "gated.yml": "issue_comment",
	})
	latest := WorkflowRunRecord{Number: 9, Event: "push", HeadBranch: "v1.0.0", Status: "completed", Conclusion: "success", CreatedAt: "2026-09-20T00:00:00Z"}
	reader := &stubActionsReader{histories: map[string]WorkflowRunHistory{
		"new.yml":   {},
		"tag.yml":   {Known: true, Total: 4, Latest: &latest},
		"gated.yml": completedRuns("skipped", "skipped"),
	}}
	_, text := auditRuns(t, root, reader, nil)
	for _, want := range []string{
		"[INFO] Workflow called.yml: runs only when another workflow calls it (workflow_call); not read on its own",
		"[INFO] Workflow new.yml: the forge has no workflow of this file name",
		"[INFO] Workflow tag.yml: no completed run on main; latest run: #9, push on v1.0.0, success",
		"[INFO] Workflow gated.yml: no successful or failed run among the latest completed runs on main",
		"[PASS] Workflow runs on main read from the forge: 2 workflows checked, none failing or never run (gated.yml, tag.yml).",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("report lacks %q:\n%s", want, text)
		}
	}
	if slices.Contains(reader.asked, "called.yml@main") {
		t.Fatalf("a workflow_call-only workflow was asked about: %v", reader.asked)
	}
}

// Negative (#612): the first question the forge refuses ends the check with its error, so a
// report is never complete over workflows it could not read; a forge that knows none of the
// workflows is ErrActionsNotReadable; a missing reader or branch is refused.
func TestAuditWorkflowRunHealth_Negative_RefusalsEndTheCheck(t *testing.T) {
	root := runHealthRepo(t, map[string]string{"a.yml": "push", "b.yml": "push"})
	refusing := &stubActionsReader{errs: map[string]error{"a.yml": ErrActionsNotReadable}}
	if _, err := AuditWorkflowRunHealth(t.Context(), root, "main", refusing, nil); !errors.Is(err, ErrActionsNotReadable) {
		t.Fatalf("a refused question returned %v", err)
	}
	if !slices.Equal(refusing.asked, []string{"a.yml@main"}) {
		t.Fatalf("the check went on after a refusal: %v", refusing.asked)
	}
	blind := &stubActionsReader{histories: map[string]WorkflowRunHistory{}}
	if _, err := AuditWorkflowRunHealth(t.Context(), root, "main", blind, nil); !errors.Is(err, ErrActionsNotReadable) {
		t.Fatalf("a forge that knows no workflow returned %v", err)
	}
	if _, err := AuditWorkflowRunHealth(t.Context(), root, "main", nil, nil); err == nil {
		t.Fatal("a nil reader was accepted")
	}
	if _, err := AuditWorkflowRunHealth(t.Context(), root, "", blind, nil); err == nil {
		t.Fatal("an empty branch was accepted")
	}
}

// Boundary (#612): a history shorter than the window is the workflow's whole completed history
// on the branch, so an all-failure one is counted exactly, never "at least": the issue's three
// failed scheduled runs read as 3, and a history one run short of the window reads as 19.
func TestAuditWorkflowRunHealth_Boundary_ShortFailureHistoryIsExact(t *testing.T) {
	short := make([]string, workflowRunWindow-1)
	for i := range short {
		short[i] = "schedule:failure"
	}
	root := runHealthRepo(t, map[string]string{"build-matrix.yml": "schedule", "nightly.yml": "schedule"})
	reader := &stubActionsReader{histories: map[string]WorkflowRunHistory{
		"build-matrix.yml": completedRuns("schedule:failure", "schedule:failure", "schedule:failure"),
		"nightly.yml":      completedRuns(short...),
	}}
	report, text := auditRuns(t, root, reader, nil)
	for i, want := range []int{3, workflowRunWindow - 1} {
		finding := report.Findings[i]
		if finding.Health != WorkflowFailing || finding.Streak != want || finding.StreakOpen {
			t.Errorf("%s: health=%s streak=%d open=%v, want failing, %d, closed", finding.Workflow, finding.Health, finding.Streak, finding.StreakOpen, want)
		}
	}
	if !strings.Contains(text, "[WARN] Workflow build-matrix.yml: failing on main, failed runs in a row: 3 (latest: run #3, schedule, failure,") ||
		strings.Contains(text, "at least") {
		t.Fatalf("short history report:\n%s", text)
	}
}

// Boundary (#612): a streak that fills the whole window is reported as at least that long, and
// a checkout without workflows asks nothing and says so.
func TestAuditWorkflowRunHealth_Boundary_OpenStreakAndNoWorkflows(t *testing.T) {
	failures := make([]string, workflowRunWindow)
	for i := range failures {
		failures[i] = "failure"
	}
	root := runHealthRepo(t, map[string]string{"nightly.yml": "schedule"})
	report, text := auditRuns(t, root, &stubActionsReader{histories: map[string]WorkflowRunHistory{"nightly.yml": completedRuns(failures...)}}, nil)
	if !report.Findings[0].StreakOpen || !strings.Contains(text, "failed runs in a row: at least 20") {
		t.Fatalf("open streak:\n%s", text)
	}
	empty := &stubActionsReader{}
	_, text = auditRuns(t, t.TempDir(), empty, nil)
	if len(empty.asked) != 0 || !strings.Contains(text, "[INFO] Workflow runs on main: no workflow under .github/workflows that runs on its own; nothing read.") {
		t.Fatalf("no-workflow report (asked %v):\n%s", empty.asked, text)
	}
}
