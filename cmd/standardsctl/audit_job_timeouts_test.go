// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
)

// The CLI audit runs the job-timeout gate: a workflow that sets no timeout-minutes emits a warning
// and passes the audit (warn-only rollout).
func TestAuditJobTimeouts_CLI_WarnOnlyPasses(t *testing.T) {
	f := newAuditFixture(t)
	writeFixtureFile(t, f.dir, ".github/workflows/job_missing.yml",
		"on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 1\n")
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	if !strings.Contains(out, "[WARN] job-timeout: .github/workflows/job_missing.yml:3 job build: declares no timeout-minutes") {
		t.Fatalf("audit output missing warning:\n%s", out)
	}
	if !strings.Contains(out, "[PASS] job-timeout") {
		t.Fatalf("audit output missing gate pass:\n%s", out)
	}
}
