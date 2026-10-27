// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const (
	// maxRunScriptLines bounds the lines read from one run: body (HISS-02).
	maxRunScriptLines = 512
)

// guardEvaluator evaluates whether a condition guards the step to default branch, tags or schedule.
// Placed in a package variable for testing the mutant where guard detection is always false
// (engine-hiss18-full-tree-on-pr-20261027).
var guardEvaluator = isGuardedCondition

var scheduleSubstrings = []string{
	"github.event_name == 'schedule'",
	"github.event_name=='schedule'",
	"'schedule' == github.event_name",
	"'schedule'==github.event_name",
}

var tagSubstrings = []string{
	"github.ref_type == 'tag'",
	"github.ref_type=='tag'",
	"github.ref_type == 'tags'",
	"github.ref_type=='tags'",
	"'tag' == github.ref_type",
	"'tags' == github.ref_type",
}

var tagPrefixSubstrings = []string{
	"startswith(github.ref, 'refs/tags/')",
	"startswith(github.ref,'refs/tags/')",
	"startswith(github.ref, 'refs/tags')",
	"startswith(github.ref,'refs/tags')",
}

var defaultBranchSubstrings = []string{
	"github.event.repository.default_branch",
	"github.ref == 'refs/heads/main'",
	"github.ref=='refs/heads/main'",
	"'refs/heads/main' == github.ref",
	"'refs/heads/main'==github.ref",
	"github.ref == 'refs/heads/master'",
	"github.ref=='refs/heads/master'",
	"'refs/heads/master' == github.ref",
	"'refs/heads/master'==github.ref",
	"github.ref_name == 'main'",
	"github.ref_name=='main'",
	"'main' == github.ref_name",
	"'main'==github.ref_name",
	"github.ref_name == 'master'",
	"github.ref_name=='master'",
	"'master' == github.ref_name",
	"'master'==github.ref_name",
}

func containsAny(s string, targets []string) bool {
	for i := 0; i < len(targets) && i < maxJobsPerFile; i++ {
		if strings.Contains(s, targets[i]) {
			return true
		}
	}
	return false
}

// isGuardedCondition reports whether a job or step condition references the default branch,
// tags, or schedule (engine-hiss18-full-tree-on-pr-20261027).
func isGuardedCondition(condition string) bool {
	cond := strings.TrimSpace(condition)
	if cond == "" {
		return false
	}
	c := strings.ReplaceAll(cond, "\"", "'")
	if containsAny(c, scheduleSubstrings) || containsAny(c, tagSubstrings) {
		return true
	}
	return containsAny(strings.ToLower(c), tagPrefixSubstrings) || containsAny(c, defaultBranchSubstrings)
}

// pushReachesNonDefaultBranches reports whether a push trigger reaches non-default branches:
// no branches filter, branches-ignore, wildcard, or branch names beyond main/master
// (engine-hiss18-full-tree-on-pr-20261027).
func pushReachesNonDefaultBranches(value *yaml.Node) bool {
	if value == nil || value.Kind != yaml.MappingNode {
		return true
	}
	if branches := util.YAMLMappingValue(value, "branches"); branches != nil {
		return branchesReachNonDefault(ghworkflow.StringList(branches))
	}
	if util.YAMLMappingValue(value, "branches-ignore") != nil {
		return true
	}
	hasTags := util.YAMLMappingValue(value, "tags") != nil || util.YAMLMappingValue(value, "tags-ignore") != nil
	return !hasTags
}

func branchesReachNonDefault(list []string) bool {
	if len(list) == 0 {
		return true
	}
	for i := 0; i < len(list) && i < maxJobsPerFile; i++ {
		b := list[i]
		if strings.Contains(b, "*") || (b != "main" && b != "master") {
			return true
		}
	}
	return false
}

// fullTreeFindings reports steps running full-tree commands in pull request or branch push
// workflows without an exempting guard condition (engine-hiss18-full-tree-on-pr-20261027).
func fullTreeFindings(w *triggerWorkflow) []workflowTriggerFinding {
	hasPR := hasWorkflowTrigger(&w.spec.On, pullRequestEvent) || hasWorkflowTrigger(&w.spec.On, pullRequestTargetEvent)
	push, hasPush := declaredTrigger(&w.spec.On, pushEvent)
	hasPushNonDefault := hasPush && pushReachesNonDefaultBranches(push.value)

	if !hasPR && !hasPushNonDefault {
		return nil
	}

	var findings []workflowTriggerFinding
	ids := sortedJobIDs(w.spec.Jobs)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		jobID := ids[i]
		job := w.spec.Jobs[jobID]
		jobFindings := evaluateJobFullTree(w, jobID, &job, hasPR, hasPushNonDefault)
		findings = append(findings, jobFindings...)
	}
	return findings
}

func hasWorkflowTrigger(on *yaml.Node, event string) bool {
	_, declared := declaredTrigger(on, event)
	return declared
}

func evaluateJobFullTree(w *triggerWorkflow, jobID string, job *workflowJob, hasPR, hasPushNonDefault bool) []workflowTriggerFinding {
	if guardEvaluator(job.If) {
		return nil
	}
	trigger := resolveJobTrigger(job.If, hasPR, hasPushNonDefault)
	if trigger == "" {
		return nil
	}
	var findings []workflowTriggerFinding
	for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
		step := job.Steps[s]
		if guardEvaluator(step.If) || strings.TrimSpace(step.Run) == "" {
			continue
		}
		if cmd, found := findFullTreeStepCommand(step.Run); found {
			findings = append(findings, buildFullTreeFinding(w, jobID, step, s, trigger, cmd))
		}
	}
	return findings
}

