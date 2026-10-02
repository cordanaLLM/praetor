// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// WorkflowRunnerLabels lists the runner labels a workflow document's jobs run on, each once,
// job by job in job ID order: a runs-on string, every string of a runs-on list, and every
// label under a runs-on mapping's labels key (ghworkflow.RunnerLabels). A value holding an
// expression (${{ ... }}) is resolved only when the workflow runs, so it names no label here;
// nor does a runner group. The document is read through the parser every workflow audit here
// uses (ghworkflow.Parse), and one with more jobs than the package bounds (maxJobsPerFile) is
// refused rather than read in part.
func WorkflowRunnerLabels(data []byte) ([]string, error) {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return nil, err
	}
	ids := sortedJobIDs(spec.Jobs)
	var labels []string
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		runsOn := spec.Jobs[ids[i]].RunsOn
		for _, label := range ghworkflow.RunnerLabels(&runsOn) {
			if !slices.Contains(labels, label) {
				labels = append(labels, label)
			}
		}
	}
	return labels, nil
}
