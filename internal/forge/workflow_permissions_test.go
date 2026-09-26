package forge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// theShapePagesShipped is what .github/workflows/pages.yml declared before #292: two write
// scopes at workflow level, a pull_request trigger, and a deploy job that pull requests
// never reach. The build job inherited credentials it had no use for.
const theShapePagesShipped = `
name: Deploy GitHub Pages
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
    paths: ['docs/**']
permissions:
  contents: read
  pages: write
  id-token: write
jobs:
  build:
    if: github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
  deploy:
    needs: build
    if: github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/deploy-pages@v4
`

// theShapePagesShipsNow moves both write scopes onto the job that uses them.
const theShapePagesShipsNow = `
name: Deploy GitHub Pages
on:
  push:
    branches: [main]
  pull_request:
    branches: [main]
    paths: ['docs/**']
permissions:
  contents: read
jobs:
  build:
    if: github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
  deploy:
    needs: build
    if: github.ref == 'refs/heads/main' && github.event_name != 'pull_request' && github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')
    runs-on: ubuntu-latest
    permissions:
      contents: read
      pages: write
      id-token: write
    steps:
      - uses: actions/deploy-pages@v4
`

func auditPermissionsDocument(t *testing.T, name, body string) []PullRequestPermissionFinding {
	t.Helper()
	findings, err := auditWorkflowPullRequestPermissions(name, []byte(body))
	if err != nil {
		t.Fatalf("audit %s: %v", name, err)
	}
	return findings
}

// Positive: the defect this check exists for is reported, once per write scope, and only
// against the job a pull request can actually start.

func TestAuditPullRequestPermissions_Positive_ReportsTheWorkflowLevelWriteScopes(t *testing.T) {
	findings := auditPermissionsDocument(t, "pages.yml", theShapePagesShipped)

	if len(findings) != 2 {
		t.Fatalf("expected one finding per write scope, got %d: %v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Job != "build" {
			t.Errorf("deploy is fenced off from pull requests; reported %s", finding)
		}
	}
	if findings[0].Scope != "pages" || findings[1].Scope != "id-token" {
		t.Errorf("findings do not name both scopes in declaration order: %v", findings)
	}
}

func TestAuditPullRequestPermissions_Positive_ReportsAJobThatDeclaresItsOwnWrite(t *testing.T) {
	body := strings.Replace(theShapePagesShipsNow,
		"  build:\n    if:", "  build:\n    permissions:\n      contents: write\n    if:", 1)

	findings := auditPermissionsDocument(t, "pages.yml", body)

	if len(findings) != 1 || findings[0].Job != "build" || findings[0].Scope != "contents" {
		t.Fatalf("expected one contents:write finding against build, got %v", findings)
	}
	if !strings.Contains(findings[0].String(), "reachable from pull_request") {
		t.Errorf("finding does not say why it is one: %s", findings[0])
	}
}

// theShapeATargetWorkflowWouldShip is the strictly worse form of #292: pull_request_target
// runs a contributor's branch in the base repository's context, so a workflow-level write
// hands that contributor the base repository's own token.
const theShapeATargetWorkflowWouldShip = `
name: Label Contributions
on:
  pull_request_target:
    branches: [main]
permissions:
  contents: write
  pull-requests: write
jobs:
  label:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
`

func TestAuditPullRequestPermissions_Positive_ReportsAPullRequestTargetWorkflow(t *testing.T) {
	findings := auditPermissionsDocument(t, "label.yml", theShapeATargetWorkflowWouldShip)

	if len(findings) != 2 {
		t.Fatalf("expected one finding per write scope, got %d: %v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.Trigger != pullRequestTargetEvent {
			t.Errorf("a finding must name the event that reaches the job: %s", finding)
		}
	}
}

