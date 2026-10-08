// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// readyTypes opens a workflow whose pull_request trigger runs on the hosted gate's types.
const readyTypes = "on:\n  pull_request:\n    types: [opened, synchronize, reopened, ready_for_review]\njobs:\n"

// draftStepJob is a job body without a condition whose first step is the draft step.
var draftStepJob = "    runs-on: x\n    steps:\n" + ghworkflow.HostedGateDraftStep + "      - run: make\n"

// renamings returns every assignment of names to three roles, each name used once: the six
// orders in which the check could meet the roles if it visited jobs by name.
func renamings(names [3]string) [][3]string {
	var out [][3]string
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				if i != j && j != k && i != k {
					out = append(out, [3]string{names[i], names[j], names[k]})
				}
			}
		}
	}
	return out
}

// The verdict follows the needs, never the job names (#817 rereview): the check decides each job
// once, after every job it needs. Positive: a gate with `if: always()` behind a job that needs a
// job a pull_request run never starts is judged alone and passes, under every naming. Negative: a
// plain chain behind a stopping job reports both jobs, each naming its own need, under every
// naming. No message names an empty need.
func TestAuditWorkflowTriggers_VerdictIndependentOfJobNames(t *testing.T) {
	orders := renamings([3]string{"a-job", "m-job", "z-job"})
	if len(orders) != 6 {
		t.Fatalf("renamings: %d orders, want 6", len(orders))
	}
	for _, names := range orders {
		gate, mid, last := names[0], names[1], names[2]
		unreached := readyTypes + "  " + gate + ":\n    needs: " + mid + "\n    if: always()\n" + draftStepJob +
			"  " + mid + ":\n    needs: " + last + "\n" + draftStepJob +
			"  " + last + ":\n    if: github.event_name != 'pull_request'\n    runs-on: x\n    steps:\n      - run: make deploy\n"
		out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": unreached}))
		if !strings.HasPrefix(out, "[PASS]") || strings.Contains(out, "needs ,") {
			t.Errorf("gate %s, mid %s, unreached %s: want a pass:\n%s", gate, mid, last, out)
		}
		chain := readyTypes + "  " + gate + ":\n    needs: " + mid + "\n" + draftStepJob +
			"  " + mid + ":\n    needs: [" + last + "]\n" + draftStepJob + "  " + last + ":\n" + draftStepJob
		out = auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": chain}))
		for _, want := range []string{
			"pull_request job " + gate + " may be skipped on a draft because it needs " + mid + ", which is held back on one",
			"pull_request job " + mid + " may be skipped on a draft because it needs " + last + ", which is held back on one",
			"(findings: 2)",
		} {
			if !strings.Contains(out, want) || strings.Contains(out, "needs ,") {
				t.Errorf("chain %s -> %s -> %s: output lacks %q or names an empty need:\n%s", gate, mid, last, want, out)
			}
		}
	}
}

// needsChain is a pull_request workflow of n jobs j00 to j<n-1>, each needing the job after it
// by name, so a check visiting jobs by name meets every job before its need. The last job begins
// with the draft step; every other one runs its work without it.
func needsChain(n int) string {
	var b strings.Builder
	b.WriteString(readyTypes)
	for i := range n {
		fmt.Fprintf(&b, "  j%02d:\n", i)
		if i < n-1 {
			fmt.Fprintf(&b, "    needs: j%02d\n    runs-on: x\n    steps:\n      - run: make\n", i+1)
			continue
		}
		b.WriteString(draftStepJob)
	}
	return b.String()
}

// Boundary: a needs chain as deep as the jobs one document may hold (ghworkflow.MaxJobsPerFile)
// is decided in full, every job naming the need that holds it back. Negative: one job more is
// refused rather than judged in part, which fails the check.
func TestAuditWorkflowTriggers_NeedsChainAtTheJobBound(t *testing.T) {
	out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": needsChain(maxJobsPerFile)}))
	for _, want := range []string{
		"pull_request job j00 may be skipped on a draft because it needs j01, which is held back on one",
		fmt.Sprintf("pull_request job j%02d may be skipped on a draft because it needs j%02d,", maxJobsPerFile-2, maxJobsPerFile-1),
		fmt.Sprintf("(findings: %d)", maxJobsPerFile-1),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("chain of %d jobs: output lacks %q:\n%s", maxJobsPerFile, want, out)
		}
	}
	root := triggerRepository(t, map[string]string{"x.yml": needsChain(maxJobsPerFile + 1)})
	_, err := AuditWorkflowTriggers(t.Context(), WorkflowTriggerOptions{Root: root, Today: triggersToday})
	if want := fmt.Sprintf("[FAIL] Workflow triggers (HISS-18): cannot read the workflows: workflow x.yml: workflow exceeds %d jobs",
		maxJobsPerFile); err == nil || err.Error() != want {
		t.Errorf("chain of %d jobs: error %v, want %q", maxJobsPerFile+1, err, want)
	}
}

