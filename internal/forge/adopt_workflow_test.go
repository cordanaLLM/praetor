package forge

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// adopt.yml is the one workflow here that a pull request comment starts. issue_comment runs
// the default branch's file with the base repository's token for anyone who can comment, so
// the shape that keeps that token away from strangers and from the pull request's code is
// asserted here rather than left to review. The permission split itself is also audited
// repository-wide by TestAuditPullRequestPermissions_Guard_ThisRepositoryIsDisciplined.

const (
	adoptWorkflowFile     = "adopt.yml"
	adoptDispatchJob      = "adopt"
	adoptCommentJob       = "adopt-comment"
	adoptHeadStepID       = "pr-head"
	adoptActionUses       = "./.github/actions/praetor-adopt"
	adoptTokenExpression  = "${{ github.token }}"
	adoptHeadRef          = "${{ steps." + adoptHeadStepID + ".outputs.sha }}"
	adoptTargetPath       = "${{ inputs.target_path }}"
	adoptTestRepository   = "cordanaLLM/praetor"
	adoptTestHead         = "0123456789abcdef0123456789abcdef01234567"
	checkoutActionPrefix  = "actions/checkout@"
	persistCredentialsKey = "persist-credentials"
)

// parseAdoptWorkflow parses this repository's adopt.yml afresh, so a test may mutate what it
// gets back without another test seeing the change.
func parseAdoptWorkflow(t *testing.T) workflowSpec {
	t.Helper()
	workflows, _ := engineWorkflows(t)
	data, ok := workflows[adoptWorkflowFile]
	if !ok {
		t.Fatalf("%s is missing", adoptWorkflowFile)
	}
	var spec workflowSpec
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse %s: %v", adoptWorkflowFile, err)
	}
	return spec
}

// adoptJob returns one job of the parsed workflow or fails the test.
func adoptJob(t *testing.T, spec workflowSpec, id string) workflowJob {
	t.Helper()
	job, ok := spec.Jobs[id]
	if !ok {
		t.Fatalf("%s has no job %q", adoptWorkflowFile, id)
	}
	return job
}

// stepIndex returns the position of the first step accept selects, or -1.
func stepIndex(job workflowJob, accept func(workflowStep) bool) int {
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		if accept(job.Steps[i]) {
			return i
		}
	}
	return -1
}

// permissionsNode parses a permissions value the way workflowSpec holds one.
func permissionsNode(t *testing.T, text string) yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil || len(document.Content) != 1 {
		t.Fatalf("parse permissions %q: %v", text, err)
	}
	return *document.Content[0]
}

// adoptCredentialGaps names every way the workflow's token reaches further than the steps
// that need it: a write scope a comment can reach, a push a comment can reach, a checkout
// that leaves a credential behind for the code it checks out, or a step handed the token that
// neither pushes nor resolves the pull request head.
func adoptCredentialGaps(spec workflowSpec) []string {
	var gaps []string
	inherited, _ := writeScopes(&spec.Permissions)
	if len(inherited) > 0 {
		gaps = append(gaps, "the workflow grants "+strings.Join(inherited, ", ")+": write to every job")
	}
	ids := sortedJobIDs(spec.Jobs)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		gaps = append(gaps, jobCredentialGaps(ids[i], spec.Jobs[ids[i]], inherited)...)
	}
	return gaps
}

// jobCredentialGaps is adoptCredentialGaps for one job. A job's own permissions key replaces
// the workflow's rather than adding to it, as the permission audit reads it.
//
// This is stricter than the permission audit. The audit accepts a write scope behind a
// trusted-commenter gate; here any job a comment starts at all, a trusted one included, runs
// the pull request's own code and must hold no write scope and push nothing.
func jobCredentialGaps(id string, job workflowJob, inherited []string) []string {
	var gaps []string
	commentReaches := len(reachingEvents(withoutCommenterGate(job.If), []string{issueCommentEvent})) > 0
	scopes := inherited
	if own, declared := writeScopes(&job.Permissions); declared {
		scopes = own
	}
	if commentReaches && len(scopes) > 0 {
		gaps = append(gaps, fmt.Sprintf("job %s: a comment reaches it while it holds %s: write",
			id, strings.Join(scopes, ", ")))
	}
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		gaps = append(gaps, stepCredentialGaps(id, job.Steps[i], commentReaches)...)
	}
	return gaps
}

