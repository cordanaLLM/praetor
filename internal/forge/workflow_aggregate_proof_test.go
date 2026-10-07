package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// provenGateSteps is the step list an aggregate fails with when a job it needs failed or was
// cancelled, the shape provenAggregate recognises.
const provenGateSteps = "    steps:\n      - name: Fail when a needed job failed or was cancelled\n" +
	"        if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')\n" +
	"        run: |\n          echo \"::error::a needed job failed or was cancelled\"\n          exit 1\n"

// skippedLaneWorkflow is path-filtered CI on a solo-maintainer repository: a planner selects the
// Go lane, an unconditional matrix job needs that lane, and a merge gate that runs on every run
// needs all three and carries steps. GitHub skips the matrix job whenever it skips the lane, and
// a skipped matrix job reports none of its per-leg checks.
func skippedLaneWorkflow(needs, steps string) string {
	return "on:\n  pull_request:\n  push:\n    branches: [main]\njobs:\n" +
		"  impact-plan:\n    name: CI impact plan\n    runs-on: ubuntu-latest\n" +
		"  go:\n    name: Go lane\n    needs: impact-plan\n    if: needs.impact-plan.outputs.go == 'true'\n    runs-on: ubuntu-latest\n" +
		"  test:\n    name: Test (${{ matrix.os }})\n    needs: go\n    runs-on: ${{ matrix.os }}\n" +
		"    strategy:\n      matrix:\n        os: [ubuntu-latest, macos-latest]\n" +
		"  merge-gate:\n    name: Merge gate\n    needs: " + needs + "\n    if: always()\n    runs-on: ubuntu-latest\n" + steps
}

const allNeeds = "[impact-plan, go, test]"

// leafContexts are the required checks of skippedLaneWorkflow when no aggregate is proven: the
// planner, the gate and both matrix legs.
var leafContexts = []string{"CI impact plan", "Merge gate", "Test (ubuntu-latest)", "Test (macos-latest)"}

// mergeable reports whether a pull request whose checks reported the conclusions in reported
// satisfies a ruleset requiring required: every required context reported, as success or as a
// job its condition skipped. A context no run reports leaves the pull request waiting forever.
func mergeable(required []string, reported map[string]string) bool {
	for _, context := range required {
		if conclusion := reported[context]; conclusion != "success" && conclusion != "skipped" {
			return false
		}
	}
	return true
}

// gateConclusion is the conclusion the merge gate of workflow reports when its needs report
// results, as aggregateFails evaluates its steps.
func gateConclusion(t *testing.T, workflow string, results map[string]string) string {
	t.Helper()
	spec, err := ghworkflow.Parse([]byte(workflow))
	if err != nil {
		t.Fatal(err)
	}
	fails, known := aggregateFails(spec.Jobs["merge-gate"].Steps, results)
	if !known {
		t.Fatalf("the gate's steps cannot be evaluated against %v", results)
	}
	if fails {
		return "failure"
	}
	return "success"
}

// Positive (#76): a merge gate whose steps prove it fails on every failed or cancelled need is
// the only required check its workflow contributes. The planner and the matrix legs it covers are
// not, so the matrix legs a skipped lane never reports cannot hold the pull request. sync
// --remote and plan --remote (RequiredStatusContextsIn) and the documentation gate
// (RequiredStatusContextsOf) judge the same list as the local ruleset.
func TestRequiredStatusContexts_Positive_ProvenAggregateIsTheOnlyRequiredCheck(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", skippedLaneWorkflow(allNeeds, provenGateSteps))
	want := []string{"Merge gate"}
	local, err := RequiredStatusContexts(t.Context(), root)
	if err != nil || !slices.Equal(local, want) {
		t.Fatalf("RequiredStatusContexts = %v, %v; want %v", local, err, want)
	}
	remote, err := RequiredStatusContextsIn(t.Context(), root, "acme/widgets")
	if err != nil || !slices.Equal(remote, want) {
		t.Fatalf("RequiredStatusContextsIn = %v, %v; want %v", remote, err, want)
	}
	selected, err := RequiredStatusContextsOf(t.Context(), root, []string{".github/workflows/ci.yml"})
	if err != nil || !slices.Equal(selected, want) {
		t.Fatalf("RequiredStatusContextsOf = %v, %v; want %v", selected, err, want)
	}
}

