// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// triggersToday is the fixed day the trigger check's exceptions are judged against.
var triggersToday = time.Date(2026, time.October, 7, 15, 0, 0, 0, time.UTC)

// hostedGateWorkflow is a one-job workflow in the hosted gate shape: it runs on the gate's
// pull_request types and on a push to main, and its job stops on a draft in its first step.
var hostedGateWorkflow = string(draftWorkflow("", ghworkflow.HostedGateDraftStep+gateRunStep))

// triggerRepository writes files under .github/workflows of a fresh root and returns the root.
func triggerRepository(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		writeWorkflowFixture(t, root, name, body)
	}
	return root
}

// auditTriggers runs the check over root with entries and fails the test on an error.
func auditTriggers(t *testing.T, root string, entries ...config.Exception) string {
	t.Helper()
	out, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: root, Exceptions: entries, Today: triggersToday})
	if err != nil {
		t.Fatalf("AuditWorkflowTriggers: %v", err)
	}
	return out
}

// triggerEntry is a HISS-18 exceptions entry for the workflow file name, expiring on expires.
func triggerEntry(name, expires string) config.Exception {
	return config.Exception{Rule: config.ExceptionRuleWorkflowTriggers, Path: ".github/workflows/" + name,
		Reason: "runs on every branch push by design", Expires: expires}
}

// Positive (#817): the hosted gate shape passes, and so do a tag release, a push filtered to
// named branches, a schedule, two jobs that each stop a draft in their own first step, a job a
// pull_request run never starts, and a job calling a reusable workflow of the repository whose
// jobs each stop a draft in their first step or never start on a pull request. The pass line
// counts the workflows it read.
func TestAuditWorkflowTriggers_Positive_HostedGateShapePasses(t *testing.T) {
	stopping := "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n" +
		"  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make plan\n" +
		"  lane:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make lane\n" +
		"  deploy:\n    if: github.event_name != 'pull_request'\n    runs-on: x\n    steps:\n      - run: make deploy\n"
	caller := "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n" +
		"  call:\n    uses: ./.github/workflows/reusable.yml\n"
	reusable := "on: workflow_call\njobs:\n  gate:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + gateRunStep +
		"  docs:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make docs\n" +
		"  publish:\n    if: github.event_name == 'push'\n    runs-on: x\n    steps:\n      - run: make publish\n"
	root := triggerRepository(t, map[string]string{
		"gate.yml":     hostedGateWorkflow,
		"release.yml":  "on:\n  push:\n    tags: ['v*']\njobs:\n  release:\n    runs-on: x\n    steps:\n      - run: make dist\n",
		"branches.yml": "on:\n  push:\n    branches: [main, 'release/**']\njobs:\n  build:\n    runs-on: x\n    steps:\n      - run: make\n",
		"nightly.yml":  "on:\n  schedule:\n    - cron: '0 3 * * *'\njobs:\n  scan:\n    runs-on: x\n    steps:\n      - run: make scan\n",
		"planned.yml":  stopping,
		"caller.yml":   caller,
		"reusable.yml": reusable,
	})
	out := auditTriggers(t, root)
	want := "[PASS] Workflow triggers (HISS-18): workflows read: 7; no push trigger runs on every branch and no job a " +
		"pull request starts runs its work on a draft."
	if out != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out, want)
	}
}

