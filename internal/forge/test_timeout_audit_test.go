// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"os"
	"path/filepath"
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
	out, err := auditTimeouts(t, root, timeoutEntry("portability.yml", timeoutsToday.AddDate(0, 0, 3).Format(config.ExceptionDateLayout)))
	if err != nil || !strings.Contains(out, "until "+timeoutsToday.AddDate(0, 0, 3).Format(config.ExceptionDateLayout)) || !strings.Contains(out, "#1381") {
		t.Fatalf("live entry: %q, %v; want the workflow passed with reason and expiry", out, err)
	}
}

// Negative: an expired entry excuses nothing; the failure names the expiry.
func TestAuditTestTimeouts_Negative_ExpiredEntryFails(t *testing.T) {
	root := triggerRepository(t, map[string]string{"portability.yml": longWorkflow})
	_, err := auditTimeouts(t, root, timeoutEntry("portability.yml", timeoutsToday.AddDate(0, 0, -1).Format(config.ExceptionDateLayout)))
	if err == nil || !strings.Contains(err.Error(), "expired "+timeoutsToday.AddDate(0, 0, -1).Format(config.ExceptionDateLayout)) {
		t.Fatalf("expired entry: err = %v; want the expiry named", err)
	}
}

// Negative: an entry for a workflow within the bound excuses nothing and fails, so the entry goes
// once the timeout is back at the bound.
func TestAuditTestTimeouts_Negative_StaleEntryFails(t *testing.T) {
	root := triggerRepository(t, map[string]string{"ci.yml": boundWorkflow})
	_, err := auditTimeouts(t, root, timeoutEntry("ci.yml", timeoutsToday.AddDate(0, 0, 3).Format(config.ExceptionDateLayout)))
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

// reusableTimeoutWorkflow is a reusable workflow whose go test takes its timeout from an input,
// as golusoris's ci-go.yml does; inputBlock declares the input (or nothing).
func reusableTimeoutWorkflow(inputBlock string) string {
	return "on:\n  workflow_call:\n    inputs:\n" + inputBlock +
		"jobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n" +
		"      - run: go test -race -timeout=${{ inputs.test-timeout }} ./...\n"
}

// Positive: -timeout=${{ inputs.X }} reads the workflow_call default; 10m is within the bound.
func TestAuditTestTimeouts_Positive_InputDefaultWithinBound(t *testing.T) {
	wf := reusableTimeoutWorkflow("      test-timeout:\n        type: string\n        default: 10m\n")
	out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci-go.yml": wf}))
	if err != nil || !strings.Contains(out, "[PASS]") {
		t.Fatalf("AuditTestTimeouts = %q, %v; want the 10m default to pass", out, err)
	}
}

// Negative: a default above the bound fails, naming the input and its default.
func TestAuditTestTimeouts_Negative_InputDefaultAboveBound(t *testing.T) {
	wf := reusableTimeoutWorkflow("      test-timeout:\n        type: string\n        default: 45m\n")
	_, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci-go.yml": wf}))
	if err == nil || !strings.Contains(err.Error(), "inputs.test-timeout") || !strings.Contains(err.Error(), "default 45m") ||
		!strings.Contains(err.Error(), "above the 30m bound") {
		t.Fatalf("err = %v; want the 45m default above the bound", err)
	}
}

// Boundary: an input without a default, or one the workflow does not declare, stays unreadable.
func TestAuditTestTimeouts_Boundary_InputWithoutDefaultUnreadable(t *testing.T) {
	for name, block := range map[string]string{
		"no default": "      test-timeout:\n        type: string\n        required: true\n",
		"undeclared": "      other:\n        type: string\n        default: 5m\n",
		"unparsable": "      test-timeout:\n        type: string\n        default: soon\n",
	} {
		_, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci-go.yml": reusableTimeoutWorkflow(block)}))
		if err == nil || !strings.Contains(err.Error(), "unreadable") || !strings.Contains(err.Error(), "inputs.test-timeout") {
			t.Fatalf("%s: err = %v; want an unreadable timeout naming the input", name, err)
		}
	}
	wf := reusableTimeoutWorkflow("      test-timeout:\n        type: string\n        default: 30m\n")
	if out, err := auditTimeouts(t, triggerRepository(t, map[string]string{"ci-go.yml": wf})); err != nil {
		t.Fatalf("default at the bound = %q, %v; want a pass", out, err)
	}
}

// Makefile recipes obey the workflow bound, including the GO_TEST sandbox wrapper.
func TestAuditTestTimeouts_Makefile(t *testing.T) {
	entry := config.Exception{Rule: config.ExceptionRuleTestTimeout, Path: "Makefile",
		Category: "C", Entered: "2026-10-15", Expires: "2026-10-25", Reason: "category C: adopt speedup pending (#1381)"}
	expired := entry
	expired.Expires = "2026-10-14"
	for _, tc := range []struct {
		name    string
		command string
		entries []config.Exception
		wantErr string
		wantOut string
	}{
		{"no entry", "go test -race -timeout 45m ./internal/adopt/", nil, "Makefile:2 sets 45m", ""},
		{"wrapper without entry", "$(GO_TEST) -race -timeout 45m ./internal/adopt/", nil, "Makefile:2 sets 45m", ""},
		{"live entry", "$(GO_TEST) -race -timeout 45m ./internal/adopt/", []config.Exception{entry}, "", "Makefile excepted until 2026-10-25"},
		{"expired entry", "$(GO_TEST) -race -timeout 45m ./internal/adopt/", []config.Exception{expired}, "Makefile expired 2026-10-14", ""},
		{"boundary", "$(GO_TEST) -race -timeout 30m ./...", nil, "", "[PASS] test-timeout"},
		{"continued without entry", "$(GO_TEST) -v -race \\\n  -timeout 45m ./internal/adopt/", nil, "Makefile:2 sets 45m", ""},
		{"continued live entry", "$(GO_TEST) -v -race \\\n  -timeout 45m ./internal/adopt/", []config.Exception{entry}, "", "Makefile excepted until 2026-10-25"},
		{"continued boundary", "$(GO_TEST) -v -race \\\n  -timeout 30m ./...", nil, "", "[PASS] test-timeout"},
		{"stale entry", "$(GO_TEST) -race -timeout 30m ./...", []config.Exception{entry}, "Makefile excuses nothing", ""},
		{"workflow entry cannot excuse Makefile", "go test -timeout 45m ./internal/adopt/", []config.Exception{timeoutEntry("ci.yml", "2026-10-18")}, "Makefile:2 sets 45m", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("test:\n\t"+tc.command+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := auditTimeouts(t, root, tc.entries...)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("AuditTestTimeouts = %q, %v; want %q", out, err, tc.wantErr)
				}
				return
			}
			if err != nil || !strings.Contains(out, tc.wantOut) {
				t.Fatalf("AuditTestTimeouts = %q, %v; want %q", out, err, tc.wantOut)
			}
		})
	}
}

