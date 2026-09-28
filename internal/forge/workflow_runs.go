// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowRun is one `run:` step of a workflow.
type WorkflowRun struct {
	// Label names the step: its command when the script is one line, else the step's name, else
	// the first line of its script.
	Label string
	// Step reports that Label names a multi-line script rather than being the command itself, so
	// a reader never takes a step's name for a command it could run.
	Step bool
	// Script is the step's whole `run:` script, trimmed, for a caller that must read every
	// command it runs rather than the one line Label shows.
	Script string
}

// WorkflowRuns lists what a workflow document runs, job by job in job ID order and step by
// step in file order: the command of a one-line `run:` step, and the name of a step whose script
// spans several lines (a toolchain install or a shell branch reads better by its name; an
// unnamed one by its first line). A `uses:` step runs an action rather than a command and is not
// listed. The document is read through the parser every workflow audit here uses
// (workflowSpec), so a claim about what a workflow runs is taken from the file itself rather
// than restated beside it. A document with more jobs or steps than the package bounds
// (maxJobsPerFile, maxStepsPerJob) is refused rather than read in part.
func WorkflowRuns(data []byte) ([]WorkflowRun, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	ids := sortedJobIDs(spec.Jobs)
	var runs []WorkflowRun
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		steps := spec.Jobs[ids[i]].Steps
		if len(steps) > maxStepsPerJob {
			return nil, fmt.Errorf("workflow job %s exceeds %d steps", ids[i], maxStepsPerJob)
		}
		for j := 0; j < len(steps) && j < maxStepsPerJob; j++ {
			if run, ok := stepRun(steps[j]); ok {
				runs = append(runs, run)
			}
		}
	}
	return runs, nil
}

// stepRun names what one step runs: its one-line command, else its name, else the first line of
// its script. A step without `run:` runs no command and reports false.
func stepRun(step workflowStep) (WorkflowRun, bool) {
	script := strings.TrimSpace(step.Run)
	if script == "" {
		return WorkflowRun{}, false
	}
	first, _, multiline := strings.Cut(script, "\n")
	if !multiline {
		return WorkflowRun{Label: script, Script: script}, true
	}
	label := strings.TrimSpace(step.Name)
	if label == "" {
		label = strings.TrimSpace(first)
	}
	return WorkflowRun{Label: label, Step: true, Script: script}, true
}
