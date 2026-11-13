// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/forge"
)

// The CLI audit runs the job-timeout gate with the wall clock: a workflow that sets no
// timeout-minutes warns and passes the audit before forge.JobTimeoutFailFrom, and fails the audit
// with a [FAIL] line from that day on. The unit tests in internal/forge pin both sides of the
// switch with a fixed day; this test moves the cutoff to check both sides and today's UTC
// boundary through the CLI on every run. Subtests run serially and restore the shared cutoff.
func TestAuditJobTimeouts_CLI_DatedSwitch(t *testing.T) {
	const finding = "job-timeout: .github/workflows/job_missing.yml:3 job build: declares no timeout-minutes"
	now := time.Now().UTC()
	for _, tc := range []struct {
		name     string
		failFrom time.Time
		wantFail bool
	}{
		{"warn", now.AddDate(0, 0, 1), false},
		{"fail", now.AddDate(0, 0, -1), true},
		{"boundary", time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := forge.JobTimeoutFailFrom
			forge.JobTimeoutFailFrom = tc.failFrom
			t.Cleanup(func() { forge.JobTimeoutFailFrom = previous })

			f := newAuditFixture(t)
			writeFixtureFile(t, f.dir, ".github/workflows/job_missing.yml",
				"on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: echo 1\n")
			out, err := f.audit(t)
			if tc.wantFail {
				if err == nil || !strings.Contains(err.Error()+out, "[FAIL] "+finding) {
					t.Fatalf("audit must fail with the finding from %s on; err: %v\nOutput: %s",
						forge.JobTimeoutFailFrom.Format(time.DateOnly), err, out)
				}
				return
			}
			if err != nil {
				t.Fatalf("audit failed in the warn period: %v\nOutput: %s", err, out)
			}
			if !strings.Contains(out, "[WARN] "+finding) || !strings.Contains(out, "[PASS] job-timeout") {
				t.Fatalf("audit output missing warning or gate pass:\n%s", out)
			}
		})
	}
}
