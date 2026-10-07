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

// aggregateCoveredJobs returns the ids of the jobs that proven aggregates of spec cover
// (provenAggregateNeeds). ids are the job ids in the order the caller walks them.
//
// A covered job is not a required check of its own: the aggregate fails whenever it fails or is
// cancelled, and passes when its condition skipped it, so requiring the aggregate alone keeps the
// branch protected while a skipped lane, a planner that proves nothing and a matrix whose legs a
// skip never reports cannot hold a pull request (#76). A job the aggregate reaches only through
// another job is not covered: when it fails, the job between is skipped, and a skipped need does
// not fail the aggregate.
func aggregateCoveredJobs(spec *workflowSpec, ids []string) map[string]bool {
	covered := make(map[string]bool)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		needs := provenAggregateNeeds(spec, &job)
		for j := 0; j < len(needs) && j < maxJobsPerFile; j++ {
			covered[needs[j]] = true
		}
	}
	return covered
}

// provenAggregateNeeds returns the jobs job needs that it is proven to fail on, or nil when job
// is no proven aggregate. The proof reads an allow-list of shapes and refuses every other one
// (aggregateShape): a job that needs at least one job, runs under always() alone, is not advisory
// and has only exit steps and alls-green steps. Its steps must pass when every need succeeded,
// and a need is covered when they fail whenever that need alone failed or was cancelled
// (failsWhenNeedFails). An aggregate the file cannot prove, such as one that only echoes, covers
// nothing and stays a required check beside the jobs it needs.
func provenAggregateNeeds(spec *workflowSpec, job *workflowJob) []string {
	needs := job.NeedIDs()
	steps, renovate, shaped := aggregateShape(spec, job)
	if len(needs) == 0 || !shaped {
		return nil
	}
	if fails, known := aggregateFails(steps, needsResults(needs, "", "")); !known || fails {
		return nil
	}
	var covered []string
	for i := 0; i < len(needs) && i < maxJobsPerFile; i++ {
		if coversNeed(spec, steps, needs, needs[i], renovate) {
			covered = append(covered, needs[i])
		}
	}
	return covered
}

// coversNeed reports whether an aggregate with steps over needs covers need. Under a leading
// Renovate skip (renovate) the aggregate is skipped on a Renovate pull request, and a skipped
// required check passes, so it covers only a need that carries the same skip
// (carriesRenovateBranchSkip) and so does not run there either. A need that still runs on that
// pull request stays required, or a failure of it would merge.
func coversNeed(spec *workflowSpec, steps []gateStep, needs []string, need string, renovate bool) bool {
	if renovate && !carriesRenovateBranchSkip(spec.Jobs[need].If) {
		return false
	}
	return failsWhenNeedFails(steps, needs, need)
}

// aggregateShape returns the steps of job (gateSteps) and whether its condition leads with the
// Renovate skip, or false when job is not an aggregate in the allowed shape: its condition is
// always() alone (aggregateCondition), it is not advisory, and every step is an exit step or an
// alls-green step.
func aggregateShape(spec *workflowSpec, job *workflowJob) (steps []gateStep, renovate, shaped bool) {
	renovate, always := aggregateCondition(job.If)
	if !always || advisoryJob(job.ContinueOnError) {
		return nil, false, false
	}
	steps, shaped = gateSteps(spec, job)
	return steps, renovate, shaped
}

// aggregateCondition reads an aggregate's job condition. It holds (always) only for always()
// alone, bare or as one ${{ }} expression, after an optional leading renovateBranchSkip conjunct,
// which renovate reports (withoutRenovateBranchSkip).
//
// always() makes the job run on every run, a cancelled one included, so it reports even when a
// need failed or was skipped. `!cancelled()` and `success() || failure()` do not run on a
// cancelled run, and whether GitHub then reports the skipped gate as skipped, which satisfies a
// required check, or as cancelled is not verified (community discussion #26303 reports both), so
// neither covers a need. Any other condition can skip the gate on a run where a need failed.
func aggregateCondition(condition string) (renovate, always bool) {
	rest := withoutRenovateBranchSkip(condition)
	expression, closed := expressionBody(rest)
	always = closed && strings.Join(strings.Fields(expression), "") == "always()"
	return rest != strings.TrimSpace(condition), always
}

