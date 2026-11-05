// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"os"
)

// CIRunnerEnv lists the GitHub Actions runner variables that change what engine code under
// test does, so a test binary must not inherit them from the CI job that runs it:
//   - GITHUB_WORKFLOW_REF: forge.AuditWorkflowRunHealth treats the running workflow file as
//     WorkflowSelf; in CI that is ci.yml, the name many fixtures use, so a fixture's failing
//     run read as healthy (cordanaLLM/praetor PR 1739 landing, 2026-11-01).
//   - GITHUB_HEAD_REF: release.ResolveReleaseContext reads a release-please branch as release
//     context.
//   - GITHUB_REPOSITORY: the forge drivers and the forge text owner default to it.
//
// Tests that need one set it with t.Setenv.
var CIRunnerEnv = []string{"GITHUB_WORKFLOW_REF", "GITHUB_HEAD_REF", "GITHUB_REPOSITORY"}

// UnsetCIRunnerEnv removes every CIRunnerEnv variable from the process environment; call it
// from a package's TestMain before m.Run.
func UnsetCIRunnerEnv() error {
	for i := 0; i < len(CIRunnerEnv); i++ {
		if err := os.Unsetenv(CIRunnerEnv[i]); err != nil {
			return fmt.Errorf("unset %s: %w", CIRunnerEnv[i], err)
		}
	}
	return nil
}
