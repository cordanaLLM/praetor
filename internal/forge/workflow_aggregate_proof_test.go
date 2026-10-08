package forge

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

// provenGateSteps is the step list an aggregate fails with when a job it needs failed or was
// cancelled, a shape provenAggregateNeeds recognises.
const provenGateSteps = "    steps:\n      - name: Fail when a needed job failed or was cancelled\n" +
	"        if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')\n" +
	"        run: |\n          echo \"::error::a needed job failed or was cancelled\"\n          exit 1\n"

// provenGateScript is the run: script of provenGateSteps, which the negative cases replace.
const provenGateScript = "          echo \"::error::a needed job failed or was cancelled\"\n          exit 1\n"

// renovateSkipCondition is renovateBranchSkip as a job condition spells it in YAML.
const renovateSkipCondition = "\"!(startsWith(github.head_ref, 'renovate/') && github.event.pull_request.user.login == 'renovate[bot]')"

// skippedLaneJobs is path-filtered CI on a solo-maintainer repository without its merge gate: a
// planner selects the Go lane, and an unconditional matrix job needs that lane. GitHub skips the
// matrix job whenever it skips the lane, and a skipped matrix job reports none of its per-leg
// checks.
const skippedLaneJobs = "on:\n  pull_request:\n  push:\n    branches: [main]\njobs:\n" +
	"  impact-plan:\n    name: CI impact plan\n    runs-on: ubuntu-latest\n" +
	"  go:\n    name: Go lane\n    needs: impact-plan\n    if: needs.impact-plan.outputs.go == 'true'\n    runs-on: ubuntu-latest\n" +
	"  test:\n    name: Test (${{ matrix.os }})\n    needs: go\n    runs-on: ${{ matrix.os }}\n" +
	"    strategy:\n      matrix:\n        os: [ubuntu-latest, macos-latest]\n"

// skippedLaneWorkflow is skippedLaneJobs with a merge gate that needs needs, runs on every run
// and carries steps.
func skippedLaneWorkflow(needs, steps string) string {
	return skippedLaneJobs + "  merge-gate:\n    name: Merge gate\n    needs: " + needs + "\n    if: always()\n    runs-on: ubuntu-latest\n" + steps
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
	job := spec.Jobs["merge-gate"]
	steps, shaped := gateSteps(&spec, &job)
	if !shaped {
		t.Fatal("the gate's steps are not in an allowed shape")
	}
	fails, known := aggregateFails(steps, results)
	if !known {
		t.Fatalf("the gate's steps cannot be evaluated against %v", results)
	}
	if fails {
		return "failure"
	}
	return "success"
}

// keepsEveryLeaf fails t unless contexts, the required checks of a skippedLaneWorkflow variant,
// still hold the planner and both matrix legs: no aggregate covered them.
func keepsEveryLeaf(t *testing.T, contexts []string, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	for _, leaf := range []string{"CI impact plan", "Test (ubuntu-latest)", "Test (macos-latest)"} {
		if !slices.Contains(contexts, leaf) {
			t.Fatalf("contexts = %v; the unproven gate must leave %q required", contexts, leaf)
		}
	}
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

// documentedGateJobs returns the merge-gate jobs docs/adoption.md shows adopters, each a YAML
// fence that starts with the merge-gate: key.
func documentedGateJobs(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "docs", "adoption.md"))
	if err != nil {
		t.Fatal(err)
	}
	// A Windows checkout under core.autocrlf holds the document in CRLF.
	text, _ := util.NormalizeLineEndings(string(data))
	var jobs []string
	for _, fence := range strings.Split(text, "```yaml\n")[1:] {
		body, _, closed := strings.Cut(fence, "```")
		if closed && strings.HasPrefix(body, "merge-gate:\n") {
			jobs = append(jobs, "  "+strings.ReplaceAll(strings.TrimSuffix(body, "\n"), "\n", "\n  ")+"\n")
		}
	}
	return jobs
}

