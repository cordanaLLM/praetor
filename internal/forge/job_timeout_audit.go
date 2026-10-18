// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const (
	jobTimeoutGate       = "job-timeout"
	staticCeilingMinutes = 120
	ceilingFloorMinutes  = 30.0
	runawayMultiplier    = 4.0
	cancelRiskMultiplier = 1.25
	maxWorkflowRuns      = 3
)

// JobTimeoutOptions is what AuditJobTimeouts reads: the repository root, the manifest's
// exceptions list (the check reads the entries of config.ExceptionRuleJobTimeout), the day
// those entries' expiry is judged against, the forge reader, and branch.
type JobTimeoutOptions struct {
	Root          string
	Exceptions    []config.Exception
	Today         time.Time
	Reader        ActionsReader
	OfflineReason string
	Branch        string
}

type jobTimeoutFinding struct {
	Workflow string
	Line     int
	JobID    string
	Reason   string
}

type jobVerdict struct {
	finding    *jobTimeoutFinding
	info       string
	isMeasured bool
	isStatic   bool
}

type workflowRunMetrics struct {
	slowest      map[string]float64
	hadReadError bool
	readErr      string
}

type workflowAuditResult struct {
	findings        []jobTimeoutFinding
	infos           []string
	measuredCount   int
	noSuccessCount  int
	unreadableCount int
	readErr         string
}

// AuditJobTimeouts checks workflow jobs for timeout-minutes declarations and duration limits.
func AuditJobTimeouts(ctx context.Context, opts JobTimeoutOptions) (string, error) {
	files, err := readWorkflowFiles(ctx, opts.Root)
	if err != nil {
		return "", err
	}
	if len(files) > maxWorkflowFiles {
		files = files[:maxWorkflowFiles]
	}
	canMeasure := opts.Reader != nil && opts.OfflineReason == "" && opts.Branch != ""
	entries := config.ExceptionsFor(opts.Exceptions, config.ExceptionRuleJobTimeout)
	var allFindings []jobTimeoutFinding
	var infoLines []string
	measuredCount, noSuccessCount, unreadableCount := 0, 0, 0
	var firstReadErr string
	for _, file := range files {
		res, err := auditWorkflowJobTimeouts(ctx, file, opts, canMeasure)
		if err != nil {
			return "", err
		}
		allFindings = append(allFindings, res.findings...)
		infoLines = append(infoLines, res.infos...)
		measuredCount += res.measuredCount
		noSuccessCount += res.noSuccessCount
		unreadableCount += res.unreadableCount
		if firstReadErr == "" && res.readErr != "" {
			firstReadErr = res.readErr
		}
	}
	staticCount := noSuccessCount + unreadableCount
	pathInfo := formatJobTimeoutPathInfo(canMeasure, opts, measuredCount, staticCount, noSuccessCount, unreadableCount, firstReadErr)
	var lines []string
	lines = append(lines, pathInfo)
	lines = append(lines, infoLines...)
	lines = append(lines, applyExceptions(allFindings, entries, opts.Today)...)
	lines = append(lines, "[PASS] "+jobTimeoutGate)
	return strings.Join(lines, "\n"), nil
}

func formatJobTimeoutPathInfo(canMeasure bool, opts JobTimeoutOptions, measured, static, noSuccess, unreadable int, readErr string) string {
	if !canMeasure {
		reason := opts.OfflineReason
		if reason == "" {
			reason = "no forge reader or branch available"
		}
		return fmt.Sprintf("[INFO] %s: static path only (%s)", jobTimeoutGate, reason)
	}
	if unreadable > 0 {
		return fmt.Sprintf("[INFO] %s: measured path for %d jobs (forge runs of %s), static path for %d jobs: %d without a successful run, %d whose runs could not be read (%s)",
			jobTimeoutGate, measured, opts.Branch, static, noSuccess, unreadable, readErr)
	}
	return fmt.Sprintf("[INFO] %s: measured path for %d jobs (forge runs of %s), static path for %d jobs (no successful run)",
		jobTimeoutGate, measured, opts.Branch, static)
}

func auditWorkflowJobTimeouts(ctx context.Context, file workflowFile, opts JobTimeoutOptions, canMeasure bool) (workflowAuditResult, error) {
	spec, err := ghworkflow.Parse(file.Data)
	if err != nil {
		return workflowAuditResult{}, err
	}
	rel := ghworkflow.Dir + "/" + file.Name
	keyLines := workflowJobKeyLines(file.Data)
	var metrics workflowRunMetrics
	if canMeasure {
		metrics = readWorkflowSlowestJobs(ctx, opts.Reader, file.Name, opts.Branch, &spec)
	}
	var res workflowAuditResult
	for _, jobID := range ghworkflow.SortedJobIDs(spec.Jobs) {
		job := spec.Jobs[jobID]
		s, hasS := metrics.slowest[jobID]
		v := judgeJob(rel, jobID, job, keyLines[jobID], hasS, s)
		accumulateJobVerdict(&res, v, metrics.hadReadError)
	}
	if res.unreadableCount > 0 {
		res.readErr = metrics.readErr
	}
	return res, nil
}

