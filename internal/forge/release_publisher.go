// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// MeasureReleasePublishers lists workflows that create, upload or publish GitHub releases.
// It shares the bounded workflow and shell readers used by the other release measurements.
func MeasureReleasePublishers(ctx context.Context, repoPath string) ([]string, error) {
	if ctx == nil {
		return nil, errors.New("release publisher measurement requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("read release publishers: %w", err)
	}
	var publishers []string
	for i := 0; i < len(files) && i < ghworkflow.MaxFiles; i++ {
		publishes, err := workflowPublishesRelease(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s/%s: %w", ghworkflow.Dir, files[i].Name, err)
		}
		if publishes {
			publishers = append(publishers, ghworkflow.Dir+"/"+files[i].Name)
		}
	}
	return publishers, nil
}

// workflowPublishesRelease checks executable steps rather than names, comments or printed text.
func workflowPublishesRelease(data []byte) (bool, error) {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return false, fmt.Errorf("read release publisher: %w", err)
	}
	ids := ghworkflow.SortedJobIDs(spec.Jobs)
	found := false
	for i := 0; i < len(ids) && i < ghworkflow.MaxJobsPerFile; i++ {
		publishes, err := jobPublishesRelease(spec.Jobs[ids[i]])
		if err != nil {
			return false, fmt.Errorf("job %s: %w", ids[i], err)
		}
		found = found || publishes
	}
	return found, nil
}

// jobPublishesRelease refuses oversized jobs and ignores steps that can never execute.
func jobPublishesRelease(job ghworkflow.Job) (bool, error) {
	if len(job.Steps) > ghworkflow.MaxStepsPerJob {
		return false, fmt.Errorf("release publisher job exceeds %d steps", ghworkflow.MaxStepsPerJob)
	}
	if ghworkflow.NeverRuns(job.If) {
		return false, nil
	}
	for i := 0; i < len(job.Steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		step := job.Steps[i]
		if ghworkflow.NeverRuns(step.If) {
			continue
		}
		publishes, err := actionPublishesRelease(step)
		if err != nil {
			return false, fmt.Errorf("step %d: %w", i+1, err)
		}
		if publishes {
			return true, nil
		}
		publishes, err = scriptPublishesRelease(step.Run)
		if err != nil {
			return false, fmt.Errorf("step %d: %w", i+1, err)
		}
		if publishes {
			return true, nil
		}
	}
	return false, nil
}
