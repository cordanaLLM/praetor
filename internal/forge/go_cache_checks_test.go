package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// theShapeThisRepositoryShipped is the workflow pair that made CI Enforcement spend 688s
// per run: both jobs leave the cache to setup-go, whose key carries no job name, so the
// short job wins the race to write it and the long one restores objects it cannot use.
const theShapeThisRepositoryShipped = `
name: Enforcement
on: [pull_request]
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - run: go test -race ./...
  compliance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: true
      - run: go run ./cmd/standardsctl audit
`

const disciplinedShape = `
name: Enforcement
on: [pull_request]
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: false
      - uses: ./.github/actions/go-cache
        with:
          job: ci-verify
          go-version: '1.27'
  compliance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.27'
          cache: false
      - uses: ./.github/actions/go-cache
        with:
          job: compliance
          go-version: '1.27'
`

// collidingShape names one build cache from two jobs, which is the same defect by hand.
const collidingShape = `
name: Enforcement
on: [pull_request]
jobs:
  verify:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with: {go-version: '1.27', cache: false}
      - uses: ./.github/actions/go-cache
        with: {job: shared, go-version: '1.27'}
  compliance:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with: {go-version: '1.27', cache: false}
      - uses: ./.github/actions/go-cache
        with: {job: shared, go-version: '1.27'}
`

// noGoAtAll must be ignored entirely: the rule is about Go build caches, not about jobs.
const noGoAtAll = `
name: Docs
on: [pull_request]
jobs:
  spelling:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: make docs
`

func auditDocument(t *testing.T, name, body string) []GoCacheFinding {
	t.Helper()
	findings, err := auditWorkflowGoCaches(name, []byte(body), make(map[string]string))
	if err != nil {
		t.Fatalf("audit %s: %v", name, err)
	}
	return findings
}

// Positive: the defect this check exists for is reported, and reported for both jobs.
// Without this case the check could pass everything and nobody would notice.
func TestAuditGoBuildCaches_Positive_ReportsTheSharedSetupGoCache(t *testing.T) {
	findings := auditDocument(t, "ci.yml", theShapeThisRepositoryShipped)
	if len(findings) != 2 {
		t.Fatalf("expected one finding per job, got %d: %v", len(findings), findings)
	}
	for _, finding := range findings {
		if !strings.Contains(finding.Detail, "setup-go still owns the caches") {
			t.Errorf("finding does not name the cause: %s", finding)
		}
	}
}

func TestAuditGoBuildCaches_Positive_ReportsTwoJobsNamingOneCache(t *testing.T) {
	findings := auditDocument(t, "ci.yml", collidingShape)
	if len(findings) != 1 {
		t.Fatalf("expected exactly one collision, got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Detail, "already claimed by ci.yml/compliance") {
		t.Errorf("collision does not name the other job: %s", findings[0])
	}
}

func TestAuditGoBuildCaches_Positive_ReportsAJobWithNoBuildCache(t *testing.T) {
	body := strings.ReplaceAll(theShapeThisRepositoryShipped, "cache: true", "cache: false")
	findings := auditDocument(t, "ci.yml", body)
	if len(findings) != 2 {
		t.Fatalf("expected one finding per uncached job, got %d: %v", len(findings), findings)
	}
	if !strings.Contains(findings[0].Detail, "restores no build cache of its own") {
		t.Errorf("unexpected detail: %s", findings[0])
	}
}

// Negative: a disciplined pair reports nothing.
func TestAuditGoBuildCaches_Negative_AcceptsPerJobCaches(t *testing.T) {
	if findings := auditDocument(t, "ci.yml", disciplinedShape); len(findings) != 0 {
		t.Fatalf("disciplined workflow reported %d findings: %v", len(findings), findings)
	}
}

// Boundary: no Go, no opinion; and an empty document is not an error.
func TestAuditGoBuildCaches_Boundary_IgnoresJobsWithoutGo(t *testing.T) {
	if findings := auditDocument(t, "docs.yml", noGoAtAll); len(findings) != 0 {
		t.Fatalf("non-Go workflow reported %d findings: %v", len(findings), findings)
	}
	if findings := auditDocument(t, "empty.yml", "name: Empty\non: [push]\n"); len(findings) != 0 {
		t.Fatalf("empty workflow reported %d findings: %v", len(findings), findings)
	}
}

// Boundary: the same job seen twice keeps its own cache rather than colliding with itself.
func TestAuditGoBuildCaches_Boundary_AJobDoesNotCollideWithItself(t *testing.T) {
	owner := make(map[string]string)
	for pass := 0; pass < 2; pass++ {
		findings, err := auditWorkflowGoCaches("ci.yml", []byte(disciplinedShape), owner)
		if err != nil {
			t.Fatalf("pass %d: %v", pass, err)
		}
		if len(findings) != 0 {
			t.Fatalf("pass %d reported %d findings: %v", pass, len(findings), findings)
		}
	}
}

func TestAuditGoBuildCaches_Boundary_RejectsUnparseableWorkflows(t *testing.T) {
	if _, err := auditWorkflowGoCaches("broken.yml", []byte("jobs: [unclosed"), make(map[string]string)); err == nil {
		t.Fatal("expected a parse error, got nil")
	}
}

// Guard: this repository's own workflows must satisfy the rule. This is what stops the
// defect returning the next time a workflow is added with setup-go's default caching.
func TestAuditGoBuildCaches_Guard_ThisRepositoryIsDisciplined(t *testing.T) {
	findings, err := AuditGoBuildCaches(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("auditing the repository: %v", err)
	}
	for _, finding := range findings {
		t.Errorf("%s", finding)
	}
}

// Guard: the workflows praetor ships to adopters must satisfy it too, or every adopted
// repository inherits the defect. They carry the cache inline because an adopter has no
// copy of praetor's composite action.
func TestAuditGoBuildCaches_Guard_ShippedTemplatesAreDisciplined(t *testing.T) {
	directory := filepath.Join("..", "..", "templates", "go")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("reading shipped templates: %v", err)
	}
	owner := make(map[string]string)
	audited := 0
	for i := 0; i < len(entries) && i < maxWorkflowFiles; i++ {
		name := entries[i].Name()
		if !strings.HasSuffix(name, ".yml.tmpl") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(directory, name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		findings, err := auditWorkflowGoCaches(name, body, owner)
		if err != nil {
			t.Fatalf("auditing %s: %v", name, err)
		}
		for _, finding := range findings {
			t.Errorf("%s", finding)
		}
		audited++
	}
	// Without this the test passes when the templates are renamed or removed.
	if audited < 2 {
		t.Fatalf("expected at least two shipped workflow templates, audited %d", audited)
	}
}

// Boundary: the audit bounds its own I/O rather than trusting the caller's context,
// matching RequiredStatusContexts, and refuses a nil one outright.
func TestAuditGoBuildCaches_Boundary_RejectsAnAbsentContext(t *testing.T) {
	var absent context.Context
	if _, err := AuditGoBuildCaches(absent, filepath.Join("..", "..")); err == nil {
		t.Fatal("expected a nil-context error, got nil")
	}
}