// stepCredentialGaps is adoptCredentialGaps for one step of a job.
func stepCredentialGaps(job string, step workflowStep, commentReaches bool) []string {
	var gaps []string
	if strings.HasPrefix(step.Uses, checkoutActionPrefix) && stepInput(step, persistCredentialsKey) != "false" {
		gaps = append(gaps, fmt.Sprintf("job %s: checkout %q persists a credential for the code it checks out", job, step.Name))
	}
	pushes := strings.Contains(step.Run, "git push")
	if pushes && commentReaches {
		gaps = append(gaps, fmt.Sprintf("job %s: step %q pushes in a job a comment reaches", job, step.Name))
	}
	handed := envVariableFor(step.Env, adoptTokenExpression) != ""
	if handed && !pushes && step.ID != adoptHeadStepID {
		gaps = append(gaps, fmt.Sprintf("job %s: step %q is handed the token but neither pushes nor resolves the head", job, step.Name))
	}
	return gaps
}

// withoutCommenterGate returns a job condition with every top-level conjunct that reads the
// commenter's association removed, which is the condition a trusted commenter meets.
func withoutCommenterGate(condition string) string {
	conjuncts := topLevelParts(condition, "&&")
	kept := make([]string, 0, len(conjuncts))
	for i := 0; i < len(conjuncts) && i < maxPermissionScopes; i++ {
		if !strings.Contains(conjuncts[i], authorAssociationExpression) {
			kept = append(kept, conjuncts[i])
		}
	}
	return strings.Join(kept, "&&")
}

// Positive: the shipped file hands the token only to the dispatch job's commit step and the
// comment job's head lookup, and the check is not vacuous: the dispatch job does write and
// does push.
func TestAdoptWorkflow_Positive_CredentialsReachOnlyTheStepsThatUseThem(t *testing.T) {
	spec := parseAdoptWorkflow(t)
	for _, gap := range adoptCredentialGaps(spec) {
		t.Error(gap)
	}
	dispatch := adoptJob(t, spec, adoptDispatchJob)
	if scopes, _ := writeScopes(&dispatch.Permissions); !slices.Equal(scopes, []string{"contents"}) {
		t.Errorf("the dispatch job writes %v; it commits the adoption and needs contents alone", scopes)
	}
	if stepIndex(dispatch, func(step workflowStep) bool { return strings.Contains(step.Run, "git push") }) < 0 {
		t.Error("the dispatch job no longer pushes, so the credential check above decided nothing")
	}
	comment := adoptJob(t, spec, adoptCommentJob)
	if scopes, _ := writeScopes(&comment.Permissions); len(scopes) != 0 {
		t.Errorf("the comment job writes %v; it reports and must hold read scopes only", scopes)
	}
}

// theAdoptShapeShipped is adopt.yml before this fix: one job for both entry points, a
// workflow-level write grant, and a condition any commenter satisfies.
const theAdoptShapeShipped = `
on:
  workflow_dispatch:
  issue_comment:
    types: [created]
permissions:
  contents: write
  pull-requests: write
  issues: write
jobs:
  adopt:
    if: >
      github.event_name == 'workflow_dispatch' ||
      (github.event.issue.pull_request && contains(github.event.comment.body, '/adopt'))
    runs-on: ubuntu-latest
    steps:
      - name: Checkout Repository
        uses: actions/checkout@v4
        with:
          ref: ${{ github.event.pull_request.head.ref || github.ref }}
      - name: Commit & Push Adoption Changes
        run: git push
`