// declaringBothPullRequestEvents is the two-event form of the label workflow, so a job
// condition can fence one event off while the other still reaches the job.
func declaringBothPullRequestEvents(condition string) string {
	body := strings.Replace(theShapeATargetWorkflowWouldShip,
		"  pull_request_target:\n    branches: [main]\n",
		"  pull_request:\n    branches: [main]\n  pull_request_target:\n    branches: [main]\n", 1)
	if condition == "" {
		return body
	}
	return strings.Replace(body,
		"  label:\n    runs-on:", "  label:\n    if: "+condition+"\n    runs-on:", 1)
}

func TestAuditPullRequestPermissions_Positive_ReportsAJobFencedOffFromOnlyOneOfTheTwoEvents(t *testing.T) {
	body := declaringBothPullRequestEvents("github.event_name != 'pull_request'")

	findings := auditPermissionsDocument(t, "label.yml", body)

	if len(findings) != 2 {
		t.Fatalf("the target event still reaches the job; got %v", findings)
	}
	// Naming pull_request here would tell an operator that a correctly fenced event is a
	// hole. Only the event that survives the condition reaches the job.
	for _, finding := range findings {
		if finding.Trigger != pullRequestTargetEvent {
			t.Errorf("the finding names an event the condition fences off: %s", finding)
		}
	}
}

func TestAuditPullRequestPermissions_Positive_NamesOnlyTheEventAnEqualityTestKeeps(t *testing.T) {
	body := declaringBothPullRequestEvents("github.event_name == 'pull_request'")

	findings := auditPermissionsDocument(t, "label.yml", body)

	if len(findings) != 2 {
		t.Fatalf("the pull request event still reaches the job; got %v", findings)
	}
	if findings[0].Trigger != pullRequestEvent {
		t.Errorf("the finding names an event the condition fences off: %s", findings[0])
	}
}

// A job fenced off from every pull request event the workflow declares is not a finding.
// Judging each conjunct against the whole declaration list reported this correctly fenced
// shape and turned the repository-wide guard below into a red build.
func TestAuditPullRequestPermissions_Negative_AcceptsAJobFencedOffFromBothEvents(t *testing.T) {
	body := declaringBothPullRequestEvents(
		"github.event_name != 'pull_request' && github.event_name != 'pull_request_target'")

	if findings := auditPermissionsDocument(t, "label.yml", body); len(findings) != 0 {
		t.Fatalf("neither declared event reaches the job; got %v", findings)
	}
}

// A job whose guard is one parenthesised disjunction naming a pull request event carries
// exactly the credential the audit exists for. Unwrapping that group and reading the whole
// disjunction as one comparison value matched no event at all and reported nothing.
func TestAuditPullRequestPermissions_Positive_ReportsAJobGuardedByADisjunctionGroup(t *testing.T) {
	body := declaringBothPullRequestEvents(
		"(github.event_name == 'push' || github.event_name == 'pull_request')")

	findings := auditPermissionsDocument(t, "label.yml", body)

	if len(findings) != 2 {
		t.Fatalf("the disjunction names pull_request; got %v", findings)
	}
	if findings[0].Trigger != pullRequestEvent {
		t.Errorf("only the event the disjunction names reaches the job: %s", findings[0])
	}
}

// The common same-repository guard, spelled as one parenthesised group, still leaves a pull
// request from a branch of this repository with the workflow's write scope. Reading the
// whole group as one comparison reported nothing here, while the unparenthesised spelling of
// the same condition reported the finding.
func TestAuditPullRequestPermissions_Positive_ReportsAJobBehindTheSameRepositoryGuardGroup(t *testing.T) {
	const guard = "github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository"
	for _, condition := range []string{guard, "(" + guard + ")"} {
		t.Run(condition, func(t *testing.T) {
			body := strings.Replace(theShapePagesShipsNow,
				"permissions:\n  contents: read\njobs:",
				"permissions:\n  contents: write\njobs:", 1)
			body = strings.Replace(body,
				"  build:\n    if: github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor')",
				"  build:\n    if: \""+condition+"\"", 1)

			findings := auditPermissionsDocument(t, "pages.yml", body)

			if len(findings) != 1 || findings[0].Job != "build" || findings[0].Scope != "contents" {
				t.Fatalf("expected one contents:write finding against build, got %v", findings)
			}
		})
	}
}

