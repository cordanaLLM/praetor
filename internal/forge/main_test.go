// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"os"
	"testing"
)

// TestMain keeps the package's tests independent of the CI runner: GitHub Actions sets
// GITHUB_WORKFLOW_REF, which makes the run-health check skip the named workflow (#1331); a test
// that wants it sets it with t.Setenv.
func TestMain(m *testing.M) {
	if err := os.Unsetenv("GITHUB_WORKFLOW_REF"); err != nil {
		fmt.Fprintln(os.Stderr, "unset GITHUB_WORKFLOW_REF:", err)
		os.Exit(2)
	}
	os.Exit(m.Run())
}
