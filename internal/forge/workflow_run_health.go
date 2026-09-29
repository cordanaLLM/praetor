// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"gopkg.in/yaml.v3"
)

// workflowRunCheckTimeout bounds one AuditWorkflowRunHealth call, every forge question
// included (HISS-02).
const workflowRunCheckTimeout = 2 * time.Minute

// WorkflowHealth classifies one workflow's runs on the default branch.
type WorkflowHealth string

const (
	// WorkflowHealthy: the newest decisive run on the branch succeeded.
	WorkflowHealthy WorkflowHealth = "healthy"
	// WorkflowFailing: the newest decisive run on the branch failed; Streak counts how many
	// decisive runs failed in a row.
	WorkflowFailing WorkflowHealth = "failing"
	// WorkflowNeverRun: the forge knows the workflow and lists no run of it on any branch.
	WorkflowNeverRun WorkflowHealth = "never-run"
	// WorkflowNoCompletedRun: the workflow has runs, none of them completed on the branch; a
	// tag or release workflow runs off the default branch.
	WorkflowNoCompletedRun WorkflowHealth = "no-completed-run"
	// WorkflowUndecided: every completed run in the window on the branch was cancelled,
	// skipped or neutral, or came from a pull request.
	WorkflowUndecided WorkflowHealth = "undecided"
	// WorkflowUnknown: the forge has no workflow of this file name, because the file is not
	// on its default branch yet.
	WorkflowUnknown WorkflowHealth = "unknown"
	// WorkflowCalledOnly: the workflow's only trigger is workflow_call; it runs inside its
	// callers' runs and never on its own, so the forge is not asked about it.
	WorkflowCalledOnly WorkflowHealth = "called-only"
)

// WorkflowRunFinding is the run check's verdict on one workflow.
type WorkflowRunFinding struct {
	// Workflow is the file name under .github/workflows; Triggers are the events its `on:`
	// key names, in file order.
	Workflow string
	Triggers []string
	Health   WorkflowHealth
	// Streak counts the failed runs in a row, newest first. StreakOpen says the forge listed a
	// full window of runs and no successful run ended the streak inside it, so it may be
	// longer; a listing shorter than the window holds every completed run, so its streak is
	// exact.
	Streak     int
	StreakOpen bool
	// Latest is the newest failed run of a failing workflow, the newest successful run of a
	// healthy one, the newest completed run of an undecided one, and the newest run on any
	// branch of one with no completed run on the branch.
	Latest *WorkflowRunRecord
	// Expected is the manifest's workflow_runs.expected entry for the workflow, if any.
	Expected *config.WorkflowRunExpectation
}

// Covered reports whether the workflow's declaration names exactly the state found: failing
// for a failing workflow, unexercised for one that never ran.
func (f WorkflowRunFinding) Covered() bool {
	if f.Expected == nil {
		return false
	}
	switch f.Health {
	case WorkflowFailing:
		return f.Expected.State == config.WorkflowRunFailing
	case WorkflowNeverRun:
		return f.Expected.State == config.WorkflowRunUnexercised
	default:
		return false
	}
}

// Unhealthy reports a failing or never-run workflow that no declaration covers.
func (f WorkflowRunFinding) Unhealthy() bool {
	return (f.Health == WorkflowFailing || f.Health == WorkflowNeverRun) && !f.Covered()
}

// WorkflowRunReport is the run check over every workflow of a checkout.
type WorkflowRunReport struct {
	// Branch is the default branch whose runs were read.
	Branch   string
	Findings []WorkflowRunFinding
	// Unmatched lists workflow_runs.expected entries that name no workflow of the checkout.
	Unmatched []string
}

// AuditWorkflowRunHealth reads, for each workflow under repoPath's .github/workflows, its
// latest completed runs on branch through reader, and classifies them (#612): healthy, failing
// with the streak and the latest failed run, never run with the triggers that would start it,
// or one of the states that says nothing about health. policy's declarations are matched by
// file name; a declaration covers only the state it names (WorkflowRunFinding.Covered).
//
// The first question the forge refuses or cannot answer ends the check with its error, so an
// unreachable forge costs one timeout rather than one per workflow, and a check that could not
// read every workflow is never reported as complete. A forge that knows none of the workflows
// asked about is ErrActionsNotReadable: its Actions are not visible to the token.
func AuditWorkflowRunHealth(ctx context.Context, repoPath, branch string, reader ActionsReader, policy *config.WorkflowRunsPolicy) (WorkflowRunReport, error) {
	if ctx == nil || reader == nil || branch == "" {
		return WorkflowRunReport{}, errors.New("workflow run check requires a context, a forge reader and a branch")
	}
	ctx, cancel := context.WithTimeout(ctx, workflowRunCheckTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return WorkflowRunReport{}, err
	}
	findings, err := readWorkflowFindings(ctx, files, branch, reader, policy)
	if err != nil {
		return WorkflowRunReport{}, err
	}
	return WorkflowRunReport{Branch: branch, Findings: findings, Unmatched: unmatchedExpectations(policy, files)}, nil
}

