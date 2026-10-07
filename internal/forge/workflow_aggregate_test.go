package forge

import (
	"slices"
	"testing"
)

// aggregateWorkflow is path-filtered CI: a planner job selects lanes, the lane runs only when
// selected, and an aggregate job that needs both carries gateCondition. The aggregate has no
// steps, so nothing in the file proves it fails when a lane fails (provenAggregate).
func aggregateWorkflow(gateCondition string) string {
	return "on:\n  pull_request:\n  push:\n    branches: [main]\njobs:\n" +
		"  impact-plan:\n    name: CI impact plan\n    runs-on: ubuntu-latest\n" +
		"  go:\n    name: Go lane\n    needs: impact-plan\n    if: needs.impact-plan.outputs.go == 'true'\n" +
		"  merge-gate:\n    name: Merge gate\n    needs: [impact-plan, go]\n    if: " + gateCondition + "\n"
}

// Positive: an aggregate merge gate with if: always() reports on every pull request, so it is
// required. Its steps do not prove it fails when a lane fails, so the unconditional planner stays
// required beside it; the lane the planner gates never is.
func TestRequiredStatusContexts_Positive_AlwaysAggregateIsRequired(t *testing.T) {
	root := t.TempDir()
	writeWorkflowFixture(t, root, "ci.yml", aggregateWorkflow("always()"))
	contexts, err := RequiredStatusContexts(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"CI impact plan", "Merge gate"}; !slices.Equal(contexts, want) {
		t.Fatalf("contexts = %v, want %v", contexts, want)
	}
}

// Negative: a condition that can skip the job on some pull requests keeps it out of the ruleset.
// GitHub reports a job its condition skipped as successful, so requiring one would pass the pull
// request whether or not the work ran. A status function joined with anything else is such a
// condition, and so is a status function that does not hold on every run.
func TestRequiredStatusContexts_Negative_ConditionalAggregateIsNotRequired(t *testing.T) {
	for _, condition := range []string{
		"needs.impact-plan.outputs.go == 'true'",
		"always() && needs.impact-plan.outputs.go == 'true'",
		"${{ always() && github.event_name == 'push' }}",
		"always() || github.event_name == 'push'",
		"failure()",
		"cancelled()",
		"success() || cancelled()",
		"'${{ always() }} && true'",
		"'${{ always()'",
	} {
		t.Run(condition, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(aggregateWorkflow(condition)))
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"CI impact plan"}; !slices.Equal(contexts, want) {
				t.Fatalf("condition %q: contexts = %v, want %v", condition, contexts, want)
			}
		})
	}
}

// Boundary: every spelling of a condition that holds on every run, bare, as one ${{ }}
// expression, quoted and with any spacing, is required alike. The rule stays inside the other
// two: a paths-filtered workflow reports on no pull request unconditionally, and an advisory
// always() job reports success whatever happened.
func TestRequiredStatusContexts_Boundary_EveryRunSpellings(t *testing.T) {
	for _, condition := range []string{
		"${{ always() }}", "${{always()}}", "\"always()\"", "  always( )  ",
		"success() || failure()", "failure()||success()", "${{ success() || failure() }}",
		"${{ !cancelled() }}", "\"!cancelled()\"",
	} {
		contexts, err := workflowPullRequestContexts([]byte(aggregateWorkflow(condition)))
		if err != nil || !slices.Contains(contexts, "Merge gate") {
			t.Errorf("condition %q: contexts = %v, err = %v, want the merge gate required", condition, contexts, err)
		}
	}
	filtered := "on:\n  pull_request:\n    paths: ['src/**']\njobs:\n  gate:\n    name: Merge gate\n    if: always()\n"
	if contexts, err := workflowPullRequestContexts([]byte(filtered)); err != nil || len(contexts) != 0 {
		t.Fatalf("a paths-filtered workflow must require nothing: %v, %v", contexts, err)
	}
	advisory := "on: pull_request\njobs:\n  gate:\n    name: Merge gate\n    if: always()\n    continue-on-error: true\n"
	if contexts, err := workflowPullRequestContexts([]byte(advisory)); err != nil || len(contexts) != 0 {
		t.Fatalf("an advisory always() job must not be required: %v, %v", contexts, err)
	}
}

func TestHoldsOnEveryRun(t *testing.T) {
	cases := []struct {
		condition string
		want      bool
	}{
		{"always()", true},
		{"${{ always() }}", true},
		{" ${{ failure() || success() }} ", true},
		{"!cancelled()", true},
		{"", false},
		{"${{ }}", false},
		{"${{", false},
		{"}}", false},
		{"always", false},
		{"!always()", false},
		{"success()", false},
		{"always() && true", false},
		{"${{ always() }} || ${{ true }}", false},
	}
	for _, tc := range cases {
		if got := holdsOnEveryRun(tc.condition); got != tc.want {
			t.Errorf("holdsOnEveryRun(%q) = %v, want %v", tc.condition, got, tc.want)
		}
	}
}