// Negative and boundary: each way the token could reach further than it does is reported.
// The dispatch-only fence on the commit step is the boundary: drop the dispatch job's
// condition and a comment reaches a job that writes and pushes.
func TestAdoptWorkflow_Negative_CredentialGapsAreReported(t *testing.T) {
	var shipped workflowSpec
	if err := yaml.Unmarshal([]byte(theAdoptShapeShipped), &shipped); err != nil {
		t.Fatalf("parse the shipped shape: %v", err)
	}
	cases := []struct {
		name   string
		mutate func(spec *workflowSpec)
		want   string
	}{
		{"the shipped shape", func(spec *workflowSpec) { *spec = shipped }, "a comment reaches it while it holds contents, pull-requests, issues: write"},
		{"a workflow-level grant", func(spec *workflowSpec) { spec.Permissions = permissionsNode(t, "contents: write") }, "the workflow grants contents: write"},
		{"the commit step in the comment job", moveCommitStepToCommentJob(t), "pushes in a job a comment reaches"},
		{"a comment job that writes", func(spec *workflowSpec) {
			job := spec.Jobs[adoptCommentJob]
			job.Permissions = permissionsNode(t, "contents: write")
			spec.Jobs[adoptCommentJob] = job
		}, "a comment reaches it while it holds contents: write"},
		{"a checkout that keeps its credential", func(spec *workflowSpec) {
			spec.Jobs[adoptCommentJob].Steps[stepIndex(spec.Jobs[adoptCommentJob], isCheckout)].With[persistCredentialsKey] = true
		}, "persists a credential"},
		{"the token handed to the adoption step", func(spec *workflowSpec) {
			dispatch := spec.Jobs[adoptDispatchJob]
			dispatch.Steps[stepIndex(dispatch, usesPraetorAdopt)].Env = map[string]string{"GH_TOKEN": adoptTokenExpression}
		}, "is handed the token"},
		{"boundary: the dispatch fence dropped", func(spec *workflowSpec) {
			job := spec.Jobs[adoptDispatchJob]
			job.If = ""
			spec.Jobs[adoptDispatchJob] = job
		}, "job adopt: step \"Commit & Push Adoption Changes\" pushes in a job a comment reaches"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := parseAdoptWorkflow(t)
			tc.mutate(&spec)
			gaps := adoptCredentialGaps(spec)
			if !strings.Contains(strings.Join(gaps, "\n"), tc.want) {
				t.Fatalf("gaps = %q, want one containing %q", gaps, tc.want)
			}
		})
	}
}

// moveCommitStepToCommentJob returns a mutation that appends the dispatch job's pushing step
// to the comment job.
func moveCommitStepToCommentJob(t *testing.T) func(spec *workflowSpec) {
	t.Helper()
	return func(spec *workflowSpec) {
		dispatch := spec.Jobs[adoptDispatchJob]
		index := stepIndex(dispatch, func(step workflowStep) bool { return strings.Contains(step.Run, "git push") })
		if index < 0 {
			t.Fatal("the dispatch job has no pushing step to move")
		}
		comment := spec.Jobs[adoptCommentJob]
		comment.Steps = append(comment.Steps, dispatch.Steps[index])
		spec.Jobs[adoptCommentJob] = comment
	}
}

func isCheckout(step workflowStep) bool { return strings.HasPrefix(step.Uses, checkoutActionPrefix) }

func usesPraetorAdopt(step workflowStep) bool { return step.Uses == adoptActionUses }

// commentCheckoutGap names why the comment job would not run against the pull request head
// the lookup resolved, or returns "".
func commentCheckoutGap(job workflowJob) string {
	lookup := stepIndex(job, func(step workflowStep) bool { return step.ID == adoptHeadStepID })
	checkout := stepIndex(job, isCheckout)
	switch {
	case lookup < 0:
		return "no step resolves the pull request head"
	case checkout < 0:
		return "no step checks anything out"
	case checkout < lookup:
		return "the checkout runs before the head is resolved"
	case stepInput(job.Steps[checkout], "ref") != adoptHeadRef:
		return "the checkout takes ref " + strconv.Quote(stepInput(job.Steps[checkout], "ref")) + ", not the resolved head"
	}
	return ""
}

// BUG-268: an issue_comment payload has no pull_request object, so the old checkout ref
// fell back to the default branch. Positive: the comment job checks out the head it resolved.
// Negative and boundary: the old ref, no ref, and a checkout ahead of the lookup.
func TestAdoptWorkflow_Positive_CommentJobChecksOutTheResolvedHead(t *testing.T) {
	spec := parseAdoptWorkflow(t)
	if gap := commentCheckoutGap(adoptJob(t, spec, adoptCommentJob)); gap != "" {
		t.Fatalf("comment job: %s", gap)
	}
	workflows, _ := engineWorkflows(t)
	if strings.Contains(string(workflows[adoptWorkflowFile]), "github.event.pull_request") {
		t.Error("adopt.yml reads github.event.pull_request, which no run of it carries")
	}
	cases := []struct {
		name   string
		mutate func(job *workflowJob)
		want   string
	}{
		{"negative: the old ref", func(job *workflowJob) {
			job.Steps[stepIndex(*job, isCheckout)].With["ref"] = "${{ github.event.pull_request.head.ref || github.ref }}"
		}, "not the resolved head"},
		{"negative: no ref at all", func(job *workflowJob) { delete(job.Steps[stepIndex(*job, isCheckout)].With, "ref") }, "not the resolved head"},
		{"boundary: checkout first", func(job *workflowJob) { slices.Reverse(job.Steps) }, "before the head is resolved"},
		{"boundary: no lookup", func(job *workflowJob) { job.Steps[0].ID = "" }, "no step resolves"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := adoptJob(t, parseAdoptWorkflow(t), adoptCommentJob)
			tc.mutate(&job)
			if gap := commentCheckoutGap(job); !strings.Contains(gap, tc.want) {
				t.Fatalf("gap = %q, want containing %q", gap, tc.want)
			}
		})
	}
}

