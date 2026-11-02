// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
	"go.yaml.in/yaml/v3"
)

const reviewPlannerScript = `if [ -f .review-gate/scripts/ci_review_gate.py ]; then
  python3 -I -B .review-gate/scripts/ci_review_gate.py gate --pr "$PR_NUMBER" --head "$PR_HEAD"
else
  echo "clear=false" >> "$GITHUB_OUTPUT"
  echo "reason=gate unreadable: no trusted helper on the default branch" >> "$GITHUB_OUTPUT"
fi`

// reviewPlannerHelperProblem verifies the helper's source, exact invocation and event inputs.
func reviewPlannerHelperProblem(job ghworkflow.Job) string {
	checkout := job.Steps[1]
	want := map[string]any{
		"ref": "${{ github.event.repository.default_branch }}", "persist-credentials": false,
		"sparse-checkout": "scripts/ci_review_gate.py", "sparse-checkout-cone-mode": false, "path": ".review-gate",
	}
	if checkout.Uses != "actions/checkout@v7" || !reflect.DeepEqual(checkout.With, want) {
		return "review helper must come only from sparse trusted default branch"
	}
	step := job.Steps[2]
	if strings.TrimSpace(step.Run) != reviewPlannerScript || step.Shell != "bash" || step.ID != "review" {
		return "review planner must execute exact trusted gate command or bootstrap hold"
	}
	if !reviewPlannerInputsMatch(step) {
		return "review planner event inputs differ"
	}
	if !reviewPlannerIdleOnDraft(&job) {
		return "review planner must exclude drafts before checkout and reads"
	}
	return ""
}

// reviewTierProblem verifies the trusted draft/hold prefix and every continuation's fresh clearance.
func reviewTierProblem(spec ghworkflow.Spec, id string) string {
	job := spec.Jobs[id]
	if len(job.NeedIDs()) != 1 || job.NeedIDs()[0] != "review-gate" {
		return "heavy job must need review-gate"
	}
	if !strings.Contains(job.If, "!cancelled()") {
		return "heavy job must report failure even when review-gate fails"
	}
	if !strings.Contains(job.RunsOn.Value, "needs.review-gate.outputs.clear == 'true'") ||
		!strings.Contains(job.RunsOn.Value, "'ubuntu-26.04'") {
		return "held job must use Ubuntu"
	}
	if ghworkflow.DraftStepPosition(&job) != 2 {
		return "trusted checkout and fresh hold must precede canonical draft barrier"
	}
	for _, step := range job.Steps[3:] {
		if strings.Contains(step.If, "!cancelled()") &&
			!strings.Contains(step.If, "(github.event_name != 'pull_request' || steps.review.outputs.clear == 'true')") {
			return "failure-continuing step must require review clearance"
		}
	}
	return ""
}

// TestWorkflowHeavyReviewGate validates the pilot planner and each heavy-job contract.
func TestWorkflowHeavyReviewGate(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for file, heavy := range map[string][]string{
		"ci.yml": {"verify", "docs-presets"}, "portability.yml": {"harness"},
	} {
		for _, problem := range reviewGateFileProblems(file, workflows[file], heavy) {
			t.Errorf("%s: %s", file, problem)
		}
	}
}

// TestWorkflowReviewTrustedHelperPlantedDefects rejects a forged planner verdict and untrusted code.
func TestWorkflowReviewTrustedHelperPlantedDefects(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	original := string(workflows["ci.yml"])
	for _, pair := range [][2]string{
		{"persist-credentials: false", "persist-credentials: true"},
		{"ref: ${{ github.event.repository.default_branch }}", "ref: ${{ github.event.pull_request.head.sha }}"},
		{"sparse-checkout: scripts/ci_review_gate.py", "sparse-checkout: scripts"},
		{"python3 -I -B .review-gate/scripts/ci_review_gate.py gate --pr \"$PR_NUMBER\" --head \"$PR_HEAD\"", "echo clear=true"},
		{"PR_HEAD: ${{ github.event.pull_request.head.sha }}", "PR_HEAD: ${{ github.sha }}"},
	} {
		planted := strings.Replace(original, pair[0], pair[1], 1)
		if planted == original || len(reviewGateFileProblems("ci.yml", []byte(planted), []string{"verify", "docs-presets"})) == 0 {
			t.Errorf("trusted helper contract accepted mutation %q", pair[0])
		}
	}
}

