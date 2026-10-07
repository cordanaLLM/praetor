package forge

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// allsGreenAction is the action re-actors/alls-green. Its one step fails when a job named in its
// jobs input reported a result the step's policy rejects (https://github.com/re-actors/alls-green).
const allsGreenAction = "re-actors/alls-green"

// allsGreenReleases maps the commit of each re-actors/alls-green release whose source was read to
// its tag: the commit each annotated tag peels to, as `git ls-remote --tags` listed them on
// 2026-10-07. Every one of them declares the jobs, allowed-failures and allowed-skips inputs,
// accepts a need's success, rejects a need's failure or cancellation unless allowed-failures names
// it, and rejects a skip unless allowed-skips or allowed-failures names it
// (src/normalize_needed_jobs_status.py from v1.1.0, src/job_outcome.py at v1.3.0).
//
// The proof assumes the code at a listed commit is the code GitHub runs for it; a commit is
// immutable, so a moved tag does not change it. Any other commit proves nothing: v1.0.0 to v1.0.2
// declare no allowed-skips, and a later release, a branch head or a commit only a fork holds has
// not been read. Add a release here only after reading its decision code.
var allsGreenReleases = map[string]string{
	"3a2de129f0713010a71314c74e33c0e3ef90e696": "v1.1.0",
	"198badcb65a1a44528f27d5da555c4be9f12eac6": "v1.2.0",
	"13b4244b312e8a314951e03958a2f91519a6a3c9": "v1.2.1",
	"05ac9388f0aebcb5727afa17fcccfecd6f8ec5fe": "v1.2.2",
	"b5b5b37504aa4183270bd3d855c52a67f212be35": "v1.3.0",
}

// jobIDShape is the form GitHub allows a job id: a letter or _, then letters, digits, - and _.
var jobIDShape = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*$`)

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
// other step. The step runs the action pinned by full commit SHA (util.ParseSHAPin) at a release
// in allsGreenReleases, so the code it runs is code whose decision was read; its jobs input is
// toJSON(needs); it carries no continue-on-error and no input the action does not declare; and
// allowed-failures and allowed-skips are absent or literal lists of job ids (allsGreenNames). A
// tag or branch reference, an unlisted commit, an expression the file cannot evaluate, or a jobs
// input naming fewer jobs proves nothing.
func allsGreenStep(step *workflowStep) (allsGreenPolicy, bool) {
	pin, pinned := util.ParseSHAPin(step.Uses)
	_, released := allsGreenReleases[pin.SHA]
	if !pinned || !released || !strings.EqualFold(pin.Action, allsGreenAction) || advisoryJob(step.ContinueOnError) || !allsGreenInputsOnly(step.With) {
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
// JSON, a list of more names than a workflow has jobs, or a name that is no job id (jobIDShape).
// Releases before v1.3.0 paste the value into a bash here-document, where a name such as
// $(echo go) would expand to another job's id.
func allsGreenNames(value any) ([]string, bool) {
	if value == nil {
		return nil, true
	}
	text, isText := value.(string)
	if !isText || strings.Contains(text, expressionOpen) {
		return nil, false
	}
	names, read := util.SplitCSV(text), strings.Count(text, ",") < maxJobsPerFile
	if json.Valid([]byte(text)) {
		names = nil
		err := json.Unmarshal([]byte(text), &names)
		read = err == nil && names != nil && len(names) <= maxJobsPerFile
	}
	return names, read && !slices.ContainsFunc(names, func(name string) bool { return !jobIDShape.MatchString(name) })
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