// Positive: a pull request whose lane the planner skipped reports the planner, the skipped lane
// and the gate, and no matrix leg. It merges under the aggregate-only ruleset once the gate
// passes; under the leaf ruleset an unproven gate leaves, the legs keep it waiting forever.
//
// Negative: a leaf that fails or is cancelled fails the gate, so the aggregate-only ruleset
// blocks the pull request, whichever need it was, the skip it causes downstream included.
func TestProvenAggregate_SkippedLaneMergesAndFailedLeafBlocks(t *testing.T) {
	workflow := skippedLaneWorkflow(allNeeds, provenGateSteps)
	required, err := workflowPullRequestContexts([]byte(workflow))
	if err != nil {
		t.Fatal(err)
	}
	skipped := map[string]string{"impact-plan": "success", "go": "skipped", "test": "skipped"}
	reported := map[string]string{"CI impact plan": "success", "Go lane": "skipped",
		"Merge gate": gateConclusion(t, workflow, skipped)}
	if !mergeable(required, reported) {
		t.Fatalf("a skipped lane must leave the pull request mergeable under %v, reported %v", required, reported)
	}
	if mergeable(leafContexts, reported) {
		t.Fatal("fixture: the leaf ruleset must be unsatisfiable when the lane is skipped")
	}
	runs := []map[string]string{
		{"impact-plan": "success", "go": "failure", "test": "skipped"},
		{"impact-plan": "success", "go": "success", "test": "failure"},
		{"impact-plan": "failure", "go": "skipped", "test": "skipped"},
		{"impact-plan": "success", "go": "cancelled", "test": "skipped"},
		{"impact-plan": "success", "go": "success", "test": "cancelled"},
	}
	for _, results := range runs {
		conclusion := gateConclusion(t, workflow, results)
		if conclusion != "failure" || mergeable(required, map[string]string{"CI impact plan": "success", "Merge gate": conclusion}) {
			t.Errorf("needs %v: the gate reported %s and the pull request must not merge", results, conclusion)
		}
	}
}

// Negative: an always() gate whose steps do not prove it fails on every failed or cancelled need
// stays required beside the jobs it needs, as before. Requiring it alone would let a failed lane
// merge (an echo, a check of one result only, a forgiven failure, a condition the file cannot
// read) or block every pull request (a step that always fails).
func TestRequiredStatusContexts_Negative_UnprovenAggregateKeepsTheLeaves(t *testing.T) {
	steps := map[string]string{
		"no steps":           "",
		"echo only":          "    steps:\n      - run: echo done\n",
		"always fails":       "    steps:\n      - run: exit 1\n",
		"failure only":       "    steps:\n      - if: contains(needs.*.result, 'failure')\n        run: exit 1\n",
		"forgiven failure":   strings.Replace(provenGateSteps, "        run: |", "        continue-on-error: true\n        run: |", 1),
		"status conjunction": strings.Replace(provenGateSteps, "if: contains", "if: always() && contains", 1),
		"unknown job":        strings.Replace(provenGateSteps, "if: contains", "if: needs.lint.result == 'failure' || contains", 1),
		"earlier exit":       strings.Replace(provenGateSteps, "          echo", "          if true; then exit 0; fi\n          echo", 1),
		"exit zero":          strings.Replace(provenGateSteps, "exit 1", "exit 0", 1),
		"action step":        "    steps:\n      - uses: re-actors/alls-green@release/v1\n        with:\n          jobs: ${{ toJSON(needs) }}\n",
		"unclosed expression": strings.Replace(provenGateSteps, "if: contains(needs.*.result, 'failure')",
			"if: ${{ contains(needs.*.result, 'failure')", 1),
	}
	for name, gate := range steps {
		t.Run(name, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, gate)))
			if err != nil || !slices.Equal(contexts, leafContexts) {
				t.Fatalf("contexts = %v, %v; want the leaves %v", contexts, err, leafContexts)
			}
		})
	}
	advisory := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "    if: always()\n", "    if: always()\n    continue-on-error: true\n", 1)
	contexts, err := workflowPullRequestContexts([]byte(advisory))
	if want := []string{"CI impact plan", "Test (ubuntu-latest)", "Test (macos-latest)"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("an advisory gate proves nothing and is not required: %v, %v; want %v", contexts, err, want)
	}
}