// TestWorkflowReviewHelperOnEveryPlatform keeps the offline helper in the shared harness step.
func TestWorkflowReviewHelperOnEveryPlatform(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec := parseReviewWorkflow(t, workflows["portability.yml"])
	for _, step := range spec.Jobs["harness"].Steps {
		if step.Name == "Harness Self-Tests" {
			if !strings.Contains(step.Run, `"$PYTHON" -B scripts/ci_review_gate.py selftest || status=$?`) {
				t.Fatal("review helper selftest missing from every-platform harness")
			}
			return
		}
	}
	t.Fatal("harness selftest step missing")
}

// TestWorkflowReviewPlannerDraftVerdict requires the exact advisory draft verdict in both workflows.
func TestWorkflowReviewPlannerDraftVerdict(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, file := range []string{"ci.yml", "portability.yml"} {
		t.Run(file, func(t *testing.T) {
			assertReviewPlannerDraftVerdict(t, workflows[file])
		})
	}
}

// assertReviewPlannerDraftVerdict checks the draft planner and its exported outputs.
func assertReviewPlannerDraftVerdict(t *testing.T, data []byte) {
	t.Helper()
	spec := parseReviewWorkflow(t, data)
	job := spec.Jobs["review-gate"]
	if !reviewPlannerIdleOnDraft(&job) {
		t.Fatal("advisory draft planner must succeed with clear=false and the exact draft reason before work")
	}
	for _, output := range []string{"clear", "reason"} {
		if !strings.Contains(string(data), "steps.draft.outputs."+output+" || steps.review.outputs."+output) {
			t.Errorf("planner output %s must include the draft verdict", output)
		}
	}
}

// TestReviewPlannerDraftAuditAndPlantedDefects rejects altered advisory draft verdicts and conditions.
func TestReviewPlannerDraftAuditAndPlantedDefects(t *testing.T) {
	const fixture = `jobs:
  review-gate:
    continue-on-error: true
    steps:
      - name: Hold heavy tier on a draft pull request
        id: draft
        if: github.event.pull_request.draft == true
        shell: bash
        run: |
          echo "clear=false" >> "$GITHUB_OUTPUT"
          echo "reason=draft pull request" >> "$GITHUB_OUTPUT"
      - if: github.event_name == 'pull_request' && github.event.pull_request.draft != true
        run: echo review
`
	spec := parseReviewWorkflow(t, []byte(fixture))
	job := spec.Jobs["review-gate"]
	decision := jobDecision{handling: ownDraftHandling(&job, stepWork(&job))}
	if problem := draftProblem(decision, &job); problem != "" {
		t.Fatal(problem)
	}
	for _, pair := range [][2]string{
		{"continue-on-error: true", "continue-on-error: false"},
		{"clear=false", "clear=true"},
		{"reason=draft pull request", "reason=unknown"},
		{"id: draft", "id: other"},
		{"shell: bash", "shell: pwsh"},
		{"run: |", "run: |\n          exit 1"},
		{" && github.event.pull_request.draft != true", ""},
	} {
		planted := strings.Replace(fixture, pair[0], pair[1], 1)
		assertReviewPlannerDraftRejected(t, planted)
	}
}

// assertReviewPlannerDraftRejected requires the draft audit to reject a planted planner defect.
func assertReviewPlannerDraftRejected(t *testing.T, text string) {
	t.Helper()
	spec := parseReviewWorkflow(t, []byte(text))
	job := spec.Jobs["review-gate"]
	decision := jobDecision{handling: ownDraftHandling(&job, stepWork(&job))}
	if draftProblem(decision, &job) == "" {
		t.Errorf("draft audit accepted planted planner defect:\n%s", text)
	}
}

