package forge

import (
	"slices"
	"strconv"
	"strings"
	"unicode"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// The results GitHub reports for a needed job in needs.<job_id>.result.
const (
	needsResultSuccess   = "success"
	needsResultFailure   = "failure"
	needsResultCancelled = "cancelled"
	needsResultSkipped   = "skipped"
)

// needsResultsPrefix is the object filter that lists the results of every needed job, as the
// argument of contains() spells it with whitespace removed.
const needsResultsPrefix = "contains(needs.*.result,"

// aggregateCoveredJobs returns the ids of the jobs that proven aggregates of jobs cover
// (provenAggregateNeeds). ids are the job ids in the order the caller walks them.
//
// A covered job is not a required check of its own: the aggregate fails whenever it fails or is
// cancelled, and passes when its condition skipped it, so requiring the aggregate alone keeps the
// branch protected while a skipped lane, a planner that proves nothing and a matrix whose legs a
// skip never reports cannot hold a pull request (#76). A job the aggregate reaches only through
// another job is not covered: when it fails, the job between is skipped, and a skipped need does
// not fail the aggregate.
func aggregateCoveredJobs(jobs map[string]workflowJob, ids []string) map[string]bool {
	covered := make(map[string]bool)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := jobs[ids[i]]
		needs := provenAggregateNeeds(&job)
		for j := 0; j < len(needs) && j < maxJobsPerFile; j++ {
			covered[needs[j]] = true
		}
	}
	return covered
}

// provenAggregateNeeds returns the jobs job needs whose failure or cancellation its steps prove
// fails it, or nil when job is no proven aggregate. A proven aggregate needs at least one job, is
// not advisory, holds on every run (holdsOnEveryRun, after a leading Renovate skip as
// reportsOnEveryPullRequest reads it), so it reports even when a need failed or was skipped, and
// its steps pass when every need succeeded. A need is covered when its steps fail whenever that
// need alone failed or was cancelled (failsWhenNeedFails). An aggregate the file cannot prove, such
// as one that only echoes, covers nothing and stays a required check beside the jobs it needs.
func provenAggregateNeeds(job *workflowJob) []string {
	needs := job.NeedIDs()
	if len(needs) == 0 || advisoryJob(job.ContinueOnError) || !holdsOnEveryRun(withoutRenovateBranchSkip(job.If)) {
		return nil
	}
	if fails, known := aggregateFails(job.Steps, needsResults(needs, "", "")); !known || fails {
		return nil
	}
	var covered []string
	for i := 0; i < len(needs) && i < maxJobsPerFile; i++ {
		if failsWhenNeedFails(job.Steps, needs, needs[i]) {
			covered = append(covered, needs[i])
		}
	}
	return covered
}

// failsWhenNeedFails reports whether steps fail when need failed, and when it was cancelled, while
// every other need succeeded (aggregateFails). Both step kinds aggregateFails reads fail on a need
// whatever the others reported: a recognised step condition is a disjunction of per-need terms,
// and an alls-green step judges every need on its own, so the result holds on every run.
func failsWhenNeedFails(steps []workflowStep, needs []string, need string) bool {
	for _, result := range [...]string{needsResultFailure, needsResultCancelled} {
		if fails, known := aggregateFails(steps, needsResults(needs, need, result)); !known || !fails {
			return false
		}
	}
	return true
}

// needsResults maps every need to success, except changed, which reports result.
func needsResults(needs []string, changed, result string) map[string]string {
	results := make(map[string]string, len(needs))
	for i := 0; i < len(needs) && i < maxJobsPerFile; i++ {
		results[needs[i]] = needsResultSuccess
	}
	if changed != "" {
		results[changed] = result
	}
	return results
}

// aggregateFails reports whether an aggregate's steps fail the job when its needs report
// results, and whether the file can tell: the first step that fails it (stepFails) decides, and a
// step whose outcome stepFails cannot tell makes the outcome unknown.
func aggregateFails(steps []workflowStep, results map[string]string) (fails, known bool) {
	for i := 0; i < len(steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		if fails, known = stepFails(&steps[i], results); fails || !known {
			return fails, known
		}
	}
	return false, true
}

// stepFails reports whether step fails its job when the needs report results, and whether the
// file can tell. A failing step (failingStep) fails it whenever its condition holds, and an
// alls-green step (allsGreenStep) when its condition holds and a need reported a result its policy
// rejects. A step of either kind whose condition needsConditionHolds cannot read makes the outcome
// unknown. Every other step is taken to pass, which can only make a real run fail more often than
// this model says.
func stepFails(step *workflowStep, results map[string]string) (fails, known bool) {
	policy, judges := allsGreenStep(step)
	if !judges && !failingStep(step) {
		return false, true
	}
	holds, read := needsConditionHolds(step.If, results)
	if !holds || !read {
		return false, read
	}
	return !judges || policy.rejects(results), true
}

