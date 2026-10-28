// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
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
	// The finding stays reported, not enforced; the expired entry itself fails the audit's last step,
	// as an expired entry of every rule does.
	if err == nil || !strings.Contains(err.Error(), "[FAIL] Exceptions: the exceptions entry (rule HISS-18, .github/workflows/everywhere.yml) expired on") {
		t.Fatalf("audit must fail on the expired entry by name: %v\nOutput: %s", err, out)
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

// flavorFixtures are the smallest repositories adoption scaffolds each flavor's CI workflow in,
// by the flavor it resolves.
var flavorFixtures = map[string]map[string]string{
	"go-service": {"go.mod": "module example.com/widgets\n\ngo 1.24\n", "cmd/widgets/main.go": "package main\n\nfunc main() {}\n"},
	"rust-systems": {"Cargo.toml": "[package]\nname = \"widgets\"\nversion = \"0.1.0\"\nedition = \"2021\"\n",
		"src/main.rs": "fn main() {}\n"},
	"typescript-node": {"package.json": "{\"name\": \"widgets\", \"scripts\": {\"test\": \"node --test\"}}\n",
		"package-lock.json": "{\"name\": \"widgets\", \"lockfileVersion\": 3, \"requires\": true, \"packages\": {\"\": {\"name\": \"widgets\"}}}\n"},
	"jvm-service":    {"pom.xml": "<project/>\n", "src/main/java/App.java": "class App {}\n"},
	"mobile-flutter": {"pubspec.yaml": "name: widgets\nenvironment:\n  sdk: \">=3.0.0 <4.0.0\"\ndependencies:\n  flutter:\n    sdk: flutter\n", "lib/main.dart": "void main() {}\n"},
}

// End to end (#817): adopting each flavor scaffolds its CI workflow in the hosted gate shape
// (templates/hostedgate.go), so the audit --offline an adopted repository's pre-commit hook runs
// reports no HISS-18 finding on a fresh adoption: the CI workflow is written (Positive) and the
// trigger check passes over every workflow adoption wrote. Negative: an earlier rendering of the
// Go CI workflow, the one that ran on every draft, is still reported, so the pass is the shape's,
// not a check that stopped reading ci.yml.
func TestAdoptThenAuditReportsNoFlavorWorkflowTriggerFinding(t *testing.T) {
	for flavorName, files := range flavorFixtures {
		t.Run(flavorName, func(t *testing.T) {
			root, _ := adoptedRepository(t, files)
			if _, err := os.Stat(filepath.Join(root, ".github", "workflows", "ci.yml")); err != nil {
				t.Fatalf("adoption wrote no CI workflow for %s: %v", flavorName, err)
			}
			out := auditOffline(t, root)
			mustContain(t, out, "[PASS] Workflow triggers (HISS-18): workflows read: ")
			if strings.Contains(out, "[WARN] Workflow triggers") {
				t.Fatalf("a fresh %s adoption reports a HISS-18 finding:\n%s", flavorName, out)
			}
		})
	}
	root, _ := adoptedRepository(t, flavorFixtures["go-service"])
	earlier, err := os.ReadFile(filepath.Join("..", "..", "internal", "flavor", "testdata", "ci-prior", "go", "520.yml"))
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, root, ".github/workflows/ci.yml", string(earlier))
	mustContain(t, auditOffline(t, root), "[WARN] Workflow triggers (HISS-18): .github/workflows/ci.yml:", "runs on a draft pull request",
		"reported, not enforced")
}

// auditOffline runs audit --offline on the adopted repository at root and fails the test when the
// audit fails.
func auditOffline(t *testing.T, root string) string {
	t.Helper()
	out, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--config=" + filepath.Join(root, ".standards.yaml"), "--offline"})
	})
	if err != nil {
		t.Fatalf("audit --offline of a fresh adoption failed: %v\n%s", err, out)
	}
	return out
}

// TestAuditWorkflowTriggers_CLI_ExternalReusable runs the audit's HISS-18 gate on a workflow
// whose pull_request job calls a reusable workflow of another repository pinned to a commit
// (order engine-hiss18-cross-repo-reusable-20261027). With a token, the gate reads the callee at
// the pin from a stand-in forge and passes; with --offline or without a token, it names the call
// as not read and asks the forge nothing. The other audit gates are left out, because the
// required-check discovery of the branch protection gate refuses a call to another repository on
// its own terms.
func TestAuditWorkflowTriggers_CLI_ExternalReusable(t *testing.T) {
	const (
		action = "lusoris/SnatchArr/.github/workflows/commit-policy.yml"
		sha    = "2bf15d49f0eca695707a999808bccd988e2160e1"
	)
	calleeBody := "on: workflow_call\njobs:\n  gate:\n    runs-on: ubuntu-latest\n    steps:\n" +
		ghworkflow.HostedGateDraftStep + "      - run: make check\n"
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.URL.Path+"?"+r.URL.RawQuery+" "+r.Header.Get("Accept"))
		if _, err := w.Write([]byte(calleeBody)); err != nil {
			t.Errorf("write response: %v", err)
		}
	}))
	defer server.Close()
	origEndpoint, origToken := actionsForgeEndpoint, resolveActionsToken
	t.Cleanup(func() { actionsForgeEndpoint, resolveActionsToken = origEndpoint, origToken })
	actionsForgeEndpoint = server.URL
	token := "test-token"
	resolveActionsToken = func(context.Context) string { return token }

	root := t.TempDir()
	writeFixtureFile(t, root, ".github/workflows/ci.yml", "on:\n  pull_request:\n"+
		"    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n  commits:\n    uses: "+action+"@"+sha+"\n")
	gate := func(offline bool) string {
		out, err := captureStdout(t, func() error {
			return auditWorkflowTriggers(t.Context(), &config.Manifest{}, root, offline)
		})
		if err != nil {
			t.Fatalf("HISS-18 gate failed: %v\n%s", err, out)
		}
		return out
	}

	out := gate(false)
	mustContain(t, out, "[PASS] Workflow triggers (HISS-18): workflows read: 1;")
	want := "/repos/lusoris/SnatchArr/contents/.github/workflows/commit-policy.yml?ref=" + sha + " application/vnd.github.raw+json"
	if strings.Contains(out, "[WARN]") || strings.Contains(out, "[SKIP]") || len(requests) != 1 || requests[0] != want {
		t.Fatalf("online gate: requests %q, want [%q]\n%s", requests, want, out)
	}

	skip := "[SKIP] Workflow triggers (HISS-18): .github/workflows/ci.yml:5: job commits calls the reusable workflow " +
		action + " of another repository at the pinned commit " + sha + ", which was not read ("
	mustContain(t, gate(true), skip+"--offline)")
	token = ""
	mustContain(t, gate(false), skip+"no forge token")
	if len(requests) != 1 {
		t.Fatalf("offline or tokenless gate asked the forge: %q", requests)
	}
}