// carriesRenovateBranchSkip reports whether a job condition leads with renovateBranchSkip as
// withoutRenovateBranchSkip strips it, so the job is skipped on every Renovate pull request.
func carriesRenovateBranchSkip(condition string) bool {
	return withoutRenovateBranchSkip(condition) != strings.TrimSpace(condition)
}

// failsWhenNeedFails reports whether steps fail when need failed, and when it was cancelled, while
// every other need succeeded (aggregateFails). Both step kinds fail on a need whatever the others
// reported: an exit step's condition is a disjunction of per-need terms, and an alls-green step
// judges every need on its own, so the result holds on every run.
func failsWhenNeedFails(steps []gateStep, needs []string, need string) bool {
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

// gateStep is one aggregate step in an allowed shape, with the condition it runs under: an exit
// step (exitStep), or an alls-green step (allsGreenStep) and its policy.
type gateStep struct {
	condition string
	allsGreen bool
	policy    allsGreenPolicy
}

// gateSteps returns the steps of job as gateSteps, or false when job has no step or has one in
// neither allowed shape. Any other step is refused rather than taken to pass: a step can change
// what a later one runs, through $GITHUB_ENV, $GITHUB_PATH or a file it writes.
func gateSteps(spec *workflowSpec, job *workflowJob) ([]gateStep, bool) {
	if len(job.Steps) == 0 || len(job.Steps) > ghworkflow.MaxStepsPerJob {
		return nil, false
	}
	steps := make([]gateStep, 0, len(job.Steps))
	for i := 0; i < len(job.Steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		step := &job.Steps[i]
		policy, allsGreen := allsGreenStep(step)
		if !allsGreen && !exitStep(spec, job, step) {
			return nil, false
		}
		steps = append(steps, gateStep{condition: step.If, allsGreen: allsGreen, policy: policy})
	}
	return steps, true
}

// aggregateFails reports whether an aggregate's steps fail the job when its needs report
// results, and whether the file can tell: the first step that fails it decides, and a step whose
// condition cannot be read makes the outcome unknown.
func aggregateFails(steps []gateStep, results map[string]string) (fails, known bool) {
	for i := 0; i < len(steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		if fails, known = steps[i].fails(results); fails || !known {
			return fails, known
		}
	}
	return false, true
}

// fails reports whether the step fails its job when the needs report results, and whether the
// file can tell. The step runs when its condition holds (needsConditionHolds); an exit step then
// fails, and an alls-green step fails when a need reported a result its policy rejects.
func (s gateStep) fails(results map[string]string) (fails, known bool) {
	holds, read := needsConditionHolds(s.condition, results)
	if !holds || !read {
		return false, read
	}
	return !s.allsGreen || s.policy.rejects(results), true
}

// exitStep reports whether step is an allowed exit step: a run: script that ends in a failing
// exit (gateScript), under a shell that ends the step with that status (gateShell), with no env:
// and no continue-on-error.
func exitStep(spec *workflowSpec, job *workflowJob, step *workflowStep) bool {
	return step.Uses == "" && !advisoryJob(step.ContinueOnError) && step.Env.Kind == 0 &&
		gateShell(spec, job, step) && gateScript(step.Run)
}

// gateShells are the shell: keywords an exit step may name. GitHub runs each from a fixed
// template, and an exit command ends the step with its status under every one of them (workflow
// syntax, jobs.<job_id>.steps[*].shell, read 2026-10-07).
var gateShells = []string{"bash", "sh", "pwsh", "powershell", "cmd"}

// gateShell reports whether step runs under the runner's default shell, which is bash, sh or
// pwsh, or under one of gateShells, by the precedence GitHub applies (ghworkflow.StepShellValue).
// A custom template is refused, `bash {0}` included: the template decides what becomes of the
// script's status, and `sh -c true {0}` discards it. An expression is refused too.
func gateShell(spec *workflowSpec, job *workflowJob, step *workflowStep) bool {
	value := ghworkflow.StepShellValue(spec, job, step)
	return value == "" || slices.Contains(gateShells, value)
}

// maxScriptLines bounds the lines gateScript reads from one run: script (HISS-02).
const maxScriptLines = 64

// The kinds of line gateLine tells apart in an exit step's script.
const (
	scriptLineRefused = iota
	scriptLineSilent
	scriptLineMessage
	scriptLineExit
)

// gateScript reports whether script is an allowed exit script: message lines (gateMessage), then
// a failing exit (failingExit), then nothing but blank and comment lines, in at most
// maxScriptLines lines. Every line is plain (plainScriptLine), so none continues onto the next,
// opens a here-document, expands a variable or a command, or joins a second command, and the exit
// is the last command bash, sh, pwsh, powershell and cmd run. Every other script is refused,
// such as one that sets a trap, defines a function or runs exec before its exit.
func gateScript(script string) bool {
	lines := strings.Split(script, "\n")
	if len(lines) > maxScriptLines {
		return false
	}
	exited := false
	for i := 0; i < len(lines) && i < maxScriptLines; i++ {
		kind := gateLine(strings.TrimSpace(lines[i]))
		if kind == scriptLineRefused || (exited && kind != scriptLineSilent) {
			return false
		}
		exited = exited || kind == scriptLineExit
	}
	return exited
}

// gateLine returns the kind of one trimmed script line: refused unless plain, silent when blank
// or a comment, the exit when failingExit reads it, a message when gateMessage does, and refused
// otherwise.
func gateLine(line string) int {
	switch {
	case !plainScriptLine(line):
		return scriptLineRefused
	case line == "" || strings.HasPrefix(line, "#"):
		return scriptLineSilent
	case failingExit(line):
		return scriptLineExit
	case gateMessage(line):
		return scriptLineMessage
	}
	return scriptLineRefused
}

// gateMessages are the commands an exit script may run before its exit. Each prints its
// arguments; none can end the script or change what the exit does.
var gateMessages = []string{"echo", "printf", "Write-Host", "Write-Output"}

// gateMessage reports whether line runs one of gateMessages.
func gateMessage(line string) bool {
	fields := strings.Fields(line)
	return len(fields) > 0 && slices.Contains(gateMessages, fields[0])
}

// failingExit reports whether command is exit with a status from 1 to 255, written as plain
// decimal digits, and nothing after it. Such an exit ends a bash, sh, pwsh, powershell or cmd
// step with that status.
func failingExit(command string) bool {
	fields := strings.Fields(command)
	if len(fields) != 2 || fields[0] != "exit" {
		return false
	}
	status, err := strconv.Atoi(fields[1])
	return err == nil && status >= 1 && status <= 255 && strconv.Itoa(status) == fields[1]
}

// scriptPunctuation is the punctuation a plain script line may hold beside ASCII letters,
// digits, spaces and tabs. It leaves out every character bash, sh, pwsh or cmd reads as a line
// continuation (\ ` ^), a redirection or here-document (< >), an expansion ($ % !), a command
// separator or group (; & | ( ) { } [ ]), a glob (* ?) or a here-string or splat (@).
const scriptPunctuation = `.,:_'"=/+-#`

// plainScriptLine reports whether line holds only ASCII letters, digits, spaces, tabs and
// scriptPunctuation, and closes every quote it opens (quotesClose).
func plainScriptLine(line string) bool {
	for _, r := range line {
		plain := (r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r))) ||
			r == ' ' || r == '\t' || strings.ContainsRune(scriptPunctuation, r)
		if !plain {
			return false
		}
	}
	return quotesClose(line)
}

// quotesClose reports whether every quote line opens is closed on the line, reading ' and " as
// bash, sh and pwsh do: either is literal inside the other. A quote left open would carry the
// next line, the exit included, into a string.
func quotesClose(line string) bool {
	var open rune
	for _, r := range line {
		switch {
		case open == 0 && (r == '\'' || r == '"'):
			open = r
		case r == open:
			open = 0
		}
	}
	return open == 0
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
