// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"gopkg.in/yaml.v3"
)

const (
	// workflowTriggerGate names the HISS-18 workflow trigger check on every line it prints.
	workflowTriggerGate = "Workflow triggers (HISS-18)"
	// workflowTriggerGuide is the document that says how a workflow meets the check.
	workflowTriggerGuide = "docs/guides/workflow-triggers.md"
	// workflowTriggerTimeout bounds one read of the workflows (HISS-02).
	workflowTriggerTimeout = 30 * time.Second
	pushEvent              = "push"
	// pushEveryRef is the problem of a push trigger that names no branch or tag filter.
	pushEveryRef = "runs on every branch and tag: it names no branches or tags filter"
	// notReadyProblem is the problem of a pull_request trigger whose jobs stop on a draft while
	// marking the draft ready starts no run.
	notReadyProblem = "stops a draft in its jobs but does not run on " + ghworkflow.HostedGateReadyType +
		", so marking the draft ready starts no run and the draft's failed check stands until the next push"
)

// WorkflowTriggerOptions is what AuditWorkflowTriggers reads: the repository root, the
// manifest's exceptions list (the check reads the entries of config.ExceptionRuleWorkflowTriggers)
// and the day those entries' expiry is judged against.
type WorkflowTriggerOptions struct {
	Root       string
	Exceptions []config.Exception
	Today      time.Time
}

// workflowTriggerFinding is one trigger or job of a workflow that starts a run the check counts
// as wasted: Workflow is the repository-relative slash path, Line the document line of the
// trigger or the job, Trigger the event, Job the job ID ("" for the trigger itself) and Problem
// what starts the run.
type workflowTriggerFinding struct {
	Workflow string
	Line     int
	Trigger  string
	Job      string
	Problem  string
}

// String names the file, the line and the trigger or job, then the problem.
func (f workflowTriggerFinding) String() string {
	subject := f.Trigger
	if f.Job != "" {
		subject += " job " + f.Job
	}
	return fmt.Sprintf("%s:%d: %s %s", f.Workflow, f.Line, subject, f.Problem)
}

// AuditWorkflowTriggers is the HISS-18 workflow trigger check the CLI and MCP audits share
// (#817). It reads every workflow under .github/workflows and reports, naming the file, the line
// and the trigger:
//
//   - a push trigger that runs on every branch: no branches or tags filter, only a
//     branches-ignore filter, or a branches pattern of asterisks alone;
//   - a job a pull_request run starts that runs its work on a draft pull request: its first step
//     is not the hosted gate's draft step (ghworkflow.DraftStepFault), and it calls no reusable
//     workflow of this repository whose jobs each begin with that step;
//   - a job a draft skips because a job it needs is held back on one and its condition does not
//     run it after a failed need: GitHub reports the skip as successful, which a required check
//     accepts, and the check cannot see which job the ruleset requires;
//   - a job that skips a draft with a job-level condition, or stops a draft and continues on
//     error: GitHub reports either as successful, which a required check accepts, so the hosted
//     gate shape fails a draft in a step instead (ghworkflow.HostedGateDraftStep);
//   - a pull_request trigger whose jobs stop on a draft but that does not run on
//     ready_for_review, so the failed draft check stands until the next push.
//
// A live exceptions entry with rule HISS-18 naming a workflow prints its findings with the
// entry's reason and expiry instead; an expired entry reports them as if it were missing, and an
// entry naming a workflow without findings is stale.
//
// Findings are reported, not enforced: HISS-18's failure action is a CI optimization gate
// (internal/hisscatalog), the one HISS rule whose failure rejects nothing, so the check warns
// and passes. An invalid exceptions entry or a workflow the check cannot read fails, because a
// check that did not run is not a pass. A repository without workflows is skipped, saying so.
func AuditWorkflowTriggers(ctx context.Context, opts WorkflowTriggerOptions) (string, error) {
	if ctx == nil {
		return "", errors.New("[FAIL] " + workflowTriggerGate + ": the check requires a context")
	}
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleWorkflowTriggers)
	if err := config.ValidateExceptions(entries, opts.Today); err != nil {
		return "", fmt.Errorf("[FAIL] %s: %w", workflowTriggerGate, err)
	}
	read, findings, err := workflowTriggerFindings(ctx, opts.Root)
	if err != nil {
		return "", fmt.Errorf("[FAIL] %s: cannot read the workflows: %w", workflowTriggerGate, err)
	}
	return judgeWorkflowTriggers(read, findings, entries, opts.Today), nil
}