// Negative (#817): each wasted run is reported as a [WARN] line naming the file, the line and the
// trigger or job, followed by the verdict, which passes: an unfiltered push in each of its three
// shapes, a push with only a paths or a branches-ignore filter, a branches pattern of asterisks,
// a job-level draft skip, a job without the draft step, a draft step that continues on error, a
// draft step whose trigger does not run on ready_for_review, a job calling another repository's
// reusable workflow, and a job that needs a stopping job, whatever its condition and with or
// without the draft step of its own: GitHub skips it on a draft unless the condition runs it after
// the failed need, and reports that skip as successful, which a required check accepts.
func TestAuditWorkflowTriggers_Negative_ReportsEachWastedRun(t *testing.T) {
	const prTypes = "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n"
	job := "    runs-on: x\n    steps:\n      - run: make test\n"
	cases := map[string]struct{ doc, want string }{
		"scalar push":     {"on: push\njobs:\n  b:\n" + job, "x.yml:1: push runs on every branch and tag: it names no branches or tags filter."},
		"sequence push":   {"on: [push]\njobs:\n  b:\n" + job, "x.yml:1: push runs on every branch and tag"},
		"empty push":      {"on:\n  push: {}\njobs:\n  b:\n" + job, "x.yml:2: push runs on every branch and tag"},
		"paths only":      {"on:\n  push:\n    paths: [src/**]\njobs:\n  b:\n" + job, "x.yml:2: push runs on every branch and tag"},
		"branches-ignore": {"on:\n  push:\n    branches-ignore: [main]\njobs:\n  b:\n" + job, "x.yml:2: push runs on every branch its branches-ignore filter does not name"},
		"every branch":    {"on:\n  push:\n    branches: [main, '**']\njobs:\n  b:\n" + job, `x.yml:2: push runs on every branch: its branches filter "**" matches every branch name.`},
		"every flat branch": {"on:\n  push:\n    branches: ['*']\njobs:\n  b:\n" + job,
			`x.yml:2: push runs on every branch without a slash in its name: its branches filter "*" matches every branch name without a slash.`},
		"draft skip": {
			string(draftWorkflow(ghworkflow.HostedGateNotDraft, "      - run: make gate\n")),
			"x.yml:7: pull_request job gate skips a draft with the job condition \"github.event.pull_request.draft != true\": GitHub reports a job its condition skipped as successful",
		},
		"runs on draft": {"on: pull_request\njobs:\n  test:\n" + job,
			`x.yml:3: pull_request job test runs on a draft pull request: its first step is not the draft step "Stop on a draft pull request"`},
		"advisory stop": {strings.Replace(hostedGateWorkflow, "    name: Gate\n", "    name: Gate\n    continue-on-error: true\n", 1),
			"x.yml:7: pull_request job gate stops a draft in its first step but continues on error"},
		"not ready": {strings.Replace(hostedGateWorkflow, ", ready_for_review]", "]", 1),
			"x.yml:2: pull_request stops a draft in its jobs but does not run on ready_for_review"},
		"remote callee": {prTypes + "  call:\n    uses: acme/ci/.github/workflows/gate.yml@v1\n",
			"x.yml:5: pull_request job call runs on a draft pull request: it calls acme/ci/.github/workflows/gate.yml@v1"},
		"runs after need": {prTypes + "  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep +
			"  lane:\n    needs: [plan]\n    if: always()\n" + job, "x.yml:19: pull_request job lane may be skipped on a draft because it needs plan"},
		"skipped by need": {prTypes + "  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep +
			"  ci-ok:\n    needs: plan\n" + job, "x.yml:19: pull_request job ci-ok may be skipped on a draft because it needs plan, " +
			"which is held back on one: unless the job's condition runs it after a failed need, which this check does not read, " +
			"GitHub skips it and reports the skip as successful, which a required check accepts; drop the need and begin the job " +
			`with the draft step "Stop on a draft pull request", or declare a workflow whose aggregate job needs the others in the exceptions list.`},
		"own draft step skipped by need": {prTypes + "  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep +
			"  ci-ok:\n    needs: plan\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep,
			"x.yml:19: pull_request job ci-ok may be skipped on a draft because it needs plan"},
	}
	for name, tc := range cases {
		out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": tc.doc}))
		if !strings.Contains(out, "[WARN] Workflow triggers (HISS-18): .github/workflows/"+tc.want) {
			t.Errorf("%s: output lacks %q:\n%s", name, tc.want, out)
		}
		if !strings.HasSuffix(out, "workflows start runs this check counts as wasted (findings: 1); reported, not enforced, "+
			"because HISS-18's failure action is a CI optimization gate. Move each gate to the hosted gate shape "+
			"(docs/guides/workflow-triggers.md), or declare a workflow that must run on every branch or draft in the exceptions "+
			"list of .standards.yaml (rule HISS-18, its path, a reason, and an expiry at most 90 days ahead).") {
			t.Errorf("%s: the verdict does not report exactly one finding:\n%s", name, out)
		}
	}
}

