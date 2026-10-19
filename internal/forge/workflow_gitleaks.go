// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// FaultyGitleaksStep records a workflow step running a gitleaks history scan without -m.
type FaultyGitleaksStep struct {
	WorkflowPath string
	StepName     string
	StepIndex    int
}

// FindGitleaksHistoryScanWithoutMinusM inspects every workflow document directly under
// .github/workflows in repoPath and reports the first step running a gitleaks history
// scan (gitleaks detect without --no-git) whose log options lack -m (#1237, #1376).
func FindGitleaksHistoryScanWithoutMinusM(ctx context.Context, repoPath string) (*FaultyGitleaksStep, error) {
	if ctx == nil {
		return nil, fmt.Errorf("context is required")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		relPath := filepath.ToSlash(filepath.Join(".github", "workflows", files[i].Name))
		if fault := scanWorkflowGitleaksFault(spec, relPath); fault != nil {
			return fault, nil
		}
	}
	return nil, nil
}

func scanWorkflowGitleaksFault(spec ghworkflow.Spec, relPath string) *FaultyGitleaksStep {
	for _, job := range spec.Jobs {
		if ghworkflow.NeverRuns(job.If) || advisoryJob(job.ContinueOnError) {
			continue
		}
		if fault := scanJobGitleaksFault(job, relPath); fault != nil {
			return fault
		}
	}
	return nil
}

func scanJobGitleaksFault(job ghworkflow.Job, relPath string) *FaultyGitleaksStep {
	for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
		step := job.Steps[s]
		if ghworkflow.NeverRuns(step.If) || advisoryJob(step.ContinueOnError) || step.Run == "" {
			continue
		}
		if stepRunsGitleaksWithoutMinusM(step.Run) {
			return &FaultyGitleaksStep{
				WorkflowPath: relPath,
				StepName:     step.Name,
				StepIndex:    s + 1,
			}
		}
	}
	return nil
}

func stepRunsGitleaksWithoutMinusM(run string) bool {
	fields, err := scriptFields(run)
	if err != nil {
		return false
	}
	commands := scriptCommands(fields)
	for c := 0; c < len(commands) && c < maxRunScriptFields; c++ {
		cmd := parseShellCommand(commands[c])
		if isGitleaksHistoryScanWithoutMinusM(cmd) {
			return true
		}
	}
	return false
}

func isGitleaksHistoryScanWithoutMinusM(cmd shellCommand) bool {
	args, ok := unwrapCommandToGitleaks(cmd)
	if !ok || len(args) == 0 {
		return false
	}
	if !isGitleaksDetect(args) {
		return false
	}
	return !hasMinusMInLogOpts(args)
}

func extractGitleaksDirectArgs(curr shellCommand) ([]string, bool) {
	if isGitleaksProgram(curr.program) {
		return curr.args, true
	}
	if curr.program == "docker" || curr.program == "podman" {
		return containerGitleaksArgs(curr.args)
	}
	if curr.program == "go" {
		return goRunGitleaksArgs(curr.args)
	}
	return nil, false
}

func unwrapCommandToGitleaks(cmd shellCommand) ([]string, bool) {
	curr := cmd
	for w := 0; w < 4; w++ {
		if a, found := extractGitleaksDirectArgs(curr); found {
			return a, true
		}
		words, unwrapped := unwrapWrapper(curr)
		if !unwrapped || len(words) == 0 {
			break
		}
		curr = parseShellCommand(words)
	}
	return nil, false
}

func isGitleaksProgram(prog string) bool {
	return prog == "gitleaks" || strings.HasSuffix(prog, "/gitleaks")
}

// containerDoubleDashArgs extracts remaining arguments after `-- <image>`.
func containerDoubleDashArgs(rest []string, i int) ([]string, bool) {
	if i+1 < len(rest) && containerImageMatches(rest[i+1], "gitleaks") {
		return rest[i+2:], true
	}
	return nil, false
}

// advanceDockerOption advances the index past a Docker option and any separate value argument.
func advanceDockerOption(rest []string, i int) int {
	if !strings.Contains(rest[i], "=") && isDockerValOption(rest[i]) && i+1 < len(rest) {
		return i + 1
	}
	return i
}

func containerGitleaksArgs(args []string) ([]string, bool) {
	rest := argsAfterWord(args, "run")
	for i := 0; i < len(rest) && i < maxRunScriptFields; i++ {
		arg := rest[i]
		if arg == "--" {
			return containerDoubleDashArgs(rest, i)
		}
		if strings.HasPrefix(arg, "-") {
			i = advanceDockerOption(rest, i)
			continue
		}
		if containerImageMatches(arg, "gitleaks") {
			return rest[i+1:], true
		}
		return nil, false
	}
	return nil, false
}

// isGoRunOrTool reports whether the arguments begin with a "run" or "tool" subcommand.
func isGoRunOrTool(args []string) bool {
	return len(args) > 0 && (args[0] == "run" || args[0] == "tool")
}

// advanceGoFlag advances the index past a Go flag and any separate value argument.
func advanceGoFlag(args []string, i int) int {
	if !strings.Contains(args[i], "=") && isGoFlagWithValue(args[i]) && i+1 < len(args) {
		return i + 1
	}
	return i
}

func goRunGitleaksArgs(args []string) ([]string, bool) {
	if !isGoRunOrTool(args) {
		return nil, false
	}
	for i := 1; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			i = advanceGoFlag(args, i)
			continue
		}
		if clean := cleanRunnerSpec(arg); strings.Contains(clean, "gitleaks") {
			return args[i+1:], true
		}
		return nil, false
	}
	return nil, false
}

func isGitleaksDetect(args []string) bool {
	hasDetect := false
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if arg == "--no-git" || arg == "--pipe" {
			return false
		}
		if arg == "detect" {
			hasDetect = true
		}
	}
	return hasDetect
}

// extractLogOptsValue retrieves the value of a --log-opts flag and advances the index if needed.
func extractLogOptsValue(args []string, i int) (string, int) {
	arg := args[i]
	if arg == "--log-opts" && i+1 < len(args) {
		return args[i+1], i + 1
	}
	if strings.HasPrefix(arg, "--log-opts=") {
		return strings.TrimPrefix(arg, "--log-opts="), i
	}
	return "", i
}

// logOptsContainsMinusM reports whether the log options string includes a "-m" flag.
func logOptsContainsMinusM(val string) bool {
	if val == "" {
		return false
	}
	clean := strings.Trim(val, "\"'")
	fields := strings.Fields(clean)
	for f := 0; f < len(fields) && f < maxRunScriptFields; f++ {
		if fields[f] == "-m" {
			return true
		}
	}
	return false
}

func hasMinusMInLogOpts(args []string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		val, nextI := extractLogOptsValue(args, i)
		i = nextI
		if logOptsContainsMinusM(val) {
			return true
		}
	}
	return false
}