// triggerWorkflow is one parsed workflow with its repository-relative path and the document line
// of each job ID.
type triggerWorkflow struct {
	path     string
	spec     workflowSpec
	jobLines map[string]int
}

// workflowTriggerFindings reads the workflows of the repository at repoPath and returns how many
// it read and every finding, workflow by workflow in name order and line by line within one.
func workflowTriggerFindings(ctx context.Context, repoPath string) (int, []workflowTriggerFinding, error) {
	ctx, cancel := context.WithTimeout(ctx, workflowTriggerTimeout)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return 0, nil, err
	}
	workflows := make([]triggerWorkflow, 0, len(files))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		workflow, err := parseTriggerWorkflow(files[i])
		if err != nil {
			return 0, nil, err
		}
		workflows = append(workflows, workflow)
	}
	callees := make(map[string]*workflowSpec, len(workflows))
	for i := 0; i < len(workflows) && i < maxWorkflowFiles; i++ {
		callees[strings.TrimPrefix(workflows[i].path, plannedWorkflowDir)] = &workflows[i].spec
	}
	var findings []workflowTriggerFinding
	for i := 0; i < len(workflows) && i < maxWorkflowFiles; i++ {
		findings = append(findings, workflows[i].findings(callees)...)
	}
	return len(workflows), findings, nil
}

// parseTriggerWorkflow decodes one workflow document and records the line of each job ID.
func parseTriggerWorkflow(file workflowFile) (triggerWorkflow, error) {
	spec, err := ghworkflow.Parse(file.Data)
	if err != nil {
		return triggerWorkflow{}, fmt.Errorf("workflow %s: %w", file.Name, err)
	}
	var document yaml.Node
	if err := yaml.Unmarshal(file.Data, &document); err != nil {
		return triggerWorkflow{}, fmt.Errorf("workflow %s: parse: %w", file.Name, err)
	}
	return triggerWorkflow{path: plannedWorkflowDir + file.Name, spec: spec, jobLines: jobKeyLines(&document)}, nil
}

// jobKeyLines maps each job ID of a workflow document to the line it stands on.
func jobKeyLines(document *yaml.Node) map[string]int {
	lines := make(map[string]int)
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return lines
	}
	jobs := util.YAMLMappingValue(document.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return lines
	}
	for i := 0; i+1 < len(jobs.Content) && i < 2*maxJobsPerFile; i += 2 {
		lines[jobs.Content[i].Value] = jobs.Content[i].Line
	}
	return lines
}

// findings lists what the push and pull_request triggers of w start that the check counts as
// wasted, in line order. callees holds every workflow of the repository by file name.
func (w *triggerWorkflow) findings(callees map[string]*workflowSpec) []workflowTriggerFinding {
	var findings []workflowTriggerFinding
	if push, declared := declaredTrigger(&w.spec.On, pushEvent); declared {
		if problem := pushProblem(push.value); problem != "" {
			findings = append(findings, workflowTriggerFinding{Workflow: w.path, Line: push.line, Trigger: pushEvent, Problem: problem})
		}
	}
	if pullRequest, declared := declaredTrigger(&w.spec.On, pullRequestEvent); declared {
		findings = append(findings, w.pullRequestFindings(pullRequest, callees)...)
	}
	slices.SortStableFunc(findings, func(a, b workflowTriggerFinding) int { return cmp.Compare(a.Line, b.Line) })
	return findings
}

// pushProblem says why a push trigger's value runs on every branch, or returns "". A trigger
// with no mapping value, or a mapping without branches, branches-ignore, tags or tags-ignore,
// runs on every branch and tag; one with branches-ignore alone runs on every branch it does not
// name; and a branches pattern of asterisks alone matches every branch name. A tags filter
// without a branches filter runs on no branch push.
func pushProblem(value *yaml.Node) string {
	if value == nil || value.Kind != yaml.MappingNode {
		return pushEveryRef
	}
	if branches := util.YAMLMappingValue(value, "branches"); branches != nil {
		if pattern, every := everyBranchPattern(ghworkflow.StringList(branches)); every {
			return fmt.Sprintf("runs on every branch: its branches filter %q matches any branch name", pattern)
		}
		return ""
	}
	if util.YAMLMappingValue(value, "branches-ignore") != nil {
		return "runs on every branch its branches-ignore filter does not name; name the branches it runs on in a branches filter instead"
	}
	if util.YAMLMappingValue(value, "tags") != nil || util.YAMLMappingValue(value, "tags-ignore") != nil {
		return ""
	}
	return pushEveryRef
}

