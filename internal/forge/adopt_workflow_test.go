package forge

import (
	"fmt"
	"maps"
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
	adoptHeadQuery        = `.head.repo.full_name // "", .head.sha // ""`
	adoptTargetPath       = "${{ inputs.target_path }}"
	adoptDryRun           = "${{ inputs.dry_run }}"
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

// mustStepIndex is stepIndex for a mutation that needs the step to exist. A missing step fails
// the test that asked for it; an index of -1 would panic instead and abort the whole package's
// test binary, hiding every other result, the repository-wide permission guard included.
func mustStepIndex(t *testing.T, job workflowJob, accept func(workflowStep) bool) int {
	t.Helper()
	index := stepIndex(job, accept)
	if index < 0 {
		t.Fatal("the job has no step this mutation needs; adopt.yml changed shape")
	}
	return index
}

// yamlNode parses one value the way workflowSpec holds a raw node: a permissions key or a
// step's env.
func yamlNode(t *testing.T, text string) yaml.Node {
	t.Helper()
	var document yaml.Node
	if err := yaml.Unmarshal([]byte(text), &document); err != nil || len(document.Content) != 1 {
		t.Fatalf("parse %q: %v", text, err)
	}
	return *document.Content[0]
}

// stepEnv reads a step's env block as names and values. It reports false when the block is
// not a mapping of names to text, such as one fromJSON expression for the whole block, whose
// variables cannot be known from the file. An absent block is an empty mapping.
func stepEnv(step workflowStep) (map[string]string, bool) {
	if step.Env.Kind == 0 {
		return map[string]string{}, true
	}
	if step.Env.Kind != yaml.MappingNode {
		return nil, false
	}
	env := map[string]string{}
	if err := step.Env.Decode(&env); err != nil {
		return nil, false
	}
	return env, true
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
	env, readable := stepEnv(step)
	if !readable {
		gaps = append(gaps, fmt.Sprintf("job %s: step %q takes its env from one expression, so where the token goes cannot be read", job, step.Name))
	}
	handed := envVariableFor(env, adoptTokenExpression) != ""
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
		mutate func(t *testing.T, spec *workflowSpec)
		want   string
	}{
		{"the shipped shape", func(_ *testing.T, spec *workflowSpec) { *spec = shipped }, "a comment reaches it while it holds contents, pull-requests, issues: write"},
		{"a workflow-level grant", func(t *testing.T, spec *workflowSpec) { spec.Permissions = yamlNode(t, "contents: write") }, "the workflow grants contents: write"},
		{"the commit step in the comment job", moveCommitStepToCommentJob, "pushes in a job a comment reaches"},
		{"a comment job that writes", func(t *testing.T, spec *workflowSpec) {
			job := spec.Jobs[adoptCommentJob]
			job.Permissions = yamlNode(t, "contents: write")
			spec.Jobs[adoptCommentJob] = job
		}, "a comment reaches it while it holds contents: write"},
		{"a checkout that keeps its credential", func(t *testing.T, spec *workflowSpec) {
			comment := spec.Jobs[adoptCommentJob]
			comment.Steps[mustStepIndex(t, comment, isCheckout)].With[persistCredentialsKey] = true
		}, "persists a credential"},
		{"the token handed to the adoption step", func(t *testing.T, spec *workflowSpec) {
			dispatch := spec.Jobs[adoptDispatchJob]
			dispatch.Steps[mustStepIndex(t, dispatch, usesPraetorAdopt)].Env = yamlNode(t, "GH_TOKEN: "+adoptTokenExpression)
		}, "is handed the token"},
		{"an env the check cannot read", func(t *testing.T, spec *workflowSpec) {
			dispatch := spec.Jobs[adoptDispatchJob]
			dispatch.Steps[mustStepIndex(t, dispatch, usesPraetorAdopt)].Env = yamlNode(t, "${{ fromJSON(vars.ADOPT_ENV) }}")
		}, "takes its env from one expression"},
		{"boundary: the dispatch fence dropped", func(_ *testing.T, spec *workflowSpec) {
			job := spec.Jobs[adoptDispatchJob]
			job.If = ""
			spec.Jobs[adoptDispatchJob] = job
		}, "job adopt: step \"Commit & Push Adoption Changes\" pushes in a job a comment reaches"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := parseAdoptWorkflow(t)
			tc.mutate(t, &spec)
			gaps := adoptCredentialGaps(spec)
			if !strings.Contains(strings.Join(gaps, "\n"), tc.want) {
				t.Fatalf("gaps = %q, want one containing %q", gaps, tc.want)
			}
		})
	}
}