// Negative: jobs whose needs form a cycle, or lead into one, cannot be ordered, so the check
// reports them in one finding on the line of the first of them, naming each, and decides none of
// them; a job outside the cycle is still judged. A local reusable workflow with a cycle is not
// shown to stop a draft, so its caller is reported.
func TestAuditWorkflowTriggers_ReportsNeedsCycle(t *testing.T) {
	cycle := readyTypes + "  build:\n    needs: test\n" + draftStepJob + "  test:\n    needs: [build]\n" + draftStepJob +
		"  deploy:\n    needs: test\n" + draftStepJob + "  lint:\n" + draftStepJob
	out := auditTriggers(t, triggerRepository(t, map[string]string{"x.yml": cycle}))
	want := "[WARN] Workflow triggers (HISS-18): .github/workflows/x.yml:5: pull_request jobs build, deploy, test form or " +
		"follow a needs cycle, so no order runs them and the check judges none of them on a draft; break the cycle."
	if !strings.Contains(out, want) || !strings.Contains(out, "(findings: 1)") {
		t.Errorf("cycle: output lacks %q as its one finding:\n%s", want, out)
	}
	caller := readyTypes + "  call:\n    uses: ./.github/workflows/reusable.yml\n"
	callee := "on: workflow_call\njobs:\n  a:\n    needs: b\n" + draftStepJob + "  b:\n    needs: a\n" + draftStepJob
	out = auditTriggers(t, triggerRepository(t, map[string]string{"caller.yml": caller, "reusable.yml": callee}))
	if !strings.Contains(out, ".github/workflows/caller.yml:5: pull_request job call runs on a draft pull request: it calls") {
		t.Errorf("a callee with a needs cycle: the caller is not reported:\n%s", out)
	}
}

// A local reusable workflow is judged with the needs inside it, in needs order. Negative: a chain
// of callee jobs behind a stopping job, each named before its need, is skipped on a draft, so the
// caller is reported. Positive: in a callee whose chain leads to a job a pull_request run never
// starts, the plain job behind it is skipped on every pull request and the always() job after
// that begins with the draft step, so the callee stops the draft; and a callee none of whose jobs
// starts on a pull request runs nothing on a draft, so its caller passes too.
func TestAuditWorkflowTriggers_CalleeNeedsChain(t *testing.T) {
	caller := readyTypes + "  call:\n    uses: ./.github/workflows/reusable.yml\n"
	push := "    if: github.event_name == 'push'\n    runs-on: x\n    steps:\n      - run: make publish\n"
	held := "on: workflow_call\njobs:\n  a:\n    needs: b\n" + draftStepJob + "  b:\n    needs: c\n" + draftStepJob + "  c:\n" + draftStepJob
	out := auditTriggers(t, triggerRepository(t, map[string]string{"caller.yml": caller, "reusable.yml": held}))
	if !strings.Contains(out, ".github/workflows/caller.yml:5: pull_request job call runs on a draft pull request: it calls") {
		t.Errorf("a callee chain behind a stopping job: the caller is not reported:\n%s", out)
	}
	for name, callee := range map[string]string{
		"unreached chain": "on: workflow_call\njobs:\n  gate:\n    needs: y\n    if: always()\n" + draftStepJob +
			"  y:\n    needs: z\n" + draftStepJob + "  z:\n" + push,
		"idle callee": "on: workflow_call\njobs:\n  b:\n    needs: a\n" + push + "  a:\n" + push,
	} {
		out := auditTriggers(t, triggerRepository(t, map[string]string{"caller.yml": caller, "reusable.yml": callee}))
		if !strings.HasPrefix(out, "[PASS]") {
			t.Errorf("%s: want a pass:\n%s", name, out)
		}
	}
}
