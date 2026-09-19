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

func TestAuditPullRequestPermissions_Boundary_JobConditionsDecideReachability(t *testing.T) {
	cases := []struct {
		name      string
		condition string
		reachable bool
	}{
		{"unconditional", "", true},
		{"negated event", "github.event_name != 'pull_request'", false},
		{"other event only", "github.event_name == 'push'", false},
		{"the pull request event itself", "github.event_name == 'pull_request'", true},
		{"double quotes", `github.event_name != "pull_request"`, false},
		{"exclusion behind a guard group", "github.repository == (vars.X || 'a/b') && github.event_name != 'pull_request'", false},
		{"a top-level disjunction stays undecided", "github.event_name != 'pull_request' || github.actor == 'bot'", true},
		{"an unrelated condition", "github.ref == 'refs/heads/main'", true},
		{"an unbalanced parenthesis does not swallow the rest", "github.event_name != 'pull_request') && true", false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if got := reachableFromPullRequest(testCase.condition); got != testCase.reachable {
				t.Errorf("reachableFromPullRequest(%q) = %t, want %t",
					testCase.condition, got, testCase.reachable)
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
		if triggersOnPullRequest(&spec.On) {
			audited++
		}
	}
	return audited
}
