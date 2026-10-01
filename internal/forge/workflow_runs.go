// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
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
	// Name is the step's own `name:`, trimmed, or empty for an unnamed step. A one-line step's
	// Label is its command, so a caller that finds a step by name reads it here.
	Name string
	// Job is the ID of the job the step belongs to, and JobName that job's `name:`, the status
	// context a job without a matrix reports under.
	Job     string
	JobName string
	// Index is the step's position among all steps of its job, from 0, `uses:` steps counted,
	// so two runs are adjacent steps exactly when they share Job and their Index differs by one.
	Index int
}

// WorkflowRuns lists what a workflow document runs, job by job in job ID order and step by
// step in file order (ghworkflow.RunSteps): the command of a one-line `run:` step, and the name of
// a step whose script spans several lines (a toolchain install or a shell branch reads better by
// its name; an unnamed one by its first line), each with the job it runs in and its place there.
// A `uses:` step runs an action rather than a command and is not listed. The document is read
// through the parser every workflow audit here uses (ghworkflow.Parse), so a claim about what a
// workflow runs is taken from the file itself rather than restated beside it. A document with
// more jobs or steps than the package bounds (maxJobsPerFile, maxStepsPerJob) is refused rather
// than read in part.
func WorkflowRuns(data []byte) ([]WorkflowRun, error) {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return nil, err
	}
	steps, err := ghworkflow.RunSteps(&spec)
	if err != nil {
		return nil, err
	}
	runs := make([]WorkflowRun, 0, len(steps))
	for _, step := range steps {
		if run, ok := stepRun(*step.Step); ok {
			run.Job, run.JobName, run.Index = step.JobID, strings.TrimSpace(step.Job.Name), step.Index
			runs = append(runs, run)
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
	name := strings.TrimSpace(step.Name)
	first, _, multiline := strings.Cut(script, "\n")
	if !multiline {
		return WorkflowRun{Label: script, Script: script, Name: name}, true
	}
	label := name
	if label == "" {
		label = strings.TrimSpace(first)
	}
	return WorkflowRun{Label: label, Step: true, Script: script, Name: name}, true
}
