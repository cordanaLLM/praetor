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

func containerGitleaksArgs(args []string) ([]string, bool) {
	rest := argsAfterWord(args, "run")
	for i := 0; i < len(rest) && i < maxRunScriptFields; i++ {
		arg := rest[i]
		if arg == "--" {
			if i+1 < len(rest) && containerImageMatches(rest[i+1], "gitleaks") {
				return rest[i+2:], true
			}
			return nil, false
		}
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && isDockerValOption(arg) && i+1 < len(rest) {
				i++
			}
			continue
		}
		if containerImageMatches(arg, "gitleaks") {
			return rest[i+1:], true
		}
		return nil, false
	}
	return nil, false
}

func goRunGitleaksArgs(args []string) ([]string, bool) {
	if len(args) == 0 || (args[0] != "run" && args[0] != "tool") {
		return nil, false
	}
	for i := 1; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			if !strings.Contains(arg, "=") && isGoFlagWithValue(arg) && i+1 < len(args) {
				i++
			}
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

func hasMinusMInLogOpts(args []string) bool {
	for i := 0; i < len(args) && i < maxRunScriptFields; i++ {
		arg := args[i]
		var val string
		if arg == "--log-opts" && i+1 < len(args) {
			val = args[i+1]
			i++
		} else if strings.HasPrefix(arg, "--log-opts=") {
			val = strings.TrimPrefix(arg, "--log-opts=")
		}
		if val != "" {
			clean := strings.Trim(val, "\"'")
			fields := strings.Fields(clean)
			for f := 0; f < len(fields) && f < maxRunScriptFields; f++ {
				if fields[f] == "-m" {
					return true
				}
			}
		}
	}
	return false
}