// A job a draft skips through its needs. Negative: every job of a chain behind a stopping job is
// reported, each naming the need that holds it back, since each one reports success on a draft
// and any of them may be the required check. Boundary: a job without a condition that also needs
// a job a pull_request run never starts is skipped on every pull request, not only on a draft, so
// the draft check leaves it alone.
func TestAuditWorkflowTriggers_JobsSkippedThroughNeeds(t *testing.T) {
	plan := "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n" +
		"  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make plan\n"
	run := "    runs-on: x\n    steps:\n      - run: make\n"
	chain := plan + "  build:\n    needs: plan\n" + run + "  ci-ok:\n    needs: [build]\n" + run
	out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": chain}))
	for _, want := range []string{
		"[WARN] Workflow triggers (HISS-18): .github/workflows/x.yml:20: pull_request job build may be skipped on a draft because it needs plan,",
		"[WARN] Workflow triggers (HISS-18): .github/workflows/x.yml:25: pull_request job ci-ok may be skipped on a draft because it needs build,",
		"1 of 1 workflows start runs this check counts as wasted (findings: 2)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chain: output lacks %q:\n%s", want, out)
		}
	}
	passing := plan + "  deploy:\n    if: github.event_name != 'pull_request'\n" + run +
		"  publish:\n    needs: [plan, deploy]\n" + run
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": passing})); !strings.HasPrefix(out, "[PASS]") {
		t.Errorf("a job without a condition behind an unreached need must pass:\n%s", out)
	}
}

// Protect by default (#817): the check does not read the status functions in a job's condition,
// so a job that needs a job held back on a draft is reported whatever they say, even when it begins
// with the draft step itself. Negative: a condition that mentions a status function but is false
// after a failed need (GitHub's failure() holds when an ancestor job failed), one that mentions
// always() inside a string, a compound always() condition, and the always() and !cancelled()
// aggregate shapes the check cannot yet prove (#821). Boundary: behind a need a pull_request run
// never starts, a job with a condition of its own is judged alone, because always() runs it on
// every pull request; without the draft step it is reported as running on a draft.
func TestAuditWorkflowTriggers_NeedSkipWhateverTheCondition(t *testing.T) {
	plan := "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n" +
		"  plan:\n    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep
	for _, condition := range []string{
		"${{ !failure() && !cancelled() }}",
		"always() && needs.plan.result == 'success'",
		"cancelled()",
		"contains(github.event.pull_request.title, 'always()')",
		"${{ always() }}",
		"${{ !cancelled() }}",
	} {
		doc := plan + "  ci-ok:\n    needs: plan\n    if: " + condition + "\n    runs-on: x\n    steps:\n" +
			ghworkflow.HostedGateDraftStep + "      - run: make\n"
		out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": doc}))
		want := "[WARN] Workflow triggers (HISS-18): .github/workflows/x.yml:19: pull_request job ci-ok may be skipped on a " +
			"draft because it needs plan, which is held back on one"
		if !strings.Contains(out, want) || !strings.Contains(out, "(findings: 1)") {
			t.Errorf("condition %q: output lacks %q as its one finding:\n%s", condition, want, out)
		}
	}
	unreached := plan + "  deploy:\n    if: github.event_name != 'pull_request'\n    runs-on: x\n    steps:\n      - run: make\n" +
		"  publish:\n    needs: [deploy]\n    if: always()\n    runs-on: x\n    steps:\n      - run: make publish\n"
	out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": unreached}))
	if want := ".github/workflows/x.yml:24: pull_request job publish runs on a draft pull request: its first step is not the draft step"; !strings.Contains(out, want) {
		t.Errorf("an always() job behind an unreached need: output lacks %q:\n%s", want, out)
	}
}