// TestAuditTestTimeouts_MakefileDanglingContinuation refuses a Makefile whose last line ends in a
// continuation, naming that line.
func TestAuditTestTimeouts_MakefileDanglingContinuation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte("test:\n\t$(GO_TEST) -race \\"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := auditTimeouts(t, root)
	if err == nil || !strings.Contains(err.Error(), "Makefile:2") {
		t.Fatalf("dangling continuation: %v; want Makefile:2 refused", err)
	}
}

// TestAuditTestTimeouts_ScanBound accepts a Makefile of exactly the line bound and refuses a Makefile
// or a workflow one line over it, so no line goes unaudited.
func TestAuditTestTimeouts_ScanBound(t *testing.T) {
	body := func(lines int) string { return "test:\n" + strings.Repeat("\t@true\n", lines-1) }
	atBound := t.TempDir()
	if err := os.WriteFile(filepath.Join(atBound, "Makefile"), []byte(body(maxTimeoutWorkflowLines)), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := auditTimeouts(t, atBound); err != nil {
		t.Fatalf("Makefile at the %d-line bound: %q, %v; want a pass", maxTimeoutWorkflowLines, out, err)
	}
	over := t.TempDir()
	if err := os.WriteFile(filepath.Join(over, "Makefile"), []byte(body(maxTimeoutWorkflowLines+1)), 0o600); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("above the %d-line scan bound", maxTimeoutWorkflowLines)
	if _, err := auditTimeouts(t, over); err == nil || !strings.Contains(err.Error(), "Makefile has") || !strings.Contains(err.Error(), want) {
		t.Fatalf("Makefile over the bound: %v; want it refused naming the bound", err)
	}
	workflow := "on: push\njobs:\n  test:\n    runs-on: ubuntu-latest\n    steps:\n      - run: go test ./...\n" +
		strings.Repeat("#\n", maxTimeoutWorkflowLines)
	if _, err := auditTimeouts(t, triggerRepository(t, map[string]string{"long.yml": workflow})); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("workflow over the bound: %v; want it refused naming the bound", err)
	}
}

// The checked-in recipe and declaration must exercise the same audit as adopter files.
func TestAuditTestTimeouts_RepositoryMakefile(t *testing.T) {
	root := filepath.Join("..", "..")
	manifest, err := config.LoadManifest(filepath.Join(root, config.ManifestFileName))
	if err != nil {
		t.Fatal(err)
	}
	// Pin the declaration's entry day; expiry itself is exercised by the negative fixture.
	today := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	out, err := AuditTestTimeouts(t.Context(), TestTimeoutOptions{Root: root, Exceptions: manifest.Exceptions, Today: today})
	if err != nil || !strings.Contains(out, "Makefile excepted until 2026-11-20") || !strings.Contains(out, "1381") {
		t.Fatalf("repository timeout audit = %q, %v; want the dated Makefile exception", out, err)
	}
}