func accumulateJobVerdict(res *workflowAuditResult, v jobVerdict, hadReadError bool) {
	if v.finding != nil {
		res.findings = append(res.findings, *v.finding)
	}
	if v.info != "" {
		res.infos = append(res.infos, v.info)
	}
	if v.isMeasured {
		res.measuredCount++
		return
	}
	if v.isStatic {
		if hadReadError {
			res.unreadableCount++
		} else {
			res.noSuccessCount++
		}
	}
}

func judgeJob(rel, jobID string, job ghworkflow.Job, jobLine int, hasS bool, s float64) jobVerdict {
	if job.Uses != "" || len(job.Steps) == 0 {
		return jobVerdict{}
	}
	if job.TimeoutMinutes.Kind == 0 || (job.TimeoutMinutes.Value == "" && job.TimeoutMinutes.Tag == "!!null") {
		f := &jobTimeoutFinding{Workflow: rel, Line: jobLine, JobID: jobID, Reason: "declares no timeout-minutes"}
		return jobVerdict{finding: f, isMeasured: hasS, isStatic: !hasS}
	}
	line := job.TimeoutMinutes.Line
	if line == 0 {
		line = jobLine
	}
	val := strings.TrimSpace(job.TimeoutMinutes.Value)
	if strings.Contains(val, "${{") {
		info := fmt.Sprintf("[INFO] %s: %s:%d job %s: timeout-minutes is an expression (%s), not judged",
			jobTimeoutGate, rel, line, jobID, val)
		return jobVerdict{info: info}
	}
	declared, err := strconv.Atoi(val)
	if err != nil {
		f := &jobTimeoutFinding{Workflow: rel, Line: line, JobID: jobID, Reason: fmt.Sprintf("timeout-minutes %q is unreadable", val)}
		return jobVerdict{finding: f, isMeasured: hasS, isStatic: !hasS}
	}
	if hasS {
		return judgeMeasuredJob(rel, line, jobID, declared, s)
	}
	return judgeStaticJob(rel, line, jobID, declared)
}

func judgeMeasuredJob(rel string, line int, jobID string, declared int, s float64) jobVerdict {
	declFloat := float64(declared)
	if declFloat < cancelRiskMultiplier*s {
		reason := fmt.Sprintf("timeout-minutes %d is below 1.25 x measured duration %.1fm (cancel risk)", declared, s)
		return jobVerdict{finding: &jobTimeoutFinding{Workflow: rel, Line: line, JobID: jobID, Reason: reason}, isMeasured: true}
	}
	ceil := math.Max(ceilingFloorMinutes, runawayMultiplier*s)
	if declFloat > ceil {
		reason := fmt.Sprintf("timeout-minutes %d is above max(30, 4 x %.1fm) bound of %.0fm (runaway)", declared, s, ceil)
		return jobVerdict{finding: &jobTimeoutFinding{Workflow: rel, Line: line, JobID: jobID, Reason: reason}, isMeasured: true}
	}
	return jobVerdict{isMeasured: true}
}

func judgeStaticJob(rel string, line int, jobID string, declared int) jobVerdict {
	if declared > staticCeilingMinutes {
		reason := fmt.Sprintf("timeout-minutes %d is above %dm bound", declared, staticCeilingMinutes)
		return jobVerdict{finding: &jobTimeoutFinding{Workflow: rel, Line: line, JobID: jobID, Reason: reason}, isStatic: true}
	}
	return jobVerdict{isStatic: true}
}

