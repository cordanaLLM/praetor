// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// Hook runners a CI step can start, by the program name each one is run as.
const (
	// HookRunnerPreCommit is the pre-commit framework: `pre-commit run`.
	HookRunnerPreCommit = "pre-commit"
	// HookRunnerLefthook is Lefthook: `lefthook run <hook>`.
	HookRunnerLefthook = "lefthook"
	// preCommitDefaultStage is the stage `pre-commit run` selects without --hook-stage.
	preCommitDefaultStage = "pre-commit"
	// preCommitAction is the action that runs `pre-commit run` with its extra_args input.
	preCommitAction = "pre-commit/action"
	// maxHookRuns bounds the hook-runner steps one measurement records (HISS-02).
	maxHookRuns = 64
	// maxCommandUnwraps bounds how many wrappers (env, xargs, ...) one command is unwrapped
	// through, as directCommandRunsTool does.
	maxCommandUnwraps = 4
)

// HookRun is one CI step that runs the repository's hook runner (#1313). Stage is, for the
// pre-commit framework, the hook stage the run selects, and for Lefthook the hook it runs. Only
// names the hooks (pre-commit IDs) or jobs and commands (Lefthook) the run is limited to; it is
// empty when the run starts every one. A run limited in a way the measurement does not read,
// such as a Lefthook --tag, is not recorded, so no linter is credited to it.
type HookRun struct {
	Runner string
	Stage  string
	Only   []string
}

// MeasureWorkflowHookRuns lists the steps of the repository's workflows that run its hook
// runner, read the way MeasureWorkflowLinters reads steps: a job or step that never runs or
// whose failure is forgiven (continue-on-error) runs nothing that counts.
func MeasureWorkflowHookRuns(ctx context.Context, repoPath string) ([]HookRun, error) {
	if ctx == nil {
		return nil, errors.New("measure workflow hook runs requires a context")
	}
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	var runs []HookRun
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		for _, id := range ghworkflow.SortedJobIDs(spec.Jobs) {
			job := spec.Jobs[id]
			if ghworkflow.NeverRuns(job.If) || advisoryJob(job.ContinueOnError) {
				continue
			}
			runs = append(runs, jobHookRuns(job)...)
			if len(runs) > maxHookRuns {
				return nil, fmt.Errorf("workflows run the hook runner in more than %d steps", maxHookRuns)
			}
		}
	}
	return runs, nil
}

// jobHookRuns returns the hook-runner runs of one job's steps.
func jobHookRuns(job ghworkflow.Job) []HookRun {
	var runs []HookRun
	for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
		step := job.Steps[s]
		if ghworkflow.NeverRuns(step.If) || advisoryJob(step.ContinueOnError) {
			continue
		}
		if run, ok := preCommitActionRun(step); ok {
			runs = append(runs, run)
		}
		if step.Run != "" {
			runs = append(runs, scriptHookRuns(step.Run)...)
		}
	}
	return runs
}

// preCommitActionRun reads a step that uses pre-commit/action, which runs `pre-commit run`
// with its extra_args input (default --all-files).
func preCommitActionRun(step ghworkflow.Step) (HookRun, bool) {
	action, _, _ := strings.Cut(strings.TrimSpace(step.Uses), "@")
	if !strings.EqualFold(action, preCommitAction) {
		return HookRun{}, false
	}
	args := []string{"run"}
	if extra, ok := step.With["extra_args"].(string); ok {
		args = append(args, strings.Fields(extra)...)
	}
	return preCommitRun(args)
}

// scriptHookRuns returns the hook-runner runs one run: script makes, through the same shell
// reading MeasureWorkflowLinters uses (scriptFields, parseShellCommand, unwrapWrapper).
func scriptHookRuns(script string) []HookRun {
	fields, err := scriptFields(script)
	if err != nil {
		return nil
	}
	commands := scriptCommands(fields)
	var runs []HookRun
	for i := 0; i < len(commands) && i < maxRunScriptFields; i++ {
		if run, ok := commandHookRun(parseShellCommand(commands[i])); ok {
			runs = append(runs, run)
		}
	}
	return runs
}

// commandHookRun reads one command, unwrapped through its wrappers, as a hook-runner run.
func commandHookRun(cmd shellCommand) (HookRun, bool) {
	current := cmd
	for unwraps := 0; unwraps < maxCommandUnwraps; unwraps++ {
		switch current.program {
		case HookRunnerPreCommit:
			return preCommitRun(current.args)
		case HookRunnerLefthook:
			return lefthookRun(current.args)
		}
		words, ok := unwrapWrapper(current)
		if !ok || len(words) == 0 {
			return HookRun{}, false
		}
		current = parseShellCommand(words)
	}
	return HookRun{}, false
}

