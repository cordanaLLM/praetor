// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// WorkflowRuns lists what a workflow document runs, job by job in job ID order and step by
// step in file order: the command of a one-line `run:` step, and the name of a step whose script
// spans several lines (a toolchain install or a shell branch reads better by its name; an
// unnamed one by its first line). A `uses:` step runs an action rather than a command and is not
// listed. The document is read through the parser every workflow audit here uses
// (workflowSpec), so a claim about what a workflow runs is taken from the file itself rather
// than restated beside it. A document with more jobs or steps than the package bounds
// (maxJobsPerFile, maxStepsPerJob) is refused rather than read in part.
func WorkflowRuns(data []byte) ([]string, error) {
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return nil, fmt.Errorf("parse workflow: %w", err)
	}
	if len(spec.Jobs) > maxJobsPerFile {
		return nil, fmt.Errorf("workflow exceeds %d jobs", maxJobsPerFile)
	}
	ids := sortedJobIDs(spec.Jobs)
	var runs []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		steps := spec.Jobs[ids[i]].Steps
		if len(steps) > maxStepsPerJob {
			return nil, fmt.Errorf("workflow job %s exceeds %d steps", ids[i], maxStepsPerJob)
		}
		for j := 0; j < len(steps) && j < maxStepsPerJob; j++ {
			if run := stepRun(steps[j]); run != "" {
				runs = append(runs, run)
			}
		}
	}
	return runs, nil
}

// stepRun names what one step runs: its one-line command, else its name, else the first line of
// its script. A step without `run:` runs no command and yields "".
func stepRun(step workflowStep) string {
	script := strings.TrimSpace(step.Run)
	if script == "" {
		return ""
	}
	first, _, multiline := strings.Cut(script, "\n")
	if !multiline {
		return script
	}
	if name := strings.TrimSpace(step.Name); name != "" {
		return name
	}
	return strings.TrimSpace(first)
}