// A job calling a reusable workflow of this repository stops a draft only when the callee's jobs,
// judged with their needs, each stop it. Negative: a callee job that needs another one beginning
// with the draft step is skipped on a draft and reported as successful, so the caller is
// reported; so is a caller whose callee calls a further reusable workflow, which is not read.
// Boundary: the same callee without the need passes.
func TestAuditWorkflowTriggers_FollowsNeedsInsideLocalCallee(t *testing.T) {
	caller := "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n" +
		"  call:\n    uses: ./.github/workflows/reusable.yml\n"
	stopping := "    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + gateRunStep
	want := ".github/workflows/caller.yml:5: pull_request job call runs on a draft pull request: it calls " +
		"./.github/workflows/reusable.yml, whose jobs this repository does not show each beginning with the draft step " +
		`"Stop on a draft pull request" and needing no job held back on a draft.`
	for name, callee := range map[string]string{
		"need inside callee": "on: workflow_call\njobs:\n  a:\n" + stopping + "  b:\n    needs: a\n" + stopping,
		"nested callee":      "on: workflow_call\njobs:\n  a:\n" + stopping + "  b:\n    uses: ./.github/workflows/caller.yml\n",
	} {
		out := auditTriggers(t, triggerRepository(t, map[string]string{"caller.yml": caller, "reusable.yml": callee}))
		if !strings.Contains(out, "[WARN] Workflow triggers (HISS-18): "+want) {
			t.Errorf("%s: output lacks %q:\n%s", name, want, out)
		}
	}
	plain := "on: workflow_call\njobs:\n  a:\n" + stopping + "  b:\n" + stopping
	if out := auditTriggers(t, triggerRepository(t, map[string]string{"caller.yml": caller, "reusable.yml": plain})); !strings.HasPrefix(out, "[PASS]") {
		t.Errorf("a callee whose jobs each stop the draft must pass:\n%s", out)
	}
}

// Exceptions. Positive: a live HISS-18 entry naming a workflow prints its findings under a [PASS]
// line with the entry's expiry and reason, on its expires day too, and the verdict passes.
// Negative: the day after, the findings are reported again under a line naming the expired
// entry; an entry naming a workflow without findings is stale; a malformed entry fails the check.
// Boundary: an entry of another rule is not read.
func TestAuditWorkflowTriggers_Exceptions(t *testing.T) {
	root := triggerRepository(t, map[string]string{"everywhere.yml": "on: push\njobs:\n  b:\n    runs-on: x\n    steps:\n      - run: make\n",
		"gate.yml": hostedGateWorkflow})
	live := auditTriggers(t, root, triggerEntry("everywhere.yml", "2026-10-07"))
	for _, want := range []string{
		"[PASS] Workflow triggers (HISS-18): .github/workflows/everywhere.yml excepted until 2026-10-07 by the exceptions entry " +
			"(rule HISS-18, .github/workflows/everywhere.yml): runs on every branch push by design",
		"\n  - .github/workflows/everywhere.yml:1: push runs on every branch and tag",
		"\n[PASS] Workflow triggers (HISS-18): workflows read: 2; no push trigger runs on every branch and no job a pull " +
			"request starts runs its work on a draft outside the excepted workflows above (1).",
	} {
		if !strings.Contains(live, want) {
			t.Errorf("live entry: output lacks %q:\n%s", want, live)
		}
	}
	if strings.Contains(live, "[WARN]") {
		t.Errorf("live entry: a finding was still reported:\n%s", live)
	}
	expired, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: root, Today: triggersToday.AddDate(0, 0, 1),
		Exceptions: []config.Exception{triggerEntry("everywhere.yml", "2026-10-07"), triggerEntry("gate.yml", "2026-12-31")}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"[WARN] Workflow triggers (HISS-18): the exceptions entry (rule HISS-18, .github/workflows/everywhere.yml) expired on 2026-10-07",
		"[WARN] Workflow triggers (HISS-18): .github/workflows/everywhere.yml:1: push runs on every branch and tag",
		"[WARN] Workflow triggers (HISS-18): the exceptions entry (rule HISS-18, .github/workflows/gate.yml) excuses no finding",
		"1 of 2 workflows start runs this check counts as wasted (findings: 1)",
	} {
		if !strings.Contains(expired, want) {
			t.Errorf("expired entry: output lacks %q:\n%s", want, expired)
		}
	}
	other := triggerEntry("everywhere.yml", "2026-12-31")
	other.Rule = config.ExceptionRuleSupplyChain
	if out := auditTriggers(t, root, other); !strings.Contains(out, "[WARN] Workflow triggers (HISS-18): .github/workflows/everywhere.yml:1") {
		t.Errorf("an entry of another rule excused the workflow:\n%s", out)
	}
	malformed := triggerEntry("everywhere.yml", "2026-12-31")
	malformed.Path = "everywhere.yml"
	if _, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: root, Exceptions: []config.Exception{malformed},
		Today: triggersToday}); err == nil || !strings.Contains(err.Error(), "[FAIL] Workflow triggers (HISS-18): exceptions[0] rule HISS-18 must name one workflow file") {
		t.Errorf("a malformed entry: %v", err)
	}
}