func TestAuditPullRequestPermissions_Positive_ReportsTheWriteAllShorthand(t *testing.T) {
	body := strings.Replace(theShapePagesShipsNow,
		"permissions:\n  contents: read", "permissions: write-all", 1)

	findings := auditPermissionsDocument(t, "pages.yml", body)

	if len(findings) != 1 || findings[0].Scope != allScopes {
		t.Fatalf("expected one write-all finding, got %v", findings)
	}
}

// Negative: the fixed shape is clean, a job that pull requests cannot reach may write, and
// a workflow no pull request starts is not audited at all.

func TestAuditPullRequestPermissions_Negative_AcceptsAWriteOnADeployOnlyJob(t *testing.T) {
	if findings := auditPermissionsDocument(t, "pages.yml", theShapePagesShipsNow); len(findings) != 0 {
		t.Fatalf("expected no findings from the fixed workflow, got %v", findings)
	}
}

func TestAuditPullRequestPermissions_Negative_IgnoresAWorkflowWithoutAPullRequestTrigger(t *testing.T) {
	body := strings.Replace(theShapePagesShipped,
		"  pull_request:\n    branches: [main]\n    paths: ['docs/**']\n", "  workflow_dispatch:\n", 1)

	if findings := auditPermissionsDocument(t, "adopt.yml", body); len(findings) != 0 {
		t.Fatalf("a dispatch-only workflow may write; got %v", findings)
	}
}

func TestAuditPullRequestPermissions_Negative_IgnoresReadOnlyScopes(t *testing.T) {
	body := strings.Replace(theShapePagesShipped,
		"  pages: write\n  id-token: write", "  pull-requests: read", 1)

	if findings := auditPermissionsDocument(t, "ci.yml", body); len(findings) != 0 {
		t.Fatalf("read scopes are not findings; got %v", findings)
	}
}

// Boundary: the job condition forms that decide reachability, an unparseable document, a
// job cap, an empty mapping, and a nil context.

