package forge

import (
	"slices"
	"strings"
	"testing"
)

// allsGreenUses pins re-actors/alls-green by a full commit SHA, as a step the file can judge names it.
const allsGreenUses = "re-actors/alls-green@05ac9388f0aebcb5727afa17fcccfecd6f8ec5fe # v1.2.2"

// allsGreenSteps is a merge gate's step list that runs the action with the inputs in with, one
// indented YAML line each.
func allsGreenSteps(uses string, with ...string) string {
	steps := "    steps:\n      - uses: " + uses + "\n"
	if len(with) == 0 {
		return steps
	}
	steps += "        with:\n"
	for _, input := range with {
		steps += "          " + input + "\n"
	}
	return steps
}

const (
	everyNeedInput  = "jobs: ${{ toJSON(needs) }}"
	laneSkipsInput  = "allowed-skips: go, test"
	plannerMayFail  = "allowed-failures: impact-plan"
	gateOnlyContext = "Merge gate"
)

// Positive (#76): a pinned alls-green step over toJSON(needs) fails its gate on every failed or
// cancelled need, so the gate is the only required check. A lane the planner skipped and the
// gate allows to be skipped leaves the pull request mergeable; a failed or cancelled need blocks it.
func TestAllsGreenAggregate_Positive_IsTheOnlyRequiredCheck(t *testing.T) {
	workflow := skippedLaneWorkflow(allNeeds, allsGreenSteps(allsGreenUses, everyNeedInput, laneSkipsInput))
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", workflow)
	required, err := RequiredStatusContexts(t.Context(), root)
	if err != nil || !slices.Equal(required, []string{gateOnlyContext}) {
		t.Fatalf("RequiredStatusContexts = %v, %v; want the gate alone", required, err)
	}
	skipped := map[string]string{"impact-plan": "success", "go": "skipped", "test": "skipped"}
	reported := map[string]string{"CI impact plan": "success", "Go lane": "skipped", gateOnlyContext: gateConclusion(t, workflow, skipped)}
	if !mergeable(required, reported) || mergeable(leafContexts, reported) {
		t.Fatalf("a skipped lane must merge under %v and not under the leaves, reported %v", required, reported)
	}
	for _, results := range []map[string]string{
		{"impact-plan": "failure", "go": "skipped", "test": "skipped"},
		{"impact-plan": "success", "go": "cancelled", "test": "skipped"},
		{"impact-plan": "success", "go": "success", "test": "failure"},
	} {
		if conclusion := gateConclusion(t, workflow, results); conclusion != "failure" {
			t.Errorf("needs %v: the gate reported %s, want failure", results, conclusion)
		}
	}
	strict := skippedLaneWorkflow(allNeeds, allsGreenSteps(allsGreenUses, everyNeedInput))
	if conclusion := gateConclusion(t, strict, skipped); conclusion != "failure" {
		t.Fatalf("without allowed-skips the action rejects a skipped lane: the gate reported %s", conclusion)
	}
}

// Negative: an alls-green step the file cannot judge proves nothing, so the gate stays required
// beside the jobs it needs, as before: an unpinned or foreign action, a jobs input that is not
// every need, a policy the file cannot settle, an input the action does not declare, a forgiven
// failure, a step condition the model cannot read, or a policy that lets every need fail.
func TestAllsGreenAggregate_Negative_UnjudgedStepKeepsTheLeaves(t *testing.T) {
	pinned := func(with ...string) string { return allsGreenSteps(allsGreenUses, with...) }
	gates := map[string]string{
		"short SHA":            allsGreenSteps("re-actors/alls-green@05ac938", everyNeedInput),
		"branch reference":     allsGreenSteps("re-actors/alls-green@release/v1", everyNeedInput),
		"other action":         allsGreenSteps("acme/alls-green@05ac9388f0aebcb5727afa17fcccfecd6f8ec5fe", everyNeedInput),
		"one need":             pinned("jobs: ${{ toJSON(needs.go) }}"),
		"jobs from an input":   pinned("jobs: ${{ inputs.jobs }}"),
		"no jobs input":        pinned(laneSkipsInput),
		"expression policy":    pinned(everyNeedInput, "allowed-failures: ${{ vars.FLAKY }}"),
		"JSON string policy":   pinned(everyNeedInput, `allowed-failures: '"go"'`),
		"JSON null policy":     pinned(everyNeedInput, "allowed-failures: 'null'"),
		"YAML list policy":     pinned(everyNeedInput, "allowed-failures: [go]"),
		"undeclared input":     pinned(everyNeedInput, "allowed-cancels: go"),
		"every need may fail":  pinned(everyNeedInput, "allowed-failures: impact-plan, go, test"),
		"forgiven failure":     strings.Replace(pinned(everyNeedInput), "        with:", "        continue-on-error: true\n        with:", 1),
		"step status function": strings.Replace(pinned(everyNeedInput), "      - uses:", "      - if: always()\n        uses:", 1),
	}
	for name, gate := range gates {
		t.Run(name, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, gate)))
			if err != nil || !slices.Equal(contexts, leafContexts) {
				t.Fatalf("contexts = %v, %v; want the leaves %v", contexts, err, leafContexts)
			}
		})
	}
}