// Boundary: every recognised spelling proves the gate, and the proof covers only the jobs the gate
// needs directly. A planner the gate reaches only through a lane stays required, because its
// failure skips the lane and a skipped need does not fail the gate. A proven inner gate that an
// outer gate needs is covered like any other need.
func TestRequiredStatusContexts_Boundary_AggregateSpellingsAndReach(t *testing.T) {
	proving := map[string]string{
		"one expression": strings.Replace(provenGateSteps, "if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')",
			"if: ${{ contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled') }}", 1),
		"case of the literal": strings.Replace(strings.Replace(provenGateSteps, "'failure'", "'FAILURE'", 1), "'cancelled'", "'Cancelled'", 1),
		"per-need terms": "    steps:\n      - if: needs.impact-plan.result != 'success' || needs.go.result == 'failure' || " +
			"needs.go.result == 'cancelled' || needs.test.result == 'failure' || needs.test.result == 'cancelled'\n        run: exit 1\n",
		"highest status": strings.Replace(provenGateSteps, "exit 1", "exit 255", 1),
		"renovate skip":  provenGateSteps,
	}
	for name, gate := range proving {
		t.Run(name, func(t *testing.T) {
			workflow := skippedLaneWorkflow(allNeeds, gate)
			if name == "renovate skip" {
				workflow = strings.Replace(workflow, "    if: always()\n", "    if: \"!(startsWith(github.head_ref, 'renovate/') && github.event.pull_request.user.login == 'renovate[bot]') && always()\"\n", 1)
			}
			contexts, err := workflowPullRequestContexts([]byte(workflow))
			if err != nil || !slices.Equal(contexts, []string{"Merge gate"}) {
				t.Fatalf("contexts = %v, %v; want the gate alone", contexts, err)
			}
		})
	}
	if contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, strings.Replace(provenGateSteps, "exit 1", "exit 256", 1)))); err != nil || !slices.Equal(contexts, leafContexts) {
		t.Fatalf("exit 256 is not a failing status: %v, %v", contexts, err)
	}
	contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow("[go, test]", provenGateSteps)))
	if want := []string{"CI impact plan", "Merge gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("a planner the gate needs only through a lane must stay required: %v, %v; want %v", contexts, err, want)
	}
	nested := skippedLaneWorkflow(allNeeds, provenGateSteps) +
		"  release-gate:\n    name: Release gate\n    needs: merge-gate\n    if: always()\n" + provenGateSteps
	contexts, err = workflowPullRequestContexts([]byte(nested))
	if want := []string{"Release gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("an outer gate covers a proven inner gate: %v, %v; want %v", contexts, err, want)
	}
}

func TestNeedsConditionHolds(t *testing.T) {
	results := map[string]string{"plan": "success", "go": "failure"}
	cases := []struct {
		condition    string
		holds, known bool
	}{
		{"", true, true},
		{"contains(needs.*.result, 'failure')", true, true},
		{"${{ contains(needs.*.result,'cancelled') }}", false, true},
		{"needs.go.result == 'Failure'", true, true},
		{"needs.plan.result != 'success'", false, true},
		{"needs.plan.result == 'cancelled' || needs.go.result != 'success'", true, true},
		{"needs.lint.result == 'failure'", false, false},
		{"always()", false, false},
		{"contains(needs.*.result, failure)", false, false},
		{"contains(needs.*.result, 'it''s')", false, false},
		{"needs.go.result", false, false},
		{"needs.go.result >= 'failure'", false, false},
		{"${{ contains(needs.*.result, 'failure')", false, false},
		{"!contains(needs.*.result, 'failure')", false, false},
	}
	for _, tc := range cases {
		holds, known := needsConditionHolds(tc.condition, results)
		if holds != tc.holds || known != tc.known {
			t.Errorf("needsConditionHolds(%q) = %v, %v; want %v, %v", tc.condition, holds, known, tc.holds, tc.known)
		}
	}
	long := strings.Repeat("contains(needs.*.result, 'failure') || ", maxJobsPerFile) + "contains(needs.*.result, 'failure')"
	if _, known := needsConditionHolds(long, results); known {
		t.Error("a condition past the term bound must not be read")
	}
}