// readWorkflowFindings checks every workflow file in order and stops at the first error. A
// forge that answers every question it was asked with "unknown workflow" cannot see the
// repository's Actions, which is ErrActionsNotReadable rather than a report of unknowns.
func readWorkflowFindings(ctx context.Context, files []workflowFile, branch string, reader ActionsReader, policy *config.WorkflowRunsPolicy) ([]WorkflowRunFinding, error) {
	findings := make([]WorkflowRunFinding, 0, len(files))
	asked, unknown := 0, 0
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		finding, err := checkWorkflowRuns(ctx, files[i], branch, reader, policy)
		if err != nil {
			return nil, err
		}
		if finding.Health != WorkflowCalledOnly {
			asked++
		}
		if finding.Health == WorkflowUnknown {
			unknown++
		}
		findings = append(findings, finding)
	}
	if asked > 0 && unknown == asked {
		return nil, fmt.Errorf("the forge knows none of the %d workflows asked about: %w", asked, ErrActionsNotReadable)
	}
	return findings, nil
}

// checkWorkflowRuns reads one workflow's triggers from the file and its runs from the forge.
func checkWorkflowRuns(ctx context.Context, file workflowFile, branch string, reader ActionsReader, policy *config.WorkflowRunsPolicy) (WorkflowRunFinding, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(file.Data, &spec); err != nil {
		return WorkflowRunFinding{}, fmt.Errorf("parse workflow %s: %w", file.Name, err)
	}
	finding := WorkflowRunFinding{Workflow: file.Name, Triggers: triggerNames(&spec.On), Expected: policy.Expectation(file.Name)}
	if len(finding.Triggers) == 1 && finding.Triggers[0] == "workflow_call" {
		finding.Health = WorkflowCalledOnly
		return finding, nil
	}
	history, err := reader.WorkflowRunHistory(ctx, file.Name, branch)
	if err != nil {
		return WorkflowRunFinding{}, err
	}
	classifyWorkflowRuns(&finding, history)
	return finding, nil
}

// triggerNames lists the events an `on:` node names, in file order (workflowTriggers).
func triggerNames(on *yaml.Node) []string {
	triggers := workflowTriggers(on)
	var names []string
	for i := 0; i < len(triggers) && i < maxJobsPerFile; i++ {
		names = append(names, triggers[i].name)
	}
	return names
}

// classifyWorkflowRuns sets the finding's health from what the forge reported.
func classifyWorkflowRuns(finding *WorkflowRunFinding, history WorkflowRunHistory) {
	switch {
	case !history.Known:
		finding.Health = WorkflowUnknown
	case len(history.Completed) == 0 && history.Total == 0:
		finding.Health = WorkflowNeverRun
	case len(history.Completed) == 0:
		finding.Health, finding.Latest = WorkflowNoCompletedRun, history.Latest
	default:
		classifyCompletedRuns(finding, history.Completed)
	}
}

// classifyCompletedRuns walks the completed runs newest first. A pull request's run is skipped:
// its head branch is the contributor's, which may share the default branch's name. A cancelled,
// skipped or neutral run neither extends nor ends a streak. The first successful run ends the
// walk: healthy when nothing failed before it, else failing with the streak counted so far. A
// streak no success ended is open only when runs fill the window: a shorter listing is the
// workflow's whole completed history on the branch.
func classifyCompletedRuns(finding *WorkflowRunFinding, runs []WorkflowRunRecord) {
	finding.Health, finding.Latest = WorkflowUndecided, &runs[0]
	for i := 0; i < len(runs) && i < workflowRunWindow; i++ {
		if isPullRequestRun(runs[i].Event) {
			continue
		}
		switch {
		case runs[i].Conclusion == "success" && finding.Streak == 0:
			finding.Health, finding.Latest = WorkflowHealthy, &runs[i]
			return
		case runs[i].Conclusion == "success":
			return
		case failedConclusion(runs[i].Conclusion):
			if finding.Streak == 0 {
				finding.Health, finding.Latest = WorkflowFailing, &runs[i]
			}
			finding.Streak++
		}
	}
	finding.StreakOpen = finding.Streak > 0 && len(runs) >= workflowRunWindow
}

// isPullRequestRun reports a run a pull request started.
func isPullRequestRun(event string) bool {
	return event == pullRequestEvent || event == pullRequestTargetEvent
}

// failedConclusion reports a conclusion that means the run failed.
func failedConclusion(conclusion string) bool {
	return conclusion == "failure" || conclusion == "timed_out" || conclusion == "startup_failure"
}