// Boundary: the spellings the action reads alike prove the gate, and a need the policy allows to
// fail is not covered. The planner allowed to fail stays required beside the gate, the lanes the
// gate still fails on do not, and a failed planner passes the gate.
func TestAllsGreenAggregate_Boundary_SpellingsAndAllowedFailures(t *testing.T) {
	proving := map[string]string{
		"JSON list":          allsGreenSteps(allsGreenUses, everyNeedInput, `allowed-skips: '["go", "test"]'`),
		"empty JSON list":    allsGreenSteps(allsGreenUses, everyNeedInput, "allowed-failures: '[]'", laneSkipsInput),
		"blank names":        allsGreenSteps(allsGreenUses, everyNeedInput, "allowed-failures: ' , '"),
		"case and spacing":   allsGreenSteps("Re-Actors/Alls-Green@05ac9388f0aebcb5727afa17fcccfecd6f8ec5fe", "jobs: ${{ tojson( needs ) }}"),
		"failure of a stray": allsGreenSteps(allsGreenUses, everyNeedInput, "allowed-failures: docs"),
	}
	for name, gate := range proving {
		t.Run(name, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, gate)))
			if err != nil || !slices.Equal(contexts, []string{gateOnlyContext}) {
				t.Fatalf("contexts = %v, %v; want the gate alone", contexts, err)
			}
		})
	}
	workflow := skippedLaneWorkflow(allNeeds, allsGreenSteps(allsGreenUses, everyNeedInput, plannerMayFail))
	contexts, err := workflowPullRequestContexts([]byte(workflow))
	if want := []string{"CI impact plan", gateOnlyContext}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("a need allowed to fail must stay required: %v, %v; want %v", contexts, err, want)
	}
	if conclusion := gateConclusion(t, workflow, map[string]string{"impact-plan": "failure", "go": "success", "test": "success"}); conclusion != "success" {
		t.Fatalf("the action accepts the planner's failure: the gate reported %s", conclusion)
	}
	tooMany := strings.Repeat("a,", maxJobsPerFile) + "a"
	if _, read := allsGreenNames(tooMany); read {
		t.Fatalf("a list past %d names must not be read", maxJobsPerFile)
	}
	if names, read := allsGreenNames(strings.Repeat("a,", maxJobsPerFile-1) + "a"); !read || len(names) != maxJobsPerFile {
		t.Fatalf("a list of %d names must be read: %v, %v", maxJobsPerFile, names, read)
	}
}

// Positive, negative and boundary statuses of the command that ends a failing run: step.
func TestFailingExit(t *testing.T) {
	cases := map[string]bool{
		"exit 1": true, "exit 255": true, "exit\t2": true,
		"exit 0": false, "exit 256": false, "exit -1": false, "exit": false, "exit x": false,
		"exit 1 2": false, "exit 1 # done": false, "echo exit 1": false, "exit $?": false,
	}
	for command, want := range cases {
		if got := failingExit(command); got != want {
			t.Errorf("failingExit(%q) = %v, want %v", command, got, want)
		}
	}
}