// everyBranchPattern returns the first of patterns made of asterisks alone, which matches every
// branch name, and whether there is one.
func everyBranchPattern(patterns []string) (string, bool) {
	for i := 0; i < len(patterns) && i < ghworkflow.MaxStepsPerJob; i++ {
		if patterns[i] != "" && strings.Trim(patterns[i], "*") == "" {
			return patterns[i], true
		}
	}
	return "", false
}

// pullRequestFindings lists every job a pull_request run of w starts that runs its work on a
// draft or passes a draft without running it (draftProblem), and the trigger itself when a job
// stops on a draft but the trigger does not run on ready_for_review.
func (w *triggerWorkflow) pullRequestFindings(trigger workflowTrigger, callees map[string]*workflowSpec) []workflowTriggerFinding {
	handling := w.draftHandlings(callees)
	ids := sortedJobIDs(w.spec.Jobs)
	var findings []workflowTriggerFinding
	stops := false
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := w.spec.Jobs[ids[i]]
		stops = stops || handling[ids[i]] == draftStops
		if problem := draftProblem(handling[ids[i]], &job, handling); problem != "" {
			findings = append(findings, workflowTriggerFinding{
				Workflow: w.path, Line: w.jobLines[ids[i]], Trigger: pullRequestEvent, Job: ids[i], Problem: problem})
		}
	}
	types := ghworkflow.StringList(util.YAMLMappingValue(trigger.value, "types"))
	if stops && !slices.Contains(types, ghworkflow.HostedGateReadyType) {
		findings = append(findings, workflowTriggerFinding{Workflow: w.path, Line: trigger.line, Trigger: pullRequestEvent, Problem: notReadyProblem})
	}
	return findings
}

// draftHandling is what a job of a pull_request workflow does on a draft pull request.
type draftHandling int

const (
	// draftUnreached: the job's condition keeps a pull_request run from starting it.
	draftUnreached draftHandling = iota
	// draftStops: the job fails a draft before its work, in its own first step or in the first
	// step of every job of the reusable workflow of this repository it calls (stopsOnDraft).
	draftStops
	// draftNeedSkip: the job needs a job held back on a draft (heldOnDraft), and its condition
	// does not run it after a need failed or was skipped, so GitHub skips it on a draft and
	// reports the skip as successful, whatever its own first step is.
	draftNeedSkip
	// draftConditionSkip: the job's condition reads the draft flag.
	draftConditionSkip
	// draftAdvisoryStop: the job fails a draft in its first step but continues on error.
	draftAdvisoryStop
	// draftRuns: the job runs its work on a draft.
	draftRuns
)

// heldOnDraft reports whether a job with handling h does not run its work on a draft and holds
// back every job that needs it: GitHub skips a job whose need failed or was skipped.
func heldOnDraft(h draftHandling) bool {
	return h == draftUnreached || h == draftStops || h == draftNeedSkip || h == draftConditionSkip
}

// draftHandlings decides each job's draftHandling: first from the job alone (ownDraftHandling),
// then by following needs until no job moves (followHeldNeeds). A job moves at most twice, to
// draftNeedSkip and then to draftUnreached, so 2n+1 passes over n jobs reach that point.
func (w *triggerWorkflow) draftHandlings(callees map[string]*workflowSpec) map[string]draftHandling {
	ids := sortedJobIDs(w.spec.Jobs)
	handling := make(map[string]draftHandling, len(ids))
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := w.spec.Jobs[ids[i]]
		handling[ids[i]] = ownDraftHandling(&job, callees)
	}
	for pass := 0; pass <= 2*len(ids) && pass <= 2*maxJobsPerFile; pass++ {
		if !followHeldNeeds(w.spec.Jobs, ids, handling) {
			break
		}
	}
	return handling
}

// ownDraftHandling decides what job does on a draft from the job alone.
func ownDraftHandling(job *workflowJob, callees map[string]*workflowSpec) draftHandling {
	switch {
	case len(reachingEvents(job.If, []string{pullRequestEvent})) == 0:
		return draftUnreached
	case strings.Contains(job.If, ghworkflow.PullRequestDraftField):
		return draftConditionSkip
	case !stopsOnDraft(job, callees):
		return draftRuns
	case advisoryJob(job.ContinueOnError):
		return draftAdvisoryStop
	}
	return draftStops
}