// failingStep reports whether step fails its job whenever it runs: a run: script whose last
// command is a failing exit (failingExit), with no other exit before it, and no
// continue-on-error.
func failingStep(step *workflowStep) bool {
	if step.Uses != "" || advisoryJob(step.ContinueOnError) {
		return false
	}
	commands := scriptCommands(step.Run)
	if len(commands) == 0 {
		return false
	}
	for i := 0; i < len(commands)-1 && i < maxScriptCommands; i++ {
		if slices.Contains(strings.FieldsFunc(commands[i], shellWordBreak), "exit") {
			return false
		}
	}
	return failingExit(commands[len(commands)-1])
}

// failingExit reports whether command is exit with a status from 1 to 255 and nothing after it.
// Such an exit ends a bash, sh, pwsh or cmd step with that status, and a shell that cannot read it
// fails the step on the error instead.
func failingExit(command string) bool {
	fields := strings.Fields(command)
	if len(fields) != 2 || fields[0] != "exit" {
		return false
	}
	status, err := strconv.Atoi(fields[1])
	return err == nil && status >= 1 && status <= 255
}

// maxScriptCommands bounds the lines failingStep reads from one run: script (HISS-02).
const maxScriptCommands = 64

// shellWordBreak splits a script line into words at whitespace and at the shell's command
// separators, so `if x; then exit 0; fi` holds the word exit and `echo exiting` does not.
func shellWordBreak(r rune) bool {
	return unicode.IsSpace(r) || strings.ContainsRune(";&|()", r)
}

// scriptCommands returns the trimmed lines of a run: script that are neither blank nor comments,
// or nil for a script longer than maxScriptCommands lines, which is no aggregate's exit step.
func scriptCommands(script string) []string {
	lines := strings.Split(script, "\n")
	if len(lines) > maxScriptCommands {
		return nil
	}
	var commands []string
	for i := 0; i < len(lines) && i < maxScriptCommands; i++ {
		line := strings.TrimSpace(lines[i])
		if line != "" && !strings.HasPrefix(line, "#") {
			commands = append(commands, line)
		}
	}
	return commands
}

// needsConditionHolds evaluates a step condition against the results of the job's needs, and
// reports whether it could: an empty condition holds, and so does a disjunction, bare or as one
// ${{ }} expression, of terms needsTermHolds reads. Any other condition, a conjunction, a
// negation or a status function included, cannot be read.
func needsConditionHolds(condition string, results map[string]string) (holds, known bool) {
	if strings.TrimSpace(condition) == "" {
		return true, true
	}
	expression, ok := expressionBody(condition)
	terms := strings.Split(strings.Join(strings.Fields(expression), ""), "||")
	if !ok || len(terms) > maxJobsPerFile {
		return false, false
	}
	for i := 0; i < len(terms) && i < maxJobsPerFile; i++ {
		term, read := needsTermHolds(terms[i], results)
		if !read {
			return false, false
		}
		holds = holds || term
	}
	return holds, true
}

// needsTermHolds evaluates one term, whitespace removed, against the needs' results:
// contains(needs.*.result, '<result>'), needs.<id>.result == '<result>' or
// needs.<id>.result != '<result>'. GitHub compares the strings without regard to case. A term
// naming a job that is not a need cannot be read: there the result is empty and the term would
// silently never hold.
func needsTermHolds(term string, results map[string]string) (holds, known bool) {
	if argument, found := strings.CutPrefix(term, needsResultsPrefix); found {
		literal, closed := strings.CutSuffix(argument, ")")
		want, quoted := expressionLiteral(literal)
		if !closed || !quoted {
			return false, false
		}
		for _, result := range results {
			if strings.EqualFold(result, want) {
				return true, true
			}
		}
		return false, true
	}
	return needsComparisonHolds(term, results)
}

// needsComparisonHolds evaluates needs.<id>.result == '<result>' or != '<result>'.
func needsComparisonHolds(term string, results map[string]string) (holds, known bool) {
	rest, found := strings.CutPrefix(term, "needs.")
	id, comparison, dotted := strings.Cut(rest, ".result")
	result, isNeed := results[id]
	if !found || !dotted || !isNeed || len(comparison) < 2 {
		return false, false
	}
	want, quoted := expressionLiteral(comparison[2:])
	if !quoted {
		return false, false
	}
	switch comparison[:2] {
	case "==":
		return strings.EqualFold(result, want), true
	case "!=":
		return !strings.EqualFold(result, want), true
	}
	return false, false
}

// expressionLiteral returns the text of a single-quoted expression string literal that holds no
// quote, the only kind a result name needs.
func expressionLiteral(text string) (string, bool) {
	inner, opened := strings.CutPrefix(text, "'")
	value, closed := strings.CutSuffix(inner, "'")
	if !opened || !closed || value == "" || strings.Contains(value, "'") {
		return "", false
	}
	return value, true
}
