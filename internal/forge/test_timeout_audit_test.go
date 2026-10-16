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

const boundWorkflow = "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    timeout-minutes: 90\n" +
	"    steps:\n      - run: go test -timeout 30m ./...\n"

const longWorkflow = "on: push\njobs:\n  test:\n    runs-on: windows-latest\n    env:\n" +
	"      ADOPT_TEST_TIMEOUT: ${{ runner.os == 'Windows' && '60m' || '30m' }}\n" +
	"    steps:\n      - run: go test -timeout \"$ADOPT_TEST_TIMEOUT\" ./internal/adopt\n"

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

// Positive: a workflow within the bound passes without an entry, and a job's timeout-minutes
// (a number without a unit) is not read as a test timeout.
func TestAuditTestTimeouts_Positive_WithinTheBound(t *testing.T) {
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci.yml": boundWorkflow}))
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want a pass", out, err)
	}
}