// stopsOnDraft reports whether job fails a draft before its work: its first step is the draft
// step, or it calls a reusable workflow of this repository (one of callees) whose every job
// begins with that step. A reusable workflow of another repository cannot be read here.
func stopsOnDraft(job *workflowJob, callees map[string]*workflowSpec) bool {
	if job.Uses == "" {
		return len(job.Steps) > 0 && ghworkflow.DraftStepFault(&job.Steps[0]) == nil
	}
	name, local := strings.CutPrefix(job.Uses, "./"+plannedWorkflowDir)
	callee := callees[name]
	if !local || callee == nil || len(callee.Jobs) == 0 {
		return false
	}
	ids := sortedJobIDs(callee.Jobs)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		steps := callee.Jobs[ids[i]].Steps
		if len(steps) == 0 || ghworkflow.DraftStepFault(&steps[0]) != nil {
			return false
		}
	}
	return true
}

// followHeldNeeds moves every job a pull_request run starts whose condition does not run it after
// a failed or skipped need, and whose needs hold it back (heldNeed), to the handling its needs
// give it: GitHub skips such a job, so its own first step never runs. A job its condition already
// skips keeps its own finding. It reports whether any job moved.
func followHeldNeeds(jobs map[string]workflowJob, ids []string, handling map[string]draftHandling) bool {
	moved := false
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := jobs[ids[i]]
		current := handling[ids[i]]
		if current == draftUnreached || current == draftConditionSkip || runsAfterFailedNeed(job.If) {
			continue
		}
		if next, _, held := heldNeed(job.NeedIDs(), handling); held && next != current {
			handling[ids[i]], moved = next, true
		}
	}
	return moved
}

// heldNeed says how needs hold back a job that does not run after a failed need, naming the need
// that does: draftUnreached when a pull_request run never starts one of them, so the job is
// skipped on every pull request; otherwise draftNeedSkip and the first need held back on a draft;
// held is false when no need is held back.
func heldNeed(needs []string, handling map[string]draftHandling) (next draftHandling, need string, held bool) {
	for j := 0; j < len(needs) && j < maxJobsPerFile; j++ {
		h, known := handling[needs[j]]
		switch {
		case !known || !heldOnDraft(h):
			continue
		case h == draftUnreached:
			return draftUnreached, needs[j], true
		case !held:
			next, need, held = draftNeedSkip, needs[j], true
		}
	}
	return next, need, held
}

// runsAfterFailedNeed reports whether a job condition calls a status function that holds after a
// need failed: always(), failure() or cancelled(), negated or not.
func runsAfterFailedNeed(condition string) bool {
	compact := strings.Join(strings.Fields(condition), "")
	return strings.Contains(compact, "always()") || strings.Contains(compact, "failure()") ||
		strings.Contains(compact, "cancelled()")
}

// draftProblem says what a job with handling h does wrong on a draft, or returns "" for a job
// that is held back on a draft without passing it. handling holds every job's handling, for the
// need that holds back a draftNeedSkip job.
func draftProblem(h draftHandling, job *workflowJob, handling map[string]draftHandling) string {
	switch h {
	case draftNeedSkip:
		_, need, _ := heldNeed(job.NeedIDs(), handling)
		return fmt.Sprintf("is skipped on a draft because it needs %s, which is held back on one: GitHub reports the "+
			"skipped job as successful, which a required check accepts; drop the need and begin the job with the draft "+
			"step %q, or begin it with that step and run it after a failed need (if: ${{ !cancelled() }}) as an "+
			"aggregate job does", need, ghworkflow.HostedGateDraftStepName)
	case draftConditionSkip:
		return fmt.Sprintf("skips a draft with the job condition %q: GitHub reports a job its condition skipped as "+
			"successful, which a required check accepts; fail the draft in a first step %q instead",
			strings.TrimSpace(job.If), ghworkflow.HostedGateDraftStepName)
	case draftAdvisoryStop:
		return "stops a draft in its first step but continues on error, so the failed draft reports success, " +
			"which a required check accepts"
	case draftRuns:
		if job.Uses != "" {
			return fmt.Sprintf("runs on a draft pull request: it calls %s, whose jobs this repository does not show "+
				"each beginning with the draft step %q, and it needs no job that stops a draft", job.Uses, ghworkflow.HostedGateDraftStepName)
		}
		return fmt.Sprintf("runs on a draft pull request: its first step is not the draft step %q, and it needs no "+
			"job that stops a draft", ghworkflow.HostedGateDraftStepName)
	}
	return ""
}