// unmatchedExpectations returns the declared workflows that are not among files.
func unmatchedExpectations(policy *config.WorkflowRunsPolicy, files []workflowFile) []string {
	if policy == nil {
		return nil
	}
	present := make(map[string]bool, len(files))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		present[files[i].Name] = true
	}
	var unmatched []string
	for i := 0; i < len(policy.Expected) && i < config.MaxWorkflowRunExpectations; i++ {
		if name := policy.Expected[i].Workflow; !present[name] {
			unmatched = append(unmatched, name)
		}
	}
	return unmatched
}

// Lines renders the report for the audit: a line per workflow that is not plainly healthy, a
// warning per declaration that names no workflow, and a summary that lists every workflow
// checked. Findings are reported and never fail the audit on their own (read-and-report).
func (r WorkflowRunReport) Lines() []string {
	var lines []string
	for _, name := range r.Unmatched {
		lines = append(lines, fmt.Sprintf("[WARN] workflow_runs.expected names %s, which is not a workflow under .github/workflows", name))
	}
	var checked, unhealthy []string
	for i := 0; i < len(r.Findings) && i < maxWorkflowFiles; i++ {
		finding := r.Findings[i]
		if line := finding.line(r.Branch); line != "" {
			lines = append(lines, line)
		}
		if finding.Health != WorkflowCalledOnly && finding.Health != WorkflowUnknown {
			checked = append(checked, finding.Workflow)
		}
		if finding.Unhealthy() {
			unhealthy = append(unhealthy, finding.Workflow)
		}
	}
	return append(lines, r.summary(checked, unhealthy))
}

func (r WorkflowRunReport) summary(checked, unhealthy []string) string {
	switch {
	case len(checked) == 0:
		return fmt.Sprintf("[INFO] Workflow runs on %s: no workflow under .github/workflows that runs on its own; nothing read.", r.Branch)
	case len(unhealthy) == 0:
		return fmt.Sprintf("[PASS] Workflow runs on %s read from the forge: %d workflows checked, none failing or never run (%s).",
			r.Branch, len(checked), strings.Join(checked, ", "))
	default:
		return fmt.Sprintf("[WARN] Workflow runs on %s read from the forge: %d of %d workflows failing or never run (%s); reported, not enforced.",
			r.Branch, len(unhealthy), len(checked), strings.Join(unhealthy, ", "))
	}
}

// line renders one finding, or "" for a healthy workflow without a declaration, which the
// summary lists.
func (f WorkflowRunFinding) line(branch string) string {
	switch {
	case f.Covered():
		return fmt.Sprintf("[INFO] Workflow %s: %s as declared (%s): %s", f.Workflow, f.Expected.State, f.Expected.Reason, f.state(branch))
	case f.Unhealthy():
		return fmt.Sprintf("[WARN] Workflow %s: %s", f.Workflow, f.state(branch))
	case f.Expected != nil:
		return fmt.Sprintf("[INFO] Workflow %s: declared %s, but %s; the declaration no longer applies", f.Workflow, f.Expected.State, f.state(branch))
	case f.Health == WorkflowHealthy:
		return ""
	default:
		return fmt.Sprintf("[INFO] Workflow %s: %s", f.Workflow, f.state(branch))
	}
}

// state describes the finding's health in words.
func (f WorkflowRunFinding) state(branch string) string {
	switch f.Health {
	case WorkflowHealthy:
		return fmt.Sprintf("latest run on %s succeeded%s", branch, runReference(f.Latest))
	case WorkflowFailing:
		return fmt.Sprintf("failing on %s, failed runs in a row: %s%s", branch, f.streak(), runReference(f.Latest))
	case WorkflowNeverRun:
		return "never run; started by " + strings.Join(f.Triggers, ", ")
	case WorkflowNoCompletedRun:
		return fmt.Sprintf("no completed run on %s%s", branch, otherRunReference(f.Latest))
	case WorkflowUndecided:
		return fmt.Sprintf("no successful or failed run among the latest completed runs on %s%s", branch, runReference(f.Latest))
	case WorkflowUnknown:
		return "the forge has no workflow of this file name; it is not on the default branch yet"
	default:
		return "runs only when another workflow calls it (workflow_call); not read on its own"
	}
}

func (f WorkflowRunFinding) streak() string {
	if f.StreakOpen {
		return fmt.Sprintf("at least %d", f.Streak)
	}
	return fmt.Sprint(f.Streak)
}

// runReference names a completed run: number, event, conclusion, time and link.
func runReference(run *WorkflowRunRecord) string {
	if run == nil {
		return ""
	}
	return fmt.Sprintf(" (latest: run #%d, %s, %s, %s, %s)", run.Number, run.Event, run.Conclusion, run.CreatedAt, run.URL)
}

// otherRunReference names the newest run on any branch, in whatever state it is.
func otherRunReference(run *WorkflowRunRecord) string {
	if run == nil {
		return ""
	}
	outcome := run.Status
	if run.Conclusion != "" {
		outcome = run.Conclusion
	}
	return fmt.Sprintf("; latest run: #%d, %s on %s, %s, %s", run.Number, run.Event, run.HeadBranch, outcome, run.CreatedAt)
}