// Positive (#76): both aggregate shapes docs/adoption.md tells adopters to write, the exit step
// and the pinned alls-green step, narrow the ruleset to the gate in skippedLaneJobs, and a skipped
// lane merges under it.
func TestRequiredStatusContexts_Positive_DocumentedAggregatesNarrowToTheGate(t *testing.T) {
	jobs := documentedGateJobs(t)
	if len(jobs) != 2 {
		t.Fatalf("docs/adoption.md shows %d merge-gate jobs, want the exit-step and the alls-green shape", len(jobs))
	}
	skipped := map[string]string{"impact-plan": "success", "go": "skipped", "test": "skipped"}
	for _, job := range jobs {
		workflow := skippedLaneJobs + job
		contexts, err := workflowPullRequestContexts([]byte(workflow))
		if err != nil || !slices.Equal(contexts, []string{"Merge gate"}) {
			t.Fatalf("documented gate\n%s\ncontexts = %v, %v; want the gate alone", job, contexts, err)
		}
		if conclusion := gateConclusion(t, workflow, skipped); conclusion != "success" {
			t.Fatalf("documented gate\n%s\nfails a skipped lane: %s", job, conclusion)
		}
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
// read, a script whose exit never runs or whose status is discarded, a step that changes what the
// exit step runs) or block every pull request (a step that always fails).
func TestRequiredStatusContexts_Negative_UnprovenAggregateKeepsTheLeaves(t *testing.T) {
	script := func(lines string) string { return strings.Replace(provenGateSteps, provenGateScript, lines, 1) }
	steps := map[string]string{
		"no steps":           "",
		"echo only":          "    steps:\n      - run: echo done\n",
		"always fails":       "    steps:\n      - run: exit 1\n",
		"failure only":       "    steps:\n      - if: contains(needs.*.result, 'failure')\n        run: exit 1\n",
		"forgiven failure":   strings.Replace(provenGateSteps, "        run: |", "        continue-on-error: true\n        run: |", 1),
		"status conjunction": strings.Replace(provenGateSteps, "if: contains", "if: always() && contains", 1),
		"unknown job":        strings.Replace(provenGateSteps, "if: contains", "if: needs.lint.result == 'failure' || contains", 1),
		"earlier exit":       script("          if true; then exit 0; fi\n          exit 1\n"),
		"exit zero":          strings.Replace(provenGateSteps, "exit 1", "exit 0", 1),
		"action step":        "    steps:\n      - uses: re-actors/alls-green@release/v1\n        with:\n          jobs: ${{ toJSON(needs) }}\n",
		"unclosed expression": strings.Replace(provenGateSteps, "if: contains(needs.*.result, 'failure')",
			"if: ${{ contains(needs.*.result, 'failure')", 1),
		"line continuation": script("          echo a needed job failed \\\n          exit 1\n"),
		"here-document":     script("          echo <<'EOF'\n          exit 1\n"),
		"exit trap":         script("          trap 'exit 0' EXIT\n          exit 1\n"),
		"exec":              script("          exec true\n          exit 1\n"),
		"open quote":        script("          echo \"a needed job failed\n          exit 1\n"),
		"exit then more":    script("          exit 1\n          echo unreachable\n"),
		"custom template":   strings.Replace(provenGateSteps, "        run: |", "        shell: sh -c true {0}\n        run: |", 1),
		"plain template":    strings.Replace(provenGateSteps, "        run: |", "        shell: bash {0}\n        run: |", 1),
		"shell expression":  strings.Replace(provenGateSteps, "        run: |", "        shell: ${{ matrix.shell }}\n        run: |", 1),
		"job template":      "    defaults:\n      run:\n        shell: perl {0}\n" + provenGateSteps,
		"step env":          strings.Replace(provenGateSteps, "        run: |", "        env:\n          BASH_ENV: /tmp/exit-zero\n        run: |", 1),
		"earlier step": strings.Replace(provenGateSteps, "    steps:\n",
			"    steps:\n      - run: echo \"BASH_ENV=/tmp/exit-zero\" >> \"$GITHUB_ENV\"\n", 1),
	}
	for name, gate := range steps {
		t.Run(name, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, gate)))
			if err != nil || !slices.Equal(contexts, leafContexts) {
				t.Fatalf("contexts = %v, %v; want the leaves %v", contexts, err, leafContexts)
			}
		})
	}
	workflowDefault := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "jobs:\n", "defaults:\n  run:\n    shell: sh -c true {0}\njobs:\n", 1)
	contexts, err := workflowPullRequestContexts([]byte(workflowDefault))
	if err != nil || !slices.Equal(contexts, leafContexts) {
		t.Fatalf("a workflow default template proves nothing: %v, %v; want %v", contexts, err, leafContexts)
	}
	advisory := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "    if: always()\n", "    if: always()\n    continue-on-error: true\n", 1)
	contexts, err = workflowPullRequestContexts([]byte(advisory))
	if want := []string{"CI impact plan", "Test (ubuntu-latest)", "Test (macos-latest)"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("an advisory gate proves nothing and is not required: %v, %v; want %v", contexts, err, want)
	}
}

