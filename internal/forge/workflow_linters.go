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

// maxLinterCandidates bounds the linters checked by one measurement (HISS-02).
const maxLinterCandidates = 64

// MeasureWorkflowLinters inspects every workflow document directly under .github/workflows in
// repoPath and reports which candidate linters are run by at least one runnable step. A step
// counts as running a linter when its run: or uses: names the tool (pinned binary, action, or a
// script whose name contains the tool name).
func MeasureWorkflowLinters(ctx context.Context, repoPath string, candidates []string) (map[string]bool, error) {
	if ctx == nil {
		return nil, errors.New("measure workflow linters requires a context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return make(map[string]bool), nil
	}
	if len(candidates) > maxLinterCandidates {
		return nil, fmt.Errorf("linter candidates count exceeds %d", maxLinterCandidates)
	}

	files, err := readWorkflowFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}

	present := make(map[string]bool, len(candidates))
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		spec, err := ghworkflow.Parse(files[i].Data)
		if err != nil {
			return nil, fmt.Errorf("workflow %s: %w", files[i].Name, err)
		}
		for _, job := range spec.Jobs {
			if ghworkflow.NeverRuns(job.If) {
				continue
			}
			for s := 0; s < len(job.Steps) && s < ghworkflow.MaxStepsPerJob; s++ {
				step := job.Steps[s]
				if ghworkflow.NeverRuns(step.If) {
					continue
				}
				for c := 0; c < len(candidates) && c < maxLinterCandidates; c++ {
					tool := candidates[c]
					if !present[tool] && stepRunsTool(step, tool) {
						present[tool] = true
					}
				}
			}
		}
	}
	return present, nil
}

// stepRunsTool reports whether step's run: or uses: names the tool.
func stepRunsTool(step ghworkflow.Step, tool string) bool {
	toolLower := strings.ToLower(tool)
	if strings.Contains(strings.ToLower(step.Uses), toolLower) {
		return true
	}
	runLower := strings.ToLower(step.Run)
	if strings.Contains(runLower, toolLower) {
		return true
	}
	if toolLower == "govet" && strings.Contains(runLower, "go vet") {
		return true
	}
	return false
}