// BUG-550: the comment job is fenced by its author_association conjunct and nothing else.
// With that conjunct removed the same condition admits a comment again, so it is the gate the
// audit recognised, not some other conjunct, that keeps strangers out.
func TestAdoptWorkflow_Positive_CommentJobAdmitsTrustedCommentersOnly(t *testing.T) {
	condition := adoptJob(t, parseAdoptWorkflow(t), adoptCommentJob).If
	events := []string{issueCommentEvent}
	if reaching := reachingEvents(condition, events); len(reaching) != 0 {
		t.Fatalf("a comment from any account starts the comment job: %q", condition)
	}
	ungated := withoutCommenterGate(condition)
	if len(topLevelParts(ungated, "&&")) == len(topLevelParts(condition, "&&")) {
		t.Fatalf("the comment job's condition has no author_association conjunct: %q", condition)
	}
	if reaching := reachingEvents(ungated, events); !slices.Equal(reaching, events) {
		t.Fatalf("without its gate the condition still fences comments off (%v), so the gate is untested", reaching)
	}
}

// targetPathGap names why the dispatch input target_path would not reach both commands that
// work on the target, or returns "".
func targetPathGap(job workflowJob) string {
	adopt := stepIndex(job, usesPraetorAdopt)
	if adopt < 0 || stepInput(job.Steps[adopt], "path") != adoptTargetPath {
		return "the adoption step does not take target_path as its path input"
	}
	ratchet := stepIndex(job, func(step workflowStep) bool { return strings.Contains(step.Run, " audit") })
	if ratchet < 0 {
		return "no step runs the debt ratchet"
	}
	variable := envVariableFor(job.Steps[ratchet].Env, adoptTargetPath)
	if variable == "" || !strings.Contains(job.Steps[ratchet].Run, "$"+variable) {
		return "the debt ratchet does not read target_path through its environment"
	}
	return ""
}

// BUG-549: target_path was declared and never read. Positive: it reaches the adoption and the
// debt ratchet, through env, and no run body in the file splices an expression into its
// script. Negative and boundary: the input dropped from either step.
func TestAdoptWorkflow_Positive_TargetPathReachesBothCommands(t *testing.T) {
	spec := parseAdoptWorkflow(t)
	if gap := targetPathGap(adoptJob(t, spec, adoptDispatchJob)); gap != "" {
		t.Fatalf("dispatch job: %s", gap)
	}
	for id, job := range spec.Jobs {
		for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
			if strings.Contains(job.Steps[i].Run, "${{") {
				t.Errorf("job %s: step %q interpolates an expression into its shell body", id, job.Steps[i].Name)
			}
		}
	}
	cases := []struct {
		name   string
		mutate func(job *workflowJob)
		want   string
	}{
		{"negative: the path input dropped", func(job *workflowJob) { delete(job.Steps[stepIndex(*job, usesPraetorAdopt)].With, "path") }, "path input"},
		{"boundary: the ratchet audits the root", func(job *workflowJob) {
			ratchet := stepIndex(*job, func(step workflowStep) bool { return strings.Contains(step.Run, " audit") })
			job.Steps[ratchet].Run = `"$PRAETOR_BIN" audit`
		}, "debt ratchet does not read target_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := adoptJob(t, parseAdoptWorkflow(t), adoptDispatchJob)
			tc.mutate(&job)
			if gap := targetPathGap(job); !strings.Contains(gap, tc.want) {
				t.Fatalf("gap = %q, want containing %q", gap, tc.want)
			}
		})
	}
}