// reviewGateFileProblems lists what is missing from one workflow's review gate: a complete gate job,
// review clearance on every heavy job, the non-PR clear default, and a fast configuration job that
// does not wait for review.
func reviewGateFileProblems(file string, data []byte, heavy []string) []string {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	if problem := reviewPlannerProblem(data, spec); problem != "" {
		problems = append(problems, problem)
	}
	for _, id := range heavy {
		if problem := reviewTierProblem(spec, id); problem != "" {
			problems = append(problems, id+": "+problem)
		}
	}
	config := spec.Jobs["config-validation"]
	if file == "ci.yml" && len(config.NeedIDs()) != 0 {
		problems = append(problems, "fast configuration validation waits for review")
	}
	return problems
}

// reviewReleaseProblem checks the release workflow triggers, permissions and trusted helper.
func reviewReleaseProblem(data []byte) string {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return err.Error()
	}
	if problem := reviewReleaseTriggersProblem(spec); problem != "" {
		return problem
	}
	var scopes map[string]string
	if err := spec.Permissions.Decode(&scopes); err != nil || len(scopes) != 5 || scopes["checks"] != "read" || scopes["statuses"] != "read" ||
		scopes["actions"] != "write" || scopes["contents"] != "read" || scopes["pull-requests"] != "read" {
		return "release permissions differ"
	}
	return reviewReleaseStepsProblem(spec.Jobs["release"])
}

// reviewReleaseTriggersProblem requires scheduled, review and manual release triggers.
func reviewReleaseTriggersProblem(spec ghworkflow.Spec) string {
	for _, event := range []string{"schedule", "pull_request_review", "workflow_dispatch"} {
		if !hasTrigger(&spec.On, event) {
			return "missing release trigger: " + event
		}
	}
	schedule := util.YAMLMappingValue(&spec.On, "schedule")
	if len(schedule.Content) != 1 {
		return "release schedule differs"
	}
	cron := util.YAMLMappingValue(schedule.Content[0], "cron")
	if cron == nil || cron.Value != "*/10 * * * *" {
		return "release cadence differs"
	}
	return ""
}

// reviewReleaseStepsProblem requires sparse default-branch code and the tested release invocation.
func reviewReleaseStepsProblem(job ghworkflow.Job) string {
	if len(job.Steps) != 2 || job.Steps[0].With["ref"] != "${{ github.event.repository.default_branch }}" ||
		job.Steps[0].With["persist-credentials"] != false {
		return "release helper must come from trusted default branch"
	}
	if job.Steps[0].With["sparse-checkout"] != "scripts/ci_review_gate.py" ||
		job.Steps[0].With["sparse-checkout-cone-mode"] != false {
		return "release must check out only trusted helper"
	}
	if job.Steps[1].Run != "python3 -B scripts/ci_review_gate.py release" {
		return "release must use tested helper"
	}
	return ""
}

// TestWorkflowReviewReleaseContractAndPlantedDefects rejects release trigger, permission and helper mutations.
func TestWorkflowReviewReleaseContractAndPlantedDefects(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	original := string(workflows["release-heavy-tier.yml"])
	if problem := reviewReleaseProblem([]byte(original)); problem != "" {
		t.Fatal(problem)
	}
	for _, pair := range [][2]string{
		{"pull_request_review:", "pull_request_review_comment:"},
		{"*/10 * * * *", "*/30 * * * *"},
		{"actions: write", "actions: read"},
		{"checks: read", "checks: none"},
		{"statuses: read", "statuses: none"},
		{"github.event.repository.default_branch", "github.event.pull_request.head.sha"},
		{"ci_review_gate.py release", "ci_review_gate.py gate"},
	} {
		planted := strings.Replace(original, pair[0], pair[1], 1)
		if planted == original || reviewReleaseProblem([]byte(planted)) == "" {
			t.Errorf("release contract accepted mutation %q", pair[0])
		}
	}
}