// Boundary: a repository without workflows is skipped with the reason stated, and a stale entry
// is still reported there. Negative: a workflow the check cannot parse, an inventory above its
// bound and a missing context fail the check, because a check that did not run is not a pass.
func TestAuditWorkflowTriggers_SkipsWithoutWorkflowsAndFailsUnread(t *testing.T) {
	empty := t.TempDir()
	if out := auditTriggers(t, empty); out != "[SKIP] Workflow triggers (HISS-18): the repository holds no workflow under "+
		".github/workflows, so no trigger was judged." {
		t.Fatalf("no workflows: %q", out)
	}
	stale := auditTriggers(t, empty, triggerEntry("gone.yml", "2026-12-31"))
	if !strings.HasPrefix(stale, "[WARN] Workflow triggers (HISS-18): the exceptions entry (rule HISS-18, .github/workflows/gone.yml) excuses no finding") ||
		!strings.HasSuffix(stale, "so no trigger was judged.") {
		t.Fatalf("a stale entry without workflows: %q", stale)
	}
	broken := triggerRepository(t, map[string]string{"broken.yml": "on: [push\n"})
	if _, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: broken, Today: triggersToday}); err == nil ||
		!strings.Contains(err.Error(), "[FAIL] Workflow triggers (HISS-18): cannot read the workflows: workflow broken.yml") {
		t.Fatalf("an unparsable workflow: %v", err)
	}
	crowded := t.TempDir()
	for i := 0; i <= maxWorkflowFiles; i++ {
		writeWorkflowFixture(t, crowded, "w"+strings.Repeat("x", i)+".yml", "on: workflow_dispatch\njobs: {}\n")
	}
	if _, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: crowded, Today: triggersToday}); err == nil ||
		!strings.Contains(err.Error(), "inventory exceeds") {
		t.Fatalf("an inventory above the bound: %v", err)
	}
	var missing context.Context
	if _, err := AuditWorkflowTriggers(missing, WorkflowTriggerOptions{Root: empty}); err == nil || !strings.Contains(err.Error(), "requires a context") {
		t.Fatalf("a nil context: %v", err)
	}
}

// Positive: no workflow of this repository carries a finding (#817): the hosted gates it ships,
// rendered into .github/workflows, read one shape with the check (ghworkflow), and its own
// workflows (ci.yml, compliance.yml, pages.yml, portability.yml, security.yml) follow it.
func TestAuditWorkflowTriggers_EngineWorkflowsPass(t *testing.T) {
	engine := filepath.Join("..", "..")
	read, findings, err := workflowTriggerFindings(t.Context(), engine)
	if err != nil {
		t.Fatal(err)
	}
	if read < 11 {
		t.Fatalf("read %d workflows, want every engine workflow", read)
	}
	for _, finding := range findings {
		t.Errorf("an engine workflow is reported: %s", finding)
	}
}
