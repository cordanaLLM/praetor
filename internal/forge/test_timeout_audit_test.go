// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

var timeoutsToday = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

const boundWorkflow = "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    timeout-minutes: 30\n" +
	"    steps:\n      - run: go test -timeout 30m ./...\n"

const longWorkflow = "on: push\njobs:\n  test:\n    runs-on: windows-latest\n" +
	"    steps:\n      - run: go test -timeout 60m ./internal/adopt\n"

// Negative: a 60m timeout with no exceptions entry fails and names the workflow, the line and the value.
func TestAuditTestTimeouts_Negative_LongTimeoutWithoutEntry(t *testing.T) {
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"portability.yml": longWorkflow}))
	if err == nil || !strings.Contains(err.Error(), ".github/workflows/portability.yml:6") || !strings.Contains(err.Error(), "60m") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want the 60m line refused", out, err)
	}
}

// timeoutEntry is a test-timeout exceptions entry for the workflow file name.
func timeoutEntry(name, expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleTestTimeout, Path: ".github/workflows/" + name,
		Reason: "category B: the Windows adopt tests run past the bound (#1381)", Expires: expires}
}

// Boundary: a live entry passes the workflow and names its reason and expiry.
func TestAuditTestTimeouts_Boundary_LiveEntryPasses(t *testing.T) {
	root := triggerRepository(t, map[string]string{"portability.yml": longWorkflow})
	out, err := auditTimeouts(t, root, timeoutEntry("portability.yml", "2026-10-18"))
	if err != nil || !strings.Contains(out, "until 2026-10-18") || !strings.Contains(out, "#1381") {
		t.Fatalf("live entry: %q, %v; want the workflow passed with reason and expiry", out, err)
	}
}

// Negative: an expired entry excuses nothing; the failure names the expiry.
func TestAuditTestTimeouts_Negative_ExpiredEntryFails(t *testing.T) {
	root := triggerRepository(t, map[string]string{"portability.yml": longWorkflow})
	_, err := auditTimeouts(t, root, timeoutEntry("portability.yml", "2026-10-14"))
	if err == nil || !strings.Contains(err.Error(), "expired 2026-10-14") {
		t.Fatalf("expired entry: err = %v; want the expiry named", err)
	}
}

// Negative: an entry for a workflow within the bound excuses nothing and fails, so the entry goes
// once the timeout is back at the bound.
func TestAuditTestTimeouts_Negative_StaleEntryFails(t *testing.T) {
	root := triggerRepository(t, map[string]string{"ci.yml": boundWorkflow})
	_, err := auditTimeouts(t, root, timeoutEntry("ci.yml", "2026-10-18"))
	if err == nil || !strings.Contains(err.Error(), "excuses nothing") {
		t.Fatalf("stale entry: err = %v; want it refused", err)
	}
}

func auditTimeouts(t *testing.T, root string, entries ...config.Exception) (string, error) {
	t.Helper()
	return AuditTestTimeouts(t.Context(), TestTimeoutOptions{Root: root, Exceptions: entries, Today: timeoutsToday})
}

// Positive: a workflow within the bound passes without an entry.
func TestAuditTestTimeouts_Positive_WithinTheBound(t *testing.T) {
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": boundWorkflow}))
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want a pass", out, err)
	}
}

// Positive: a step with docker memory limits is ignored even when --stop-timeout is present.
func TestAuditTestTimeouts_Positive_DockerMemoryIgnored(t *testing.T) {
	wf := "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: docker run --memory 64m --memory-swap 64m --stop-timeout 5 image\n      - run: go test ./...\n"
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wf}))
	if err != nil {
		t.Fatalf("AuditTestTimeouts = %q, %v; want docker memory ignored", out, err)
	}
}

// Negative: go test with timeouts above the bound are flagged, including combinations like 1h5m.
func TestAuditTestTimeouts_Negative_GoTestLongTimeouts(t *testing.T) {
	wf1 := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -timeout 64m ./...\n"
	_, err1 := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wf1}))
	if err1 == nil || !strings.Contains(err1.Error(), "64m") {
		t.Fatalf("err1 = %v; want 64m flagged", err1)
	}

	wf2 := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./... -timeout=1h5m\n"
	_, err2 := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wf2}))
	if err2 == nil || !strings.Contains(err2.Error(), "1h5m") {
		t.Fatalf("err2 = %v; want 1h5m flagged", err2)
	}
}

// Boundary: go test with exactly 30m timeout passes.
func TestAuditTestTimeouts_Boundary_GoTestBoundPasses(t *testing.T) {
	wf := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -timeout 30m ./...\n"
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wf}))
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want 30m to pass", out, err)
	}
}

// Boundary: a job's timeout-minutes is not this gate's subject (decision
// engine-timeout-audit-scope-20261018): 45 passes, as on the gate before the fix.
func TestAuditTestTimeouts_TimeoutMinutesNotJudged(t *testing.T) {
	wfLong := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    timeout-minutes: 45\n    steps:\n      - run: go test ./...\n"
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wfLong}))
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want timeout-minutes: 45 left to a job-timeout gate", out, err)
	}
}

// Negative: an unparsable duration such as -timeout 5x is reported as unreadable.
func TestAuditTestTimeouts_Negative_UnreadableTimeout(t *testing.T) {
	wf := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test -timeout 5x ./...\n"
	_, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": wf}))
	if err == nil || !strings.Contains(err.Error(), "unreadable") || !strings.Contains(err.Error(), "5x") {
		t.Fatalf("err = %v; want unreadable timeout 5x reported", err)
	}
}