// Negative: the every-run guard. A gate covers its needs only under always(): GitHub skips a gate
// with no condition, with success() or with a conditional one when a need failed, and a skipped
// gate passes a required check. `!cancelled()` and `success() || failure()` do not run on a
// cancelled run, which may report the gate skipped. Each keeps the leaves, though its steps would
// prove it.
func TestRequiredStatusContexts_Negative_GateConditionMustBeAlways(t *testing.T) {
	conditions := map[string]string{
		"no condition":   "",
		"success":        "    if: success()\n",
		"conditional":    "    if: github.event_name == 'pull_request'\n",
		"always and":     "    if: always() && github.event_name == 'pull_request'\n",
		"not cancelled":  "    if: '!cancelled()'\n",
		"either outcome": "    if: success() || failure()\n",
		"open guard":     "    if: ${{ always()\n",
	}
	for name, condition := range conditions {
		t.Run(name, func(t *testing.T) {
			workflow := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "    if: always()\n", condition, 1)
			contexts, err := workflowPullRequestContexts([]byte(workflow))
			keepsEveryLeaf(t, contexts, err)
		})
	}
}

// Negative: a gate with the Renovate skip is skipped on a Renovate pull request, and a skipped
// required check passes. Jobs it needs that run there anyway stay required, so a failure of one
// cannot merge.
//
// Boundary: a need that carries the same skip does not run on that pull request either, so the
// gate covers it, while a need without the skip stays required beside the gate.
func TestRequiredStatusContexts_RenovateSkippedGateCoversOnlySkippedNeeds(t *testing.T) {
	skippedGate := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "    if: always()\n",
		"    if: "+renovateSkipCondition+" && always()\"\n", 1)
	contexts, err := workflowPullRequestContexts([]byte(skippedGate))
	if err != nil || !slices.Equal(contexts, leafContexts) {
		t.Fatalf("a Renovate-skipped gate must keep needs that run on Renovate pull requests: %v, %v; want %v", contexts, err, leafContexts)
	}
	sameSkip := strings.Replace(skippedGate, "    if: needs.impact-plan.outputs.go == 'true'\n",
		"    if: "+renovateSkipCondition+" && needs.impact-plan.outputs.go == 'true'\"\n", 1)
	sameSkip = strings.Replace(sameSkip, "    needs: go\n", "    needs: go\n    if: "+renovateSkipCondition+"\"\n", 1)
	contexts, err = workflowPullRequestContexts([]byte(sameSkip))
	if want := []string{"CI impact plan", "Merge gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("a Renovate-skipped gate covers the needs with the same skip: %v, %v; want %v", contexts, err, want)
	}
}

// Boundary: every recognised spelling of the gate's condition, its exit step's condition, script
// and shell proves the gate.
func TestRequiredStatusContexts_Boundary_AggregateSpellings(t *testing.T) {
	script := func(lines string) string { return strings.Replace(provenGateSteps, provenGateScript, lines, 1) }
	proving := map[string]string{
		"one expression": strings.Replace(provenGateSteps, "if: contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled')",
			"if: ${{ contains(needs.*.result, 'failure') || contains(needs.*.result, 'cancelled') }}", 1),
		"case of the literal": strings.Replace(strings.Replace(provenGateSteps, "'failure'", "'FAILURE'", 1), "'cancelled'", "'Cancelled'", 1),
		"per-need terms": "    steps:\n      - if: needs.impact-plan.result != 'success' || needs.go.result == 'failure' || " +
			"needs.go.result == 'cancelled' || needs.test.result == 'failure' || needs.test.result == 'cancelled'\n        run: exit 1\n",
		"highest status":   strings.Replace(provenGateSteps, "exit 1", "exit 255", 1),
		"messages":         script("          printf 'gate failed'\n          Write-Host \"a need failed\"\n          Write-Output done\n          exit 1\n"),
		"comments":         script("          # a needed job failed\n\n          exit 1\n          # nothing runs after the exit\n"),
		"pwsh":             strings.Replace(provenGateSteps, "        run: |", "        shell: pwsh\n        run: |", 1),
		"cmd":              strings.Replace(provenGateSteps, "        run: |", "        shell: cmd\n        run: |", 1),
		"step over job":    "    defaults:\n      run:\n        shell: perl {0}\n" + strings.Replace(provenGateSteps, "        run: |", "        shell: bash\n        run: |", 1),
		"keyword defaults": "    defaults:\n      run:\n        shell: sh\n" + provenGateSteps,
	}
	for name, gate := range proving {
		t.Run(name, func(t *testing.T) {
			contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, gate)))
			if err != nil || !slices.Equal(contexts, []string{"Merge gate"}) {
				t.Fatalf("contexts = %v, %v; want the gate alone", contexts, err)
			}
		})
	}
	expression := strings.Replace(skippedLaneWorkflow(allNeeds, provenGateSteps), "    if: always()\n", "    if: ${{ always( ) }}\n", 1)
	if contexts, err := workflowPullRequestContexts([]byte(expression)); err != nil || !slices.Equal(contexts, []string{"Merge gate"}) {
		t.Fatalf("always() as one expression proves the gate: %v, %v", contexts, err)
	}
}