func applyExceptions(findings []jobTimeoutFinding, entries []config.Exception, today time.Time) []string {
	used := make([]bool, len(entries))
	byWorkflow := make(map[string][]jobTimeoutFinding)
	var wfOrder []string
	for _, f := range findings {
		if _, exists := byWorkflow[f.Workflow]; !exists {
			wfOrder = append(wfOrder, f.Workflow)
		}
		byWorkflow[f.Workflow] = append(byWorkflow[f.Workflow], f)
	}
	var lines []string
	for _, wf := range wfOrder {
		wFindings := byWorkflow[wf]
		live, _ := config.ExceptionFor(entries, wf, today, used)
		if live != nil {
			lines = append(lines, fmt.Sprintf("[PASS] %s: %s excepted until %s: %s",
				jobTimeoutGate, wf, live.Expires, live.Reason))
			continue
		}
		for _, f := range wFindings {
			lines = append(lines, fmt.Sprintf("[WARN] %s: %s:%d job %s: %s",
				jobTimeoutGate, f.Workflow, f.Line, f.JobID, f.Reason))
		}
	}
	for i := 0; i < len(entries) && i < config.MaxExceptions; i++ {
		if !used[i] {
			lines = append(lines, fmt.Sprintf("[WARN] %s: the exceptions entry (rule %s, %s) excuses no finding, because the workflow it names has none; remove it.",
				jobTimeoutGate, entries[i].Rule, entries[i].Target()))
		}
	}
	return lines
}

func readWorkflowSlowestJobs(ctx context.Context, reader ActionsReader, workflow, branch string, spec *ghworkflow.Spec) workflowRunMetrics {
	history, err := reader.WorkflowRunHistory(ctx, workflow, branch)
	if err != nil {
		return workflowRunMetrics{hadReadError: true, readErr: historyReadError(err)}
	}
	slowest := make(map[string]float64)
	successfulCount := 0
	var firstErr string
	hadErr := false
	for i := 0; i < len(history.Completed) && successfulCount < maxWorkflowRuns; i++ {
		run := history.Completed[i]
		if run.Conclusion != "success" {
			continue
		}
		successfulCount++
		if run.ID == 0 {
			hadErr = true
			if firstErr == "" {
				firstErr = "run without ID"
			}
			continue
		}
		jobs, err := reader.WorkflowRunJobs(ctx, run.ID)
		if err != nil {
			hadErr = true
			if firstErr == "" {
				firstErr = jobsReadError(err)
			}
			continue
		}
		updateSlowestJobs(slowest, jobs, spec)
	}
	return workflowRunMetrics{slowest: slowest, hadReadError: hadErr, readErr: firstErr}
}

func historyReadError(err error) string {
	return err.Error()
}

func jobsReadError(err error) string {
	msg := err.Error()
	if strings.Contains(msg, "read run jobs") {
		return msg
	}
	return "read run jobs: " + msg
}

func updateSlowestJobs(slowest map[string]float64, jobs []WorkflowJobRecord, spec *ghworkflow.Spec) {
	for i := 0; i < len(jobs) && i < maxWorkflowRunJobs; i++ {
		jobRec := jobs[i]
		if jobRec.Conclusion != "success" {
			continue
		}
		dur, ok := parseJobDuration(jobRec.StartedAt, jobRec.CompletedAt)
		if !ok {
			continue
		}
		matchSlowestJob(slowest, jobRec.Name, dur, spec)
	}
}

func matchSlowestJob(slowest map[string]float64, forgeName string, dur float64, spec *ghworkflow.Spec) {
	for jobID, job := range spec.Jobs {
		target := job.Name
		if target == "" {
			target = jobID
		}
		if jobMatches(target, forgeName) {
			if dur > slowest[jobID] {
				slowest[jobID] = dur
			}
		}
	}
}

func jobMatches(target, forgeName string) bool {
	if forgeName == target {
		return true
	}
	targetPrefix := matchJobPrefix(target)
	forgePrefix := matchJobPrefix(forgeName)
	return targetPrefix != "" && targetPrefix == forgePrefix
}

func matchJobPrefix(name string) string {
	if idx := strings.Index(name, "${{"); idx != -1 {
		name = name[:idx]
	}
	if idx := strings.Index(name, " ("); idx != -1 {
		name = name[:idx]
	}
	return strings.TrimSpace(name)
}

func parseJobDuration(startedAt, completedAt string) (float64, bool) {
	if startedAt == "" || completedAt == "" {
		return 0, false
	}
	start, err1 := time.Parse(time.RFC3339, startedAt)
	end, err2 := time.Parse(time.RFC3339, completedAt)
	if err1 != nil || err2 != nil || !end.After(start) {
		return 0, false
	}
	return end.Sub(start).Minutes(), true
}

func workflowJobKeyLines(data []byte) map[string]int {
	var document yaml.Node
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil
	}
	lines := make(map[string]int)
	if document.Kind != yaml.DocumentNode || len(document.Content) == 0 {
		return lines
	}
	jobs := util.YAMLMappingValue(document.Content[0], "jobs")
	if jobs == nil || jobs.Kind != yaml.MappingNode {
		return lines
	}
	for i := 0; i+1 < len(jobs.Content) && i < 2*ghworkflow.MaxJobsPerFile; i += 2 {
		lines[jobs.Content[i].Value] = jobs.Content[i].Line
	}
	return lines
}
