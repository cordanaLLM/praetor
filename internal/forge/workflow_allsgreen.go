package forge

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// allsGreenAction is the action re-actors/alls-green. Its one step fails when a job named in its
// jobs input reported a result the step's policy rejects (action.yml and src/job_outcome.py on its
// release/v1 branch, https://github.com/re-actors/alls-green).
const allsGreenAction = "re-actors/alls-green"

// allsGreenEveryNeed is the jobs input, whitespace removed, that hands the action the result of
// every job the step's job needs.
const allsGreenEveryNeed = "${{toJSON(needs)}}"

// allsGreenInputs are the inputs the action declares (action.yml).
var allsGreenInputs = []string{"jobs", "allowed-failures", "allowed-skips"}

// allsGreenPolicy is the jobs an alls-green step accepts a failure or a skip of: its
// allowed-failures and allowed-skips inputs.
type allsGreenPolicy struct {
	mayFail, maySkip []string
}

// allsGreenStep returns the policy of an alls-green step the file can judge, and false for any
// other step. The step runs the action pinned by full commit SHA (util.ParseSHAPin), so the code it
// runs is the code the file names; its jobs input is toJSON(needs); it carries no
// continue-on-error and no input the action does not declare; and allowed-failures and
// allowed-skips are absent or literal lists (allsGreenNames). A tag or branch reference, an
// expression the file cannot evaluate, or a jobs input naming fewer jobs proves nothing.
func allsGreenStep(step *workflowStep) (allsGreenPolicy, bool) {
	pin, pinned := util.ParseSHAPin(step.Uses)
	if !pinned || !strings.EqualFold(pin.Action, allsGreenAction) || advisoryJob(step.ContinueOnError) || !allsGreenInputsOnly(step.With) {
		return allsGreenPolicy{}, false
	}
	jobs, jobsText := step.With["jobs"].(string)
	mayFail, failuresRead := allsGreenNames(step.With["allowed-failures"])
	maySkip, skipsRead := allsGreenNames(step.With["allowed-skips"])
	everyNeed := jobsText && strings.EqualFold(strings.Join(strings.Fields(jobs), ""), allsGreenEveryNeed)
	if !everyNeed || !failuresRead || !skipsRead {
		return allsGreenPolicy{}, false
	}
	return allsGreenPolicy{mayFail: mayFail, maySkip: maySkip}, true
}

// allsGreenInputsOnly reports whether every input with passes is one the action declares.
func allsGreenInputsOnly(with map[string]any) bool {
	for name := range with {
		if !slices.Contains(allsGreenInputs, name) {
			return false
		}
	}
	return true
}

// allsGreenNames reads an allowed-failures or allowed-skips value as the action parses it
// (parse_as_list in src/normalize_needed_jobs_status.py): a JSON list of job names, or else a
// comma-separated list (util.SplitCSV), blank names dropped. An absent value names no job. A value
// the file cannot settle cannot be read: an expression, a YAML value that is no string, any other
// JSON, or a list of more names than a workflow has jobs.
func allsGreenNames(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	text, isText := value.(string)
	if !isText || strings.Contains(text, expressionOpen) {
		return nil, false
	}
	if json.Valid([]byte(text)) {
		var names []string
		err := json.Unmarshal([]byte(text), &names)
		return names, err == nil && names != nil && len(names) <= maxJobsPerFile
	}
	return util.SplitCSV(text), strings.Count(text, ",") < maxJobsPerFile
}

// rejects reports whether a need reported a result the policy rejects (evaluate_jobs in
// src/job_outcome.py).
func (p allsGreenPolicy) rejects(results map[string]string) bool {
	for id, result := range results {
		if !p.accepts(id, result) {
			return true
		}
	}
	return false
}

// accepts reports whether the policy accepts result from the job id: any result from a job
// allowed to fail, success or skipped from a job allowed to be skipped, and success alone from any
// other job. Names match regardless of case, which can only accept more results than the action
// does, so a need is covered only when the action rejects its failure under every reading.
func (p allsGreenPolicy) accepts(id, result string) bool {
	switch {
	case namesJob(p.mayFail, id), result == needsResultSuccess:
		return true
	case result == needsResultSkipped:
		return namesJob(p.maySkip, id)
	}
	return false
}

// namesJob reports whether names holds the job id, regardless of case.
func namesJob(names []string, id string) bool {
	return slices.ContainsFunc(names, func(name string) bool { return strings.EqualFold(name, id) })
}