func resolveJobTrigger(jobIf string, hasPR, hasPushNonDefault bool) string {
	if hasPR {
		if len(reachingEvents(jobIf, []string{pullRequestEvent})) > 0 {
			return pullRequestEvent
		}
		if hasPushNonDefault {
			return pushEvent
		}
		return ""
	}
	if hasPushNonDefault {
		return pushEvent
	}
	return ""
}

func buildFullTreeFinding(w *triggerWorkflow, jobID string, step workflowStep, stepIdx int, trigger, cmd string) workflowTriggerFinding {
	line := step.RunLine
	if line == 0 {
		line = w.jobLines[jobID]
	}
	contextText := "every pull request"
	if trigger == pushEvent {
		contextText = "every branch push"
	}
	stepLabel := formatStepLabel(step, stepIdx)
	problem := fmt.Sprintf("step %s runs full-tree command %q on %s: scope to changed files (pre-commit run --from-ref origin/main --to-ref HEAD) or guard to default branch, tags or schedule", stepLabel, cmd, contextText)

	return workflowTriggerFinding{
		Workflow: w.path,
		Line:     line,
		Trigger:  trigger,
		Job:      jobID,
		Problem:  problem,
	}
}

// findFullTreeStepCommand searches the script line-by-line for full-tree commands
// (engine-hiss18-full-tree-on-pr-20261027).
func findFullTreeStepCommand(script string) (string, bool) {
	lines := strings.Split(script, "\n")
	for i := 0; i < len(lines) && i < maxRunScriptLines; i++ {
		if cmd, found := findFullTreeCommandInLine(lines[i]); found {
			return cmd, true
		}
	}
	return "", false
}

// findFullTreeCommandInLine inspects a single line for full-tree command invocations
// (engine-hiss18-full-tree-on-pr-20261027).
func findFullTreeCommandInLine(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	fields, err := ShellFields(trimmed)
	if err == nil {
		commands := scriptCommands(fields)
		for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
			words := commands[i]
			cmd := parseShellCommand(words)
			if matched, ok := isFullTreeCommand(cmd, words); ok {
				return matched, true
			}
		}
		return "", false
	}
	return fullTreeFallbackMatch(trimmed)
}

// isFullTreeCommand checks if a parsed shell command matches the full-tree patterns
// (engine-hiss18-full-tree-on-pr-20261027).
func isFullTreeCommand(cmd shellCommand, words []string) (string, bool) {
	if isPrintOrExport(cmd.program) {
		return "", false
	}
	if hasAllFilesFlag(cmd.args) || isPreCommitAll(cmd.program, cmd.args) || isMoleculeAll(cmd.program, cmd.args) {
		return strings.Join(words, " "), true
	}
	return "", false
}

func isPrintOrExport(prog string) bool {
	return prog == "" || prog == "echo" || prog == "printf" || prog == "export"
}

func hasAllFilesFlag(args []string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if args[i] == "--all-files" || strings.HasPrefix(args[i], "--all-files=") {
			return true
		}
	}
	return false
}

func isPreCommitAll(prog string, args []string) bool {
	return (prog == "pre-commit" || prog == "prek") && slices.Contains(args, "-a")
}

func isMoleculeAll(prog string, args []string) bool {
	matchesMolecule := prog == "molecule" || slices.Contains(args, "molecule")
	return matchesMolecule && (slices.Contains(args, "--all") || hasPrefixInArgs(args, "--all="))
}

func hasPrefixInArgs(args []string, prefix string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		if strings.HasPrefix(args[i], prefix) {
			return true
		}
	}
	return false
}

// fullTreeFallbackMatch provides conservative token matching on unquoted words when ShellFields
// cannot parse a line (engine-hiss18-full-tree-on-pr-20261027).
func fullTreeFallbackMatch(line string) (string, bool) {
	unquoted := stripCommentsAndQuotes(line)
	fields := strings.Fields(unquoted)
	if len(fields) == 0 || fields[0] == "echo" || fields[0] == "printf" {
		return "", false
	}
	if hasAllFilesFlag(fields) || isPreCommitAllFields(fields) || isMoleculeAllFields(fields) {
		return strings.TrimSpace(line), true
	}
	return "", false
}

func isPreCommitAllFields(fields []string) bool {
	hasTool := slices.Contains(fields, "pre-commit") || slices.Contains(fields, "prek")
	return hasTool && slices.Contains(fields, "-a")
}

func isMoleculeAllFields(fields []string) bool {
	return slices.Contains(fields, "molecule") && (slices.Contains(fields, "--all") || hasPrefixInArgs(fields, "--all="))
}

// stripCommentsAndQuotes strips comments and quoted text for conservative token matching
// (engine-hiss18-full-tree-on-pr-20261027).
func stripCommentsAndQuotes(line string) string {
	stripped, err := util.StripHashComments(line)
	if err != nil {
		stripped = line
	}
	var b strings.Builder
	var stepper hiss.QuoteStepper
	for i := 0; i < len(stripped) && i < maxRunScriptFields; i++ {
		stepper.Step(stripped, i)
		if !stepper.InQuote() && stripped[i] != '\'' && stripped[i] != '"' {
			b.WriteByte(stripped[i])
		}
	}
	return b.String()
}