// moveCommitStepToCommentJob appends the dispatch job's pushing step to the comment job.
func moveCommitStepToCommentJob(t *testing.T, spec *workflowSpec) {
	t.Helper()
	dispatch := spec.Jobs[adoptDispatchJob]
	index := mustStepIndex(t, dispatch, func(step workflowStep) bool { return strings.Contains(step.Run, "git push") })
	comment := spec.Jobs[adoptCommentJob]
	comment.Steps = append(comment.Steps, dispatch.Steps[index])
	spec.Jobs[adoptCommentJob] = comment
}

func isCheckout(step workflowStep) bool { return strings.HasPrefix(step.Uses, checkoutActionPrefix) }

func usesPraetorAdopt(step workflowStep) bool { return step.Uses == adoptActionUses }

func isHeadLookup(step workflowStep) bool { return step.ID == adoptHeadStepID }

func isRatchet(step workflowStep) bool { return strings.Contains(step.Run, " audit") }

// commentCheckoutGap names why the comment job would not run against the pull request head
// the lookup resolved, or returns "".
func commentCheckoutGap(job workflowJob) string {
	lookup := stepIndex(job, isHeadLookup)
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
		mutate func(t *testing.T, job *workflowJob)
		want   string
	}{
		{"negative: the old ref", func(t *testing.T, job *workflowJob) {
			job.Steps[mustStepIndex(t, *job, isCheckout)].With["ref"] = "${{ github.event.pull_request.head.ref || github.ref }}"
		}, "not the resolved head"},
		{"negative: no ref at all", func(t *testing.T, job *workflowJob) {
			delete(job.Steps[mustStepIndex(t, *job, isCheckout)].With, "ref")
		}, "not the resolved head"},
		{"boundary: checkout first", func(_ *testing.T, job *workflowJob) { slices.Reverse(job.Steps) }, "before the head is resolved"},
		{"boundary: no lookup", func(t *testing.T, job *workflowJob) { job.Steps[mustStepIndex(t, *job, isHeadLookup)].ID = "" }, "no step resolves"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := adoptJob(t, parseAdoptWorkflow(t), adoptCommentJob)
			tc.mutate(t, &job)
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
	ratchet := stepIndex(job, isRatchet)
	if ratchet < 0 {
		return "no step runs the debt ratchet"
	}
	env, _ := stepEnv(job.Steps[ratchet])
	variable := envVariableFor(env, adoptTargetPath)
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
		mutate func(t *testing.T, job *workflowJob)
		want   string
	}{
		{"negative: the path input dropped", func(t *testing.T, job *workflowJob) {
			delete(job.Steps[mustStepIndex(t, *job, usesPraetorAdopt)].With, "path")
		}, "path input"},
		{"boundary: the ratchet audits the root", func(t *testing.T, job *workflowJob) {
			job.Steps[mustStepIndex(t, *job, isRatchet)].Run = `"$PRAETOR_BIN" audit`
		}, "debt ratchet does not read target_path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := adoptJob(t, parseAdoptWorkflow(t), adoptDispatchJob)
			tc.mutate(t, &job)
			if gap := targetPathGap(job); !strings.Contains(gap, tc.want) {
				t.Fatalf("gap = %q, want containing %q", gap, tc.want)
			}
		})
	}
}

// dryRunGap names why the dispatch input dry_run would not reach the adoption step, or returns "".
func dryRunGap(job workflowJob) string {
	adopt := stepIndex(job, usesPraetorAdopt)
	if adopt < 0 {
		return "no step uses praetor-adopt"
	}
	if stepInput(job.Steps[adopt], "dry-run") != adoptDryRun {
		return "the adoption step does not take dry_run as its dry-run input"
	}
	return ""
}