// TestWorkflowReviewHoldPlantedDefects rejects missing, bypassed or stale review barriers.
func TestWorkflowReviewHoldPlantedDefects(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	original := string(workflows["portability.yml"])
	for name, text := range map[string]string{
		"missing need":   strings.Replace(original, "    needs: review-gate\n", "", 1),
		"spent platform": strings.Replace(original, "needs.review-gate.outputs.clear == 'true' && matrix.os || 'ubuntu-26.04'", "matrix.os", 1),
		"skipped hold":   strings.Replace(original, "id: review\n        if: github.event_name == 'pull_request' && github.event.pull_request.draft != true\n        shell: bash", "id: review\n        if: false\n        shell: bash", 1),
		"passing hold":   strings.Replace(original, "          exit 1\n\n      # The hosted gate's draft step", "          exit 0\n\n      # The hosted gate's draft step", 1),
		"continued work": strings.ReplaceAll(original, "(github.event_name != 'pull_request' || steps.review.outputs.clear == 'true') && github.event.pull_request.draft", "github.event.pull_request.draft"),
	} {
		if text == original {
			t.Fatalf("%s: mutation did not apply", name)
		}
		spec, err := ghworkflow.Parse([]byte(text))
		if err != nil || reviewTierProblem(spec, "harness") == "" {
			t.Errorf("%s: mutation accepted or unparsable: %v", name, err)
		}
	}
}

// TestWorkflowReviewHoldDraftProof refuses prefixes or conditions that hide a failed review hold.
func TestWorkflowReviewHoldDraftProof(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec := parseReviewWorkflow(t, workflows["ci.yml"])
	for _, condition := range []string{"!cancelled()", renovateBranchSkip + " && !cancelled()"} {
		job := spec.Jobs["verify"]
		job.If = condition
		if !reviewHoldRunsAfterFailure(&job) {
			t.Fatalf("proven review/draft barrier refused: %s", condition)
		}
		for _, suffix := range []string{" && !failure()", " && needs.review-gate.result == 'success'", " || false"} {
			job.If = condition + suffix
			if reviewHoldRunsAfterFailure(&job) {
				t.Errorf("conditional failure reporting accepted: %s", job.If)
			}
		}
	}
	job := spec.Jobs["verify"]
	for _, script := range []string{"exit 0", "make verify-all\nexit 1", job.Steps[1].Run + "\necho work"} {
		job.Steps[1].Run = script
		if reviewHoldRunsAfterFailure(&job) {
			t.Errorf("non-inert hold accepted: %s", script)
		}
	}
}

// parseReviewWorkflow shares strict workflow parsing and attributes failures to the test caller.
func parseReviewWorkflow(t *testing.T, data []byte) ghworkflow.Spec {
	t.Helper()
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return spec
}

// reviewPlannerProblem verifies planner shape, trusted code and non-PR clearance before heavy jobs.
func reviewPlannerProblem(data []byte, spec ghworkflow.Spec) string {
	gate, exists := spec.Jobs["review-gate"]
	if !exists || len(gate.Steps) != 3 {
		return "no complete review-gate"
	}
	if !strings.Contains(string(data), "github.event_name != 'pull_request' && 'true'") {
		return "non-PR events must clear the gate"
	}
	return reviewPlannerHelperProblem(gate)
}

// reviewPlannerInputsMatch requires exactly the token and the PR event identity consumed by the gate.
func reviewPlannerInputsMatch(step ghworkflow.Step) bool {
	if len(step.Env.Content) != 6 {
		return false
	}
	for key, value := range map[string]string{
		"GITHUB_TOKEN": "${{ github.token }}", "PR_NUMBER": "${{ github.event.pull_request.number }}",
		"PR_HEAD": "${{ github.event.pull_request.head.sha }}",
	} {
		if got, set, known := ghworkflow.EnvValue(key, &step.Env); !set || !known || got != value {
			return false
		}
	}
	return true
}

