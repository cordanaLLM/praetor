// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"go.yaml.in/yaml/v3"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxReuseWorkflowBytes bounds one workflow file read for an existing REUSE lint job (HISS-02).
const maxReuseWorkflowBytes = 4 << 20

// isReuseLintJob reports whether id or job represents a REUSE lint job:
// named REUSE lint, REUSE, or running reuse lint / fsfe/reuse-action (#1327).
func isReuseLintJob(id string, job ghworkflow.Job) bool {
	if isReuseJobName(id, job.Name) {
		return true
	}
	for i := 0; i < len(job.Steps) && i < ghworkflow.MaxStepsPerJob; i++ {
		if isReuseStep(job.Steps[i]) {
			return true
		}
	}
	return false
}

// isReuseJobName reports whether id or name represents a REUSE lint job identifier.
func isReuseJobName(id, name string) bool {
	if strings.EqualFold(id, "reuse") || strings.EqualFold(id, "reuse-lint") || strings.EqualFold(id, "reuse_lint") {
		return true
	}
	return strings.EqualFold(name, "REUSE lint") || strings.EqualFold(name, "REUSE") || strings.EqualFold(name, "reuse-lint")
}

// isReuseStep reports whether step runs reuse lint or uses fsfe/reuse-action.
func isReuseStep(step ghworkflow.Step) bool {
	if strings.Contains(step.Run, "reuse lint") {
		return true
	}
	return strings.HasPrefix(strings.TrimSpace(step.Uses), "fsfe/reuse-action")
}

// workflowDeclaresReuseJob reports whether the workflow content data declares a REUSE lint job.
func workflowDeclaresReuseJob(data []byte) (bool, error) {
	var spec ghworkflow.Spec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		return false, fmt.Errorf("unmarshal workflow: %w", err)
	}
	if len(spec.Jobs) > ghworkflow.MaxJobsPerFile {
		return false, fmt.Errorf("workflow exceeds %d jobs", ghworkflow.MaxJobsPerFile)
	}
	for id, job := range spec.Jobs {
		if isReuseLintJob(id, job) {
			return true, nil
		}
	}
	return false, nil
}

func isCandidateWorkflowEntry(entry os.DirEntry) bool {
	if entry.IsDir() {
		return false
	}
	name := entry.Name()
	if name == filepath.Base(reuseWorkflowFile) {
		return false
	}
	return strings.HasSuffix(name, ".yml") || strings.HasSuffix(name, ".yaml")
}

func workflowFileDeclaresReuseJob(workflowDir string, entry os.DirEntry) (bool, error) {
	if !isCandidateWorkflowEntry(entry) {
		return false, nil
	}
	path := filepath.Join(workflowDir, entry.Name())
	data, err := util.ReadFileLimited(path, maxReuseWorkflowBytes)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	return workflowDeclaresReuseJob(data)
}

// adopterDeclaresReuseJob reports whether repoPath holds an existing workflow with a job that
// runs reuse lint, uses fsfe/reuse-action, or is named REUSE lint, excluding Praetor's own
// unedited reuseWorkflowFile (#1327).
func adopterDeclaresReuseJob(ctx context.Context, repoPath string) (bool, error) {
	if ctx == nil {
		return false, fmt.Errorf("context required")
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}
	workflowDir := filepath.Join(repoPath, filepath.FromSlash(ghworkflow.Dir))
	entries, err := os.ReadDir(workflowDir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read %s: %w", ghworkflow.Dir, err)
	}
	for i := 0; i < len(entries) && i < ghworkflow.MaxFiles; i++ {
		declares, err := workflowFileDeclaresReuseJob(workflowDir, entries[i])
		if err != nil {
			return false, err
		}
		if declares {
			return true, nil
		}
	}
	return false, nil
}