// preCommitRun reads `pre-commit <args>`: only the run subcommand runs hooks; its positional
// arguments are hook IDs, and --hook-stage selects the stage (pre-commit 4.x `run --help`).
func preCommitRun(args []string) (HookRun, bool) {
	if len(args) == 0 || args[0] != "run" {
		return HookRun{}, false
	}
	run := HookRun{Runner: HookRunnerPreCommit, Stage: preCommitDefaultStage}
	for i := 1; i < len(args) && i < maxRunScriptFields; i++ {
		next, credited := run.readPreCommitArg(args, i)
		if !credited {
			return HookRun{}, false
		}
		i = next
	}
	return run, true
}

// readPreCommitArg reads args[i] of `pre-commit run` into run and returns the index of the last
// argument it consumed, or false for a run the audit does not credit: one reading another
// configuration file than the one the audit reads.
func (run *HookRun) readPreCommitArg(args []string, i int) (int, bool) {
	arg := args[i]
	switch {
	case preCommitConfigFlag(arg):
		return i, false
	case strings.HasPrefix(arg, "--hook-stage="):
		run.Stage = strings.TrimPrefix(arg, "--hook-stage=")
	case arg == "--hook-stage" && i+1 < len(args):
		run.Stage = args[i+1]
		return i + 1, true
	case arg == "--files":
		// --files takes every following argument up to the next flag (nargs='*').
		for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
		}
	case preCommitValueFlag(arg):
		return i + 1, true
	case !strings.HasPrefix(arg, "-"):
		run.Only = append(run.Only, arg)
	}
	return i, true
}

// preCommitConfigFlag reports whether arg names the configuration file `pre-commit run` reads.
func preCommitConfigFlag(arg string) bool {
	return arg == "--config" || arg == "-c" || strings.HasPrefix(arg, "--config=")
}

// preCommitValueFlag reports whether a `pre-commit run` flag takes the next argument as its
// value, so that value is not read as a hook ID.
func preCommitValueFlag(arg string) bool {
	switch arg {
	case "--from-ref", "--source", "-s", "--to-ref", "--origin", "-o", "--remote-branch",
		"--local-branch", "--remote-name", "--remote-url", "--commit-msg-filename", "--prepare-commit-message-source",
		"--commit-object-name", "--checkout-type", "--is-squash-merge", "--rewrite-command", "--color":
		return true
	}
	return false
}

// lefthookRun reads `lefthook <args>`: only `run <hook>` runs hooks; --job and --command
// (repeatable, or comma lists in --jobs and --commands) limit it, and a limit by --tag, which
// names no job, is not read (Lefthook 2.1 `run --help`).
func lefthookRun(args []string) (HookRun, bool) {
	if len(args) < 2 || args[0] != "run" || strings.HasPrefix(args[1], "-") {
		return HookRun{}, false
	}
	run := HookRun{Runner: HookRunnerLefthook, Stage: args[1]}
	for i := 2; i < len(args) && i < maxRunScriptFields; i++ {
		if !strings.HasPrefix(args[i], "--") {
			continue
		}
		name, value, next := lefthookFlag(args, i)
		i = next
		switch name {
		case "tag":
			return HookRun{}, false
		case "job", "jobs", "command", "commands":
			if value != "" {
				run.Only = append(run.Only, strings.Split(value, ",")...)
			}
		}
	}
	return run, true
}

// lefthookFlag reads the flag at args[i] as its name and value, written --name=value or, for a
// flag that takes one, --name value, and returns the index of the last argument it consumed.
func lefthookFlag(args []string, i int) (name, value string, last int) {
	name, value, hasValue := strings.Cut(strings.TrimPrefix(args[i], "--"), "=")
	if !hasValue && i+1 < len(args) && lefthookValueFlag(name) {
		return name, args[i+1], i + 1
	}
	return name, value, i
}

// lefthookValueFlag reports whether a `lefthook run` flag takes a value.
func lefthookValueFlag(name string) bool {
	switch name {
	case "job", "jobs", "command", "commands", "tag", "exclude", "file", "colors":
		return true
	}
	return false
}

// CommandRunsTool reports whether a command line, such as a pre-commit hook's entry or a
// Lefthook command's run, runs tool, read the way a workflow step's run: is
// (MeasureWorkflowLinters), without Makefile or package-script lookups.
func CommandRunsTool(ctx context.Context, command, tool string) bool {
	return scriptRunsTool(ctx, command, tool, nil, nil)
}

// NameRunsTool reports whether a hook's ID or a job's name names tool as a dash-separated word,
// as an action's repository name does (stepActionRunsTool): "markdownlint-cli2" names
// markdownlint.
func NameRunsTool(name, tool string) bool {
	return matchesDashWord(strings.ToLower(name), strings.ToLower(tool))
}