// The table asserts the event list rather than a bare yes: the same answer decides whether
// a job is reported and which events the finding names, so a row that pinned only
// reachability would let the wording drift away from the condition it describes.
func TestAuditPullRequestPermissions_Boundary_JobConditionsDecideReachability(t *testing.T) {
	onlyPullRequest := []string{pullRequestEvent}
	onlyTarget := []string{pullRequestTargetEvent}
	both := []string{pullRequestEvent, pullRequestTargetEvent}
	none := []string(nil)
	cases := []struct {
		name      string
		condition string
		events    []string
		reaching  []string
	}{
		{"unconditional", "", onlyPullRequest, onlyPullRequest},
		{"negated event", "github.event_name != 'pull_request'", onlyPullRequest, none},
		{"other event only", "github.event_name == 'push'", onlyPullRequest, none},
		{"the pull request event itself", "github.event_name == 'pull_request'", onlyPullRequest, onlyPullRequest},
		{"double quotes", `github.event_name != "pull_request"`, onlyPullRequest, none},
		{"exclusion behind a guard group", "github.repository == (vars.X || 'a/b') && github.event_name != 'pull_request'", onlyPullRequest, none},
		{"a top-level disjunction stays undecided", "github.event_name != 'pull_request' || github.actor == 'bot'", onlyPullRequest, onlyPullRequest},
		{"an unrelated condition", "github.ref == 'refs/heads/main'", onlyPullRequest, onlyPullRequest},
		{"an unbalanced parenthesis does not swallow the rest", "github.event_name != 'pull_request') && true", onlyPullRequest, none},
		// The group used to be deleted along with the event test inside it, which reported
		// a correctly fenced job and failed the repository-wide guard below.
		{"a parenthesised event test still excludes", "(github.event_name != 'pull_request') && github.ref == 'refs/heads/main'", onlyPullRequest, none},
		{"a doubly parenthesised event test still excludes", "((github.event_name != 'pull_request'))", onlyPullRequest, none},
		{"a parenthesised comparison value still excludes", "github.event_name != ('pull_request')", onlyPullRequest, none},
		{"a group that is not one group decides nothing", "(github.event_name != 'pull_request') || (github.actor == 'bot')", onlyPullRequest, onlyPullRequest},
		// A group holding a top-level disjunction is the shape unwrapping alone got wrong:
		// the whole disjunction reached the comparison as one value, matched no event, and
		// the job went unreported although a pull request starts it.
		{"a disjunction group keeps the event it names", "(github.event_name == 'push' || github.event_name == 'pull_request')", onlyPullRequest, onlyPullRequest},
		{"a disjunction group fences off the event it does not name", "(github.event_name == 'push' || github.event_name == 'pull_request')", onlyTarget, none},
		{"a guarded disjunction group keeps the event it names", "(github.event_name == 'schedule' || github.event_name == 'pull_request') && github.ref == 'refs/heads/main'", both, onlyPullRequest},
		// pull_request_target reaches a job on a contributor's say-so exactly as
		// pull_request does, and a workflow declaring both is fenced off from neither by a
		// condition that names only one.
		{"the target event itself", "github.event_name == 'pull_request_target'", onlyTarget, onlyTarget},
		{"the target event in a target workflow is not an exclusion", "github.event_name == 'pull_request_target'", both, onlyTarget},
		{"excluding only pull_request leaves the target reachable", "github.event_name != 'pull_request'", both, onlyTarget},
		{"excluding the target leaves pull_request reachable", "github.event_name != 'pull_request_target'", onlyPullRequest, onlyPullRequest},
		{"excluding the target in a target workflow", "github.event_name != 'pull_request_target'", onlyTarget, none},
		{"another event in a target workflow", "github.event_name == 'push'", onlyTarget, none},
		// Each conjunct used to be judged against the whole declaration list, so neither
		// one excluded on its own and a job fenced off from both events was reported.
		{"both events fenced off leaves nothing reachable", "github.event_name != 'pull_request' && github.event_name != 'pull_request_target'", both, none},
		// A group holding a top-level conjunction is the same-repository guard. Unwrapped and
		// read as one comparison, its right-hand side "'pull_request' && ..." named no event,
		// so the equality test fenced off the very event it names and the job went unreported.
		{"a same-repository guard group keeps the event it names", "(github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository)", onlyPullRequest, onlyPullRequest},
		{"a same-repository guard group fences off the other event", "(github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name == github.repository)", both, onlyPullRequest},
		{"a conjunction group excludes through any of its parts", "(github.event_name != 'pull_request' && github.ref == 'refs/heads/main')", onlyPullRequest, none},
		{"a conjunction group naming another event excludes", "(github.event_name == 'push' && github.ref == 'refs/heads/main')", onlyPullRequest, none},
		// A disjunction part that holds a conjunction binds differently under either operator
		// precedence, and names the event under both; it is not read as one comparison.
		{"a conjunction inside a disjunction group decides nothing", "(github.event_name == 'push' || github.event_name == 'pull_request' && github.actor == 'bot')", onlyPullRequest, onlyPullRequest},
		// Only a single quoted literal names an event. Anything else is a value the file does
		// not decide, and reading it as "some other event" guessed the job away.
		{"a comparison against an expression decides nothing", "github.event_name == github.event.inputs.kind", onlyPullRequest, onlyPullRequest},
		{"an escaped quote stays inside the literal it escapes", "github.event_name == 'pull_request''s'", onlyPullRequest, none},
		{"a quote that ends the literal early is not escaped", "(github.event_name != 'pull_request''' && 'x')", onlyPullRequest, onlyPullRequest},
		// GitHub compares strings without regard to case.
		{"a differently cased event name still names the event", "github.event_name == 'Pull_Request'", onlyPullRequest, onlyPullRequest},
		{"a differently cased exclusion still excludes", "github.event_name != 'PULL_REQUEST'", onlyPullRequest, none},
		{"an empty literal names no event", "github.event_name == ''", onlyPullRequest, none},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			got := reachingEvents(testCase.condition, testCase.events)
			if strings.Join(got, ",") != strings.Join(testCase.reaching, ",") {
				t.Errorf("reachingEvents(%q, %v) = %v, want %v",
					testCase.condition, testCase.events, got, testCase.reaching)
			}
		})
	}
}