// runHeadLookup executes the comment job's own head-lookup body with a stub gh that prints
// answer and exits with code, binding the step's env block the way GitHub would.
func runHeadLookup(t *testing.T, number, answer string, code int) stepOutcome {
	t.Helper()
	job := adoptJob(t, parseAdoptWorkflow(t), adoptCommentJob)
	lookup := stepIndex(job, func(step workflowStep) bool { return step.ID == adoptHeadStepID })
	if lookup < 0 {
		t.Fatal("the comment job has no head lookup step")
	}
	bound := map[string]string{adoptTokenExpression: "token-under-test", "${{ github.event.issue.number }}": number}
	env := []string{
		"GITHUB_REPOSITORY=" + adoptTestRepository, "PRAETOR_STUB_STDOUT=" + answer,
		"PRAETOR_STUB_EXIT=" + strconv.Itoa(code), "PRAETOR_STUB_ENV=GH_TOKEN",
	}
	for key, expression := range job.Steps[lookup].Env {
		value, modelled := bound[expression]
		if !modelled {
			t.Fatalf("the head lookup binds %s=%s, which this test does not model", key, expression)
		}
		env = append(env, key+"="+value)
	}
	return executeShellBody(t, job.Steps[lookup].Run, "gh", func(_, _ string) []string { return env })
}

// Positive: the lookup asks the pulls API for the comment's pull request, with the token, and
// publishes the head it answered.
func TestAdoptWorkflow_Positive_HeadLookupPublishesTheAnsweredHead(t *testing.T) {
	got := runHeadLookup(t, "7", adoptTestHead, 0)
	if got.exitCode != 0 {
		t.Fatalf("exit %d:\n%s", got.exitCode, got.combined)
	}
	if got.outputs["sha"] != adoptTestHead {
		t.Errorf("sha output = %q, want %q", got.outputs["sha"], adoptTestHead)
	}
	want := [][]string{{"api", "repos/" + adoptTestRepository + "/pulls/7", "--jq", ".head.sha", "GH_TOKEN=token-under-test"}}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("gh invocations = %q, want %q", got.invocations, want)
	}
}

// Negative: an API failure or an answer that is not a commit id fails the step and publishes
// no head, so the checkout never falls back to the default branch.
func TestAdoptWorkflow_Negative_HeadLookupRefusesWhatItCannotVerify(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		code   int
		want   string
	}{
		{"the API refuses", "", 1, "could not read pull request #7"},
		{"a deleted head", "null", 0, "not a commit id"},
		{"a second line smuggled in", adoptTestHead + "\nrefs/heads/main", 0, "not a commit id"},
		{"an upper-case id", strings.ToUpper(adoptTestHead), 0, "not a commit id"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runHeadLookup(t, "7", tc.answer, tc.code)
			if got.exitCode == 0 || got.outputs["sha"] != "" {
				t.Fatalf("exit %d, sha %q: an unverified head was accepted", got.exitCode, got.outputs["sha"])
			}
			if !strings.Contains(got.combined, tc.want) {
				t.Errorf("output does not say %q:\n%s", tc.want, got.combined)
			}
		})
	}
}

// Boundary: the id lengths Git uses (SHA-1 and SHA-256) are accepted and one short of either
// is not, and a pull request number that is not a number is refused before gh is called.
func TestAdoptWorkflow_Boundary_HeadLookupEdges(t *testing.T) {
	cases := []struct {
		name   string
		number string
		answer string
		accept bool
	}{
		{"a SHA-256 id", "7", strings.Repeat("ab", 32), true},
		{"one digit short", "7", adoptTestHead[:39], false},
		{"one digit long", "7", adoptTestHead + "0", false},
		{"an empty number", "", adoptTestHead, false},
		{"a number carrying shell", "7; true", adoptTestHead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runHeadLookup(t, tc.number, tc.answer, 0)
			if accepted := got.exitCode == 0 && got.outputs["sha"] == tc.answer; accepted != tc.accept {
				t.Fatalf("accepted = %v, want %v (exit %d):\n%s", accepted, tc.accept, got.exitCode, got.combined)
			}
			if tc.number != "7" && len(got.invocations) != 0 {
				t.Errorf("gh was called with an unverified number: %q", got.invocations)
			}
		})
	}
}
