// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package release

import (
	"fmt"
	"os"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// TestMain keeps the CI runner's GitHub Actions variables (testsupport.CIRunnerEnv) away from the
// package's tests: with GITHUB_WORKFLOW_REF naming ci.yml, a fixture workflow of that name is read
// as the running workflow and its live run health is never asked (PR 1739 landing, 2026-11-01).
func TestMain(m *testing.M) {
	if err := testsupport.UnsetCIRunnerEnv(); err != nil {
		fmt.Fprintln(os.Stderr, "isolate CI runner environment:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}