func TestAuditPullRequestPermissions_Boundary_AnEmptyMappingGrantsNothing(t *testing.T) {
	body := strings.Replace(theShapePagesShipped,
		"permissions:\n  contents: read\n  pages: write\n  id-token: write", "permissions: {}", 1)

	if findings := auditPermissionsDocument(t, "pages.yml", body); len(findings) != 0 {
		t.Fatalf("an empty mapping grants nothing; got %v", findings)
	}
}

func TestAuditPullRequestPermissions_Boundary_RejectsUnparseableWorkflows(t *testing.T) {
	if _, err := auditWorkflowPullRequestPermissions("broken.yml", []byte("jobs: [")); err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}

func TestAuditPullRequestPermissions_Boundary_RejectsAnOversizedJobInventory(t *testing.T) {
	var body strings.Builder
	body.WriteString("on: [pull_request]\npermissions:\n  contents: write\njobs:\n")
	for i := 0; i <= maxJobsPerFile; i++ {
		body.WriteString("  job")
		body.WriteString(string(rune('a' + i%26)))
		body.WriteString(string(rune('a' + i/26)))
		body.WriteString(":\n    runs-on: ubuntu-latest\n")
	}

	_, err := auditWorkflowPullRequestPermissions("many.yml", []byte(body.String()))
	if err == nil {
		t.Fatal("expected a job-count error, got nil")
	}
	if !strings.Contains(err.Error(), "exceeds 64 jobs") {
		t.Fatalf("the bound was not what refused it: %v", err)
	}
}

func TestAuditPullRequestPermissions_Boundary_RejectsAnAbsentContext(t *testing.T) {
	var absent context.Context
	if _, err := AuditPullRequestPermissions(absent, filepath.Join("..", "..")); err == nil {
		t.Fatal("expected a nil-context error, got nil")
	}
}

// Guard: this repository's own workflows satisfy the check. Without it the audit could be
// written, pass its fixtures, and never be pointed at the tree it exists to protect.
func TestAuditPullRequestPermissions_Guard_ThisRepositoryIsDisciplined(t *testing.T) {
	root := filepath.Join("..", "..")
	findings, err := AuditPullRequestPermissions(context.Background(), root)
	if err != nil {
		t.Fatalf("auditing this repository: %v", err)
	}
	for _, finding := range findings {
		t.Errorf("%s", finding)
	}
	// Without this the guard also passes when no workflow is read at all, which is the one
	// way a clean result means nothing.
	if audited := pullRequestWorkflowsIn(t, root); audited < 3 {
		t.Fatalf("expected several pull-request workflows to audit, saw %d", audited)
	}
}

// pullRequestWorkflowsIn counts the repository's workflows a pull request can start.
func pullRequestWorkflowsIn(t *testing.T, root string) int {
	t.Helper()
	files, err := readWorkflowFiles(t.Context(), root)
	if err != nil {
		t.Fatalf("reading workflows: %v", err)
	}
	audited := 0
	for i := 0; i < len(files) && i < maxWorkflowFiles; i++ {
		var spec workflowSpec
		if err := yaml.Unmarshal(files[i].Data, &spec); err != nil {
			t.Fatalf("parsing %s: %v", files[i].Name, err)
		}
		if len(pullRequestTriggers(&spec.On)) > 0 {
			audited++
		}
	}
	return audited
}