// Positive: dry_run reaches the adoption action so a dry-run dispatch plans without writing.
// Negative and boundary: the input dropped, or wired with another value.
func TestAdoptWorkflow_Positive_DryRunReachesAdoptionAction(t *testing.T) {
	spec := parseAdoptWorkflow(t)
	if gap := dryRunGap(adoptJob(t, spec, adoptDispatchJob)); gap != "" {
		t.Fatalf("dispatch job: %s", gap)
	}
	cases := []struct {
		name   string
		mutate func(t *testing.T, job *workflowJob)
		want   string
	}{
		{"negative: the dry-run input dropped", func(t *testing.T, job *workflowJob) {
			delete(job.Steps[mustStepIndex(t, *job, usesPraetorAdopt)].With, "dry-run")
		}, "the adoption step does not take dry_run as its dry-run input"},
		{"boundary: hardcoded false", func(t *testing.T, job *workflowJob) {
			job.Steps[mustStepIndex(t, *job, usesPraetorAdopt)].With["dry-run"] = "false"
		}, "the adoption step does not take dry_run as its dry-run input"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			job := adoptJob(t, parseAdoptWorkflow(t), adoptDispatchJob)
			tc.mutate(t, &job)
			if gap := dryRunGap(job); !strings.Contains(gap, tc.want) {
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
	lookup := mustStepIndex(t, job, isHeadLookup)
	bound := map[string]string{adoptTokenExpression: "token-under-test", "${{ github.event.issue.number }}": number}
	env := []string{
		"GITHUB_REPOSITORY=" + adoptTestRepository, "PRAETOR_STUB_STDOUT=" + answer,
		"PRAETOR_STUB_EXIT=" + strconv.Itoa(code), "PRAETOR_STUB_ENV=GH_TOKEN",
	}
	lookupEnv, readable := stepEnv(job.Steps[lookup])
	if !readable {
		t.Fatal("the head lookup's env is not a mapping this test can bind")
	}
	for key, expression := range lookupEnv {
		value, modelled := bound[expression]
		if !modelled {
			t.Fatalf("the head lookup binds %s=%s, which this test does not model", key, expression)
		}
		env = append(env, key+"="+value)
	}
	return executeShellBody(t, job.Steps[lookup].Run, "gh", func(_, _ string) []string { return env })
}

// headAnswer is what gh prints for the lookup's query: the head's repository on one line and
// its commit on the next.
func headAnswer(repository, sha string) string { return repository + "\n" + sha }

// Positive: the lookup asks the pulls API for the comment's pull request, with the token, and
// publishes the head it answered when that head lives in this repository.
func TestAdoptWorkflow_Positive_HeadLookupPublishesTheAnsweredHead(t *testing.T) {
	got := runHeadLookup(t, "7", headAnswer(adoptTestRepository, adoptTestHead), 0)
	if got.exitCode != 0 {
		t.Fatalf("exit %d:\n%s", got.exitCode, got.combined)
	}
	if got.outputs["sha"] != adoptTestHead {
		t.Errorf("sha output = %q, want %q", got.outputs["sha"], adoptTestHead)
	}
	want := [][]string{{"api", "repos/" + adoptTestRepository + "/pulls/7", "--jq", adoptHeadQuery, "GH_TOKEN=token-under-test"}}
	if !equalInvocations(got.invocations, want) {
		t.Errorf("gh invocations = %q, want %q", got.invocations, want)
	}
}

// Negative: an API failure, a head outside this repository, or an answer that is not a commit
// id fails the step and publishes no head, so the checkout never falls back to the default
// branch and never takes code a stranger pushed.
func TestAdoptWorkflow_Negative_HeadLookupRefusesWhatItCannotVerify(t *testing.T) {
	cases := []struct {
		name   string
		answer string
		code   int
		want   string
	}{
		{"the API refuses", "", 1, "could not read pull request #7"},
		{"a fork head", headAnswer("stranger/praetor", adoptTestHead), 0, "lives in 'stranger/praetor', not in " + adoptTestRepository},
		{"a deleted fork", headAnswer("", adoptTestHead), 0, "lives in 'a deleted repository'"},
		{"a deleted head", headAnswer(adoptTestRepository, ""), 0, "not a commit id"},
		{"a third line smuggled in", headAnswer(adoptTestRepository, adoptTestHead+"\nrefs/heads/main"), 0, "not a commit id"},
		{"an upper-case id", headAnswer(adoptTestRepository, strings.ToUpper(adoptTestHead)), 0, "not a commit id"},
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
// is not; a repository name this one's is a prefix of, or an answer missing its repository
// line, is not this repository; and a pull request number that is not a number is refused
// before gh is called.
func TestAdoptWorkflow_Boundary_HeadLookupEdges(t *testing.T) {
	cases := []struct {
		name   string
		number string
		answer string
		sha    string
		accept bool
	}{
		{"a SHA-256 id", "7", headAnswer(adoptTestRepository, strings.Repeat("ab", 32)), strings.Repeat("ab", 32), true},
		{"one digit short", "7", headAnswer(adoptTestRepository, adoptTestHead[:39]), adoptTestHead[:39], false},
		{"one digit long", "7", headAnswer(adoptTestRepository, adoptTestHead+"0"), adoptTestHead + "0", false},
		{"a name extending this one", "7", headAnswer(adoptTestRepository+"-fork", adoptTestHead), adoptTestHead, false},
		{"no repository line", "7", adoptTestHead, adoptTestHead, false},
		{"an empty number", "", headAnswer(adoptTestRepository, adoptTestHead), adoptTestHead, false},
		{"a number carrying shell", "7; true", headAnswer(adoptTestRepository, adoptTestHead), adoptTestHead, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runHeadLookup(t, tc.number, tc.answer, 0)
			if accepted := got.exitCode == 0 && got.outputs["sha"] == tc.sha; accepted != tc.accept {
				t.Fatalf("accepted = %v, want %v (exit %d):\n%s", accepted, tc.accept, got.exitCode, got.combined)
			}
			if !tc.accept && got.outputs["sha"] != "" {
				t.Errorf("a refused head still published sha %q", got.outputs["sha"])
			}
			if tc.number != "7" && len(got.invocations) != 0 {
				t.Errorf("gh was called with an unverified number: %q", got.invocations)
			}
		})
	}
}

// adoptCacheGaps names every way a Go cache could carry what the comment job built into a
// later run, or the dispatch job's cache could leave AuditGoBuildCaches. That audit reads the
// setup-go steps of workflow jobs, not those inside the praetor-adopt action, so the dispatch
// job sets Go up itself and turns the action's cache off.
func adoptCacheGaps(spec workflowSpec) []string {
	var gaps []string
	if !jobSetsUpGo(spec.Jobs[adoptDispatchJob]) {
		gaps = append(gaps, "the dispatch job sets Go up only inside the action, where AuditGoBuildCaches does not look")
	}
	ids := sortedJobIDs(spec.Jobs)
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		for j := 0; j < len(job.Steps) && j < maxStepsPerJob; j++ {
			gaps = append(gaps, stepCacheGaps(ids[i], job.Steps[j])...)
		}
	}
	return gaps
}

// stepCacheGaps is adoptCacheGaps for one step of a job.
func stepCacheGaps(job string, step workflowStep) []string {
	var gaps []string
	if usesPraetorAdopt(step) && stepInput(step, "cache") != "false" {
		gaps = append(gaps, fmt.Sprintf("job %s: step %q leaves the action's setup-go cache on", job, step.Name))
	}
	caches := step.Uses == goCacheActionPath || strings.HasPrefix(step.Uses, cacheActionPrefix)
	if caches && job == adoptCommentJob {
		gaps = append(gaps, fmt.Sprintf("job %s: step %q restores and saves a cache in a job a comment starts", job, step.Name))
	}
	return gaps
}

// Positive: no step of the comment job keeps a Go cache, and the dispatch job keeps its own
// through ./.github/actions/go-cache where AuditGoBuildCaches sees it. The audit over the file
// reports nothing, so the check is not vacuous: the dispatch job is inside it.
func TestAdoptWorkflow_Positive_OnlyTheDispatchJobKeepsAGoCache(t *testing.T) {
	spec := parseAdoptWorkflow(t)
	for _, gap := range adoptCacheGaps(spec) {
		t.Error(gap)
	}
	workflows, _ := engineWorkflows(t)
	findings, err := auditWorkflowGoCaches(adoptWorkflowFile, workflows[adoptWorkflowFile], map[string]string{})
	if err != nil || len(findings) != 0 {
		t.Fatalf("go cache audit of %s: %v %v", adoptWorkflowFile, findings, err)
	}
	dispatch := adoptJob(t, spec, adoptDispatchJob)
	if identity := goBuildCacheIdentity(dispatch); !slices.Equal(identity, []string{"job:adopt"}) {
		t.Errorf("the dispatch job's build cache identity is %v, want [job:adopt]", identity)
	}
}

// Negative and boundary: the action's cache left on in either job, or a cache step in the
// comment job, is reported. The boundary is the dispatch job's own setup-go dropped: the job
// still builds with the action's Go, and silently leaves the audit.
func TestAdoptWorkflow_Negative_CacheGapsAreReported(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(t *testing.T, spec *workflowSpec)
		want   string
	}{
		{"the comment job's action cache left on", func(t *testing.T, spec *workflowSpec) {
			comment := spec.Jobs[adoptCommentJob]
			delete(comment.Steps[mustStepIndex(t, comment, usesPraetorAdopt)].With, "cache")
		}, "job adopt-comment: step \"Execute Dogfood Benchmark\" leaves the action's setup-go cache on"},
		{"the dispatch job's action cache left on", func(t *testing.T, spec *workflowSpec) {
			dispatch := spec.Jobs[adoptDispatchJob]
			dispatch.Steps[mustStepIndex(t, dispatch, usesPraetorAdopt)].With["cache"] = true
		}, "job adopt: step \"Execute Fast Adoption\" leaves the action's setup-go cache on"},
		{"a cache step in the comment job", func(_ *testing.T, spec *workflowSpec) {
			comment := spec.Jobs[adoptCommentJob]
			comment.Steps = append(comment.Steps, workflowStep{Name: "Restore Go caches", Uses: goCacheActionPath})
			spec.Jobs[adoptCommentJob] = comment
		}, "restores and saves a cache in a job a comment starts"},
		{"boundary: the dispatch job's setup-go dropped", func(_ *testing.T, spec *workflowSpec) {
			dispatch := spec.Jobs[adoptDispatchJob]
			dispatch.Steps = slices.DeleteFunc(dispatch.Steps, func(step workflowStep) bool {
				return strings.HasPrefix(step.Uses, setupGoPrefix)
			})
			spec.Jobs[adoptDispatchJob] = dispatch
		}, "where AuditGoBuildCaches does not look"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec := parseAdoptWorkflow(t)
			tc.mutate(t, &spec)
			if gaps := adoptCacheGaps(spec); !strings.Contains(strings.Join(gaps, "\n"), tc.want) {
				t.Fatalf("gaps = %q, want one containing %q", gaps, tc.want)
			}
		})
	}
}

// adoptTimeoutGaps names every job of the workflow that sets no timeout-minutes of its own,
// and the head lookup step when it sets none: without one GitHub lets a hung job run for 360
// minutes. The keys are read through a shape local to this test, so the shared workflowJob
// gains no field that only a test reads and that a valid spelling elsewhere could fail to
// parse.
func adoptTimeoutGaps(t *testing.T, data []byte) []string {
	t.Helper()
	var spec struct {
		Jobs map[string]struct {
			TimeoutMinutes any `yaml:"timeout-minutes"`
			Steps          []struct {
				ID             string `yaml:"id"`
				TimeoutMinutes any    `yaml:"timeout-minutes"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(data, &spec); err != nil {
		t.Fatalf("parse %s: %v", adoptWorkflowFile, err)
	}
	var gaps []string
	ids := slices.Sorted(maps.Keys(spec.Jobs))
	for i := 0; i < len(ids) && i < maxJobsPerFile; i++ {
		job := spec.Jobs[ids[i]]
		if job.TimeoutMinutes == nil {
			gaps = append(gaps, "job "+ids[i]+" sets no timeout-minutes")
		}
		for j := 0; j < len(job.Steps) && j < maxStepsPerJob; j++ {
			if job.Steps[j].ID == adoptHeadStepID && job.Steps[j].TimeoutMinutes == nil {
				gaps = append(gaps, "step "+adoptHeadStepID+" calls the API with no timeout-minutes")
			}
		}
	}
	return gaps
}

// HISS-02: every job, and the one step that makes a network call of its own, runs under an
// explicit bound. Positive: the shipped file. Negative: a job's bound removed. Boundary: the
// job bounds kept and only the head lookup's step bound removed.
func TestAdoptWorkflow_Positive_EveryJobAndTheHeadLookupAreBounded(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	shipped := string(workflows[adoptWorkflowFile])
	if gaps := adoptTimeoutGaps(t, []byte(shipped)); len(gaps) != 0 {
		t.Fatalf("unbounded: %q", gaps)
	}
	cases := []struct {
		name   string
		remove string
		want   string
	}{
		{"negative: the dispatch job's bound removed", "    timeout-minutes: 30\n    permissions:\n      contents: write\n", "job adopt sets no timeout-minutes"},
		{"boundary: only the head lookup's bound removed", "        timeout-minutes: 2\n", "step pr-head calls the API with no timeout-minutes"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if strings.Count(shipped, tc.remove) != 1 {
				t.Fatalf("adopt.yml no longer holds %q once, so this mutation decides nothing", tc.remove)
			}
			// The first line of the matched text is the bound; the rest only anchors it.
			_, anchor, _ := strings.Cut(tc.remove, "\n")
			mutated := strings.Replace(shipped, tc.remove, anchor, 1)
			if gaps := adoptTimeoutGaps(t, []byte(mutated)); !slices.Contains(gaps, tc.want) {
				t.Fatalf("gaps = %q, want %q", gaps, tc.want)
			}
		})
	}
}