// judgeWorkflowTriggers renders the check's lines from the findings of read workflows and the
// HISS-18 entries: each excepted workflow, each reported finding, each stale entry, then the
// verdict.
func judgeWorkflowTriggers(read int, findings []workflowTriggerFinding, entries []config.Exception, today time.Time) string {
	used := make([]bool, len(entries))
	var lines []string
	groups := findingsByWorkflow(findings)
	reported, reportedWorkflows, excepted := 0, 0, 0
	for i := 0; i < len(groups) && i < maxWorkflowFiles; i++ {
		live, expired := config.ExceptionFor(entries, groups[i][0].Workflow, today, used)
		if live != nil {
			lines = append(lines, exceptedWorkflowTriggers(groups[i], *live))
			excepted++
			continue
		}
		lines = append(lines, reportedWorkflowTriggers(groups[i], expired)...)
		reported, reportedWorkflows = reported+len(groups[i]), reportedWorkflows+1
	}
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		if !used[i] {
			lines = append(lines, fmt.Sprintf("[WARN] %s: the exceptions entry (rule %s, %s) excuses no finding, "+
				"because the workflow it names has none; remove it.", workflowTriggerGate, entries[i].Rule, entries[i].Target()))
		}
	}
	return strings.Join(append(lines, workflowTriggerVerdict(read, reported, reportedWorkflows, excepted)), "\n")
}

// findingsByWorkflow groups findings, which arrive workflow by workflow, into one slice per
// workflow in that order.
func findingsByWorkflow(findings []workflowTriggerFinding) [][]workflowTriggerFinding {
	var groups [][]workflowTriggerFinding
	for i := 0; i < len(findings) && i < maxWorkflowFiles*2*maxJobsPerFile; i++ {
		last := len(groups) - 1
		if last >= 0 && groups[last][0].Workflow == findings[i].Workflow {
			groups[last] = append(groups[last], findings[i])
			continue
		}
		groups = append(groups, []workflowTriggerFinding{findings[i]})
	}
	return groups
}

// exceptedWorkflowTriggers is the [PASS] line of a workflow a live entry excepts, with the
// entry's expiry and reason and each finding it keeps from being reported.
func exceptedWorkflowTriggers(group []workflowTriggerFinding, entry config.Exception) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[PASS] %s: %s excepted until %s by the exceptions entry (rule %s, %s): %s",
		workflowTriggerGate, group[0].Workflow, entry.Expires, entry.Rule, entry.Target(), entry.Reason)
	for i := 0; i < len(group) && i < 2*maxJobsPerFile; i++ {
		b.WriteString("\n  - " + group[i].String())
	}
	return b.String()
}

// reportedWorkflowTriggers is one [WARN] line per finding of a workflow no live entry excepts,
// led by a line naming its expired entry when it has one.
func reportedWorkflowTriggers(group []workflowTriggerFinding, expired *config.Exception) []string {
	lines := make([]string, 0, len(group)+1)
	if expired != nil {
		lines = append(lines, fmt.Sprintf("[WARN] %s: the exceptions entry (rule %s, %s) expired on %s, so the findings "+
			"below are reported as if it were not declared; move the workflow to the hosted gate shape, or renew the entry "+
			"with a new reason and expiry.", workflowTriggerGate, expired.Rule, expired.Target(), expired.Expires))
	}
	for i := 0; i < len(group) && i < 2*maxJobsPerFile; i++ {
		lines = append(lines, "[WARN] "+workflowTriggerGate+": "+group[i].String()+".")
	}
	return lines
}

// workflowTriggerVerdict is the check's last line: a skip without workflows, a warning that
// counts the reported findings and names both ways out, or a pass.
func workflowTriggerVerdict(read, reported, reportedWorkflows, excepted int) string {
	switch {
	case read == 0:
		return fmt.Sprintf("[SKIP] %s: the repository holds no workflow under %s, so no trigger was judged.",
			workflowTriggerGate, ghworkflow.Dir)
	case reported > 0:
		return fmt.Sprintf("[WARN] %s: %d of %d workflows start runs this check counts as wasted (findings: %d); "+
			"reported, not enforced, because HISS-18's failure action is a CI optimization gate. Move each gate to the "+
			"hosted gate shape (%s), or declare a workflow that must run on every branch or draft in the exceptions list "+
			"of .standards.yaml (rule %s, its path, a reason, and an expiry at most %d days ahead).", workflowTriggerGate,
			reportedWorkflows, read, reported, workflowTriggerGuide, config.ExceptionRuleWorkflowTriggers, config.MaxExceptionDays)
	}
	scope := ""
	if excepted > 0 {
		scope = fmt.Sprintf(" outside the excepted workflows above (%d)", excepted)
	}
	return fmt.Sprintf("[PASS] %s: workflows read: %d; no push trigger runs on every branch and no job a pull request "+
		"starts runs its work on a draft%s.", workflowTriggerGate, read, scope)
}