// TestWorkflowFreshHoldPermissions requires each heavy job's effective read scopes for its fresh lookup.
func TestWorkflowFreshHoldPermissions(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for _, file := range []string{"ci.yml", "portability.yml"} {
		spec := parseReviewWorkflow(t, workflows[file])
		if problem := reviewReadPermissionsProblem(spec.Permissions); problem != "" {
			t.Errorf("%s: %s", file, problem)
		}
		for _, scope := range []string{"contents", "pull-requests", "checks", "statuses"} {
			planted := strings.Replace(string(workflows[file]), "  "+scope+": read", "  "+scope+": none", 1)
			changed := parseReviewWorkflow(t, []byte(planted))
			if reviewReadPermissionsProblem(changed.Permissions) == "" {
				t.Errorf("%s: accepted missing %s", file, scope)
			}
		}
	}
}

// reviewReadPermissionsProblem validates the read permissions used by every fresh review hold.
func reviewReadPermissionsProblem(node yaml.Node) string {
	var scopes map[string]string
	if err := node.Decode(&scopes); err != nil {
		return err.Error()
	}
	for _, scope := range []string{"contents", "pull-requests", "checks", "statuses"} {
		if scopes[scope] != "read" {
			return "fresh review requires " + scope + ": read"
		}
	}
	return ""
}

// reviewDocsAPILinksProblem verifies that the review gate API link list in the workflow guide
// separates each intermediate item with a comma.
func reviewDocsAPILinksProblem(text string) string {
	links := []string{"[Checks API](", "[status API]("}
	for i := 0; i < len(links) && i < 2; i++ {
		link := links[i]
		idx := strings.Index(text, link)
		if idx == -1 {
			return link + " missing"
		}
		endIdx := strings.Index(text[idx:], ")")
		if endIdx == -1 {
			return link + " malformed"
		}
		rest := strings.TrimSpace(text[idx+endIdx+1:])
		if !strings.HasPrefix(rest, ",") {
			return link + " missing trailing comma"
		}
	}
	return ""
}

// TestWorkflowReviewDocsAPILinks verifies that the API link list in docs/guides/workflow-triggers.md
// correctly separates each link item with a comma.
func TestWorkflowReviewDocsAPILinks(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(engineRoot, "docs", "guides", "workflow-triggers.md"))
	if err != nil {
		t.Fatalf("read workflow triggers guide: %v", err)
	}
	if problem := reviewDocsAPILinksProblem(string(data)); problem != "" {
		t.Fatalf("workflow-triggers.md: %s", problem)
	}
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "positive",
			text: "See the [Checks API](https://example.com/checks),\n[status API](https://example.com/status),\n[whole-run rerun API](https://example.com/rerun)\nand [job-attempt API](https://example.com/jobs).",
			want: "",
		},
		{
			name: "negative missing status comma",
			text: "See the [Checks API](https://example.com/checks),\n[status API](https://example.com/status)\n[whole-run rerun API](https://example.com/rerun)\nand [job-attempt API](https://example.com/jobs).",
			want: "[status API]( missing trailing comma",
		},
		{
			name: "boundary missing checks comma",
			text: "See the [Checks API](https://example.com/checks)\n[status API](https://example.com/status),\n[whole-run rerun API](https://example.com/rerun)\nand [job-attempt API](https://example.com/jobs).",
			want: "[Checks API]( missing trailing comma",
		},
		{
			name: "boundary missing link",
			text: "See the [Checks API](https://example.com/checks),\n[whole-run rerun API](https://example.com/rerun)\nand [job-attempt API](https://example.com/jobs).",
			want: "[status API]( missing",
		},
	}
	for i := 0; i < len(cases) && i < 10; i++ {
		tc := cases[i]
		t.Run(tc.name, func(t *testing.T) {
			got := reviewDocsAPILinksProblem(tc.text)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("problem = %q, want containing %q", got, tc.want)
			}
		})
	}
}