// Boundary: the proof covers only the jobs the gate needs directly. A planner the gate reaches
// only through a lane stays required, because its failure skips the lane and a skipped need does
// not fail the gate. A need whose failure the gate's condition does not test stays required too.
// A proven inner gate that an outer gate needs is covered like any other need. An exit status
// past 255 is no failing exit.
func TestRequiredStatusContexts_Boundary_AggregateReach(t *testing.T) {
	if contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, strings.Replace(provenGateSteps, "exit 1", "exit 256", 1)))); err != nil || !slices.Equal(contexts, leafContexts) {
		t.Fatalf("exit 256 is not a failing status: %v, %v", contexts, err)
	}
	contexts, err := workflowPullRequestContexts([]byte(skippedLaneWorkflow("[go, test]", provenGateSteps)))
	if want := []string{"CI impact plan", "Merge gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("a planner the gate needs only through a lane must stay required: %v, %v; want %v", contexts, err, want)
	}
	partial := "    steps:\n      - if: needs.go.result != 'success' || needs.test.result != 'success'\n        run: exit 1\n"
	contexts, err = workflowPullRequestContexts([]byte(skippedLaneWorkflow(allNeeds, partial)))
	if want := []string{"CI impact plan", "Merge gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("a gate covers only the needs whose failure it proves fails it: %v, %v; want %v", contexts, err, want)
	}
	nested := skippedLaneWorkflow(allNeeds, provenGateSteps) +
		"  release-gate:\n    name: Release gate\n    needs: merge-gate\n    if: always()\n" + provenGateSteps
	contexts, err = workflowPullRequestContexts([]byte(nested))
	if want := []string{"Release gate"}; err != nil || !slices.Equal(contexts, want) {
		t.Fatalf("an outer gate covers a proven inner gate: %v, %v; want %v", contexts, err, want)
	}
}

// Positive, negative and boundary: gateScript accepts message lines, then one failing exit, then
// blank and comment lines, each line plain, within maxScriptLines lines, and nothing else.
func TestGateScript(t *testing.T) {
	cases := map[string]bool{
		"exit 1":                           true,
		"exit 1\n":                         true,
		"echo \"don't merge\"\nexit 2\n":   true,
		"echo 'say \"no\"'\nexit 3\n":      true,
		"exit 1\n# done\n\n":               true,
		"":                                 false,
		"echo failed\n":                    false,
		"echo 'unclosed\nexit 1\n":         false,
		"echo $RESULTS\nexit 1\n":          false,
		"echo `date`\nexit 1\n":            false,
		"echo a ^\nexit 1\n":               false,
		"echo a; exit 0\nexit 1\n":         false,
		"echo a & exit 0\nexit 1\n":        false,
		"echo (a)\nexit 1\n":               false,
		"echo @'\nexit 1\n":                false,
		"echo %ERRORLEVEL%\nexit 1\n":      false,
		"echo é\nexit 1\n":                 false,
		"set +e\nexit 1\n":                 false,
		"function exit { :; }\nexit 1\n":   false,
		"exit 1\nexit 2\n":                 false,
		"ECHO done\nexit 1\n":              false,
		"echo done\n  exit\t7  \n":         true,
		"\techo done\nexit 01\n":           false,
		"echo done\nexit 1 # comment\n":    false,
		"Write-Host done\nexit 1\n# end\n": true,
	}
	for script, want := range cases {
		if got := gateScript(script); got != want {
			t.Errorf("gateScript(%q) = %v, want %v", script, got, want)
		}
	}
	bounded := strings.Repeat("echo a\n", maxScriptLines-2) + "exit 1\n"
	if !gateScript(bounded) {
		t.Fatalf("a script of %d lines must be read", maxScriptLines)
	}
	if gateScript("echo a\n" + bounded) {
		t.Fatalf("a script past %d lines must not be read", maxScriptLines)
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
