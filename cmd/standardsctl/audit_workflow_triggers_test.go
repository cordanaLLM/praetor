// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// fixtureEverywhereWorkflow is a push workflow without a branch filter.
const fixtureEverywhereWorkflow = "on: push\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: make\n"

// The CLI audit runs the HISS-18 workflow trigger check (#817). Boundary: the fixture holds no
// workflow, so the check is skipped with its reason. Positive: a push without a branch filter is
// reported as a [WARN] line naming the file and line, and the audit passes. Negative: a HISS-18
// exceptions entry that expired yesterday no longer excuses the workflow, and the audit names the
// expired entry; a live one prints the finding under a [PASS] line.
func TestAuditWorkflowTriggers_CLI(t *testing.T) {
	f := newAuditFixture(t)
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit failed: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[SKIP] Workflow triggers (HISS-18): the repository holds no workflow under .github/workflows, so no trigger was judged.")

	writeFixtureFile(t, f.dir, ".github/workflows/everywhere.yml", fixtureEverywhereWorkflow)
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on a reported workflow: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[WARN] Workflow triggers (HISS-18): .github/workflows/everywhere.yml:1: push runs on every branch and tag",
		"reported, not enforced, because HISS-18's failure action is a CI optimization gate")

	today := config.ExceptionDay(time.Now())
	entry := func(expires time.Time) string {
		return "exceptions:\n  - rule: \"HISS-18\"\n    path: \".github/workflows/everywhere.yml\"\n" +
			"    reason: \"builds every branch push by design\"\n    expires: \"" + expires.Format(config.ExceptionDateLayout) + "\"\n"
	}
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+entry(today.AddDate(0, 0, -1)))
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on an expired entry: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[WARN] Workflow triggers (HISS-18): the exceptions entry (rule HISS-18, .github/workflows/everywhere.yml) expired on",
		"[WARN] Workflow triggers (HISS-18): .github/workflows/everywhere.yml:1: push runs on every branch and tag")

	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+entry(today.AddDate(0, 0, 30)))
	out, err = f.audit(t)
	if err != nil {
		t.Fatalf("audit failed on a live entry: %v\nOutput: %s", err, out)
	}
	mustContain(t, out, "[PASS] Workflow triggers (HISS-18): .github/workflows/everywhere.yml excepted until",
		"\n  - .github/workflows/everywhere.yml:1: push runs on every branch and tag")
	if strings.Contains(out, "[WARN] Workflow triggers") {
		t.Fatalf("a live entry left a warning:\n%s", out)
	}
}

// End to end (#817): adopting a Go service (go.mod and cmd/) scaffolds the go-service flavor's
// ci.yml, whose job runs on draft pull requests. The audit reports that workflow and still
// passes, so the pre-commit audit --offline an adopted repository runs does not block its commits
// on a HISS-18 finding in a workflow Praetor wrote.
func TestAdoptThenAuditReportsTheFlavorWorkflowTriggers(t *testing.T) {
	root, _ := adoptedRepository(t, map[string]string{
		"go.mod": "module example.com/widgets\n\ngo 1.24\n", "cmd/widgets/main.go": "package main\n\nfunc main() {}\n",
	})
	out, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--config=" + filepath.Join(root, ".standards.yaml"), "--offline"})
	})
	if err != nil {
		t.Fatalf("audit --offline of a fresh adoption failed: %v\n%s", err, out)
	}
	mustContain(t, out, "[WARN] Workflow triggers (HISS-18): .github/workflows/ci.yml:", "runs on a draft pull request",
		"reported, not enforced")
}
