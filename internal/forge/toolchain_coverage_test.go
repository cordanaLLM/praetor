package forge

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// theActionThisRepositoryShipped is the adoption action's shape before the fix: the input
// docs/adoption.md tells adopters not to pass defaulted to a toolchain a minor behind the
// module, so every adopter run installed it.
const theActionThisRepositoryShipped = `
name: "Praetor Fast Adoption"
description: "Adopt a repository into Praetor governance"
inputs:
  go-version:
    description: "Go version to install"
    required: false
    default: "1.24"

runs:
  using: "composite"
  steps:
    - uses: actions/setup-go@v5
      with:
        go-version: ${{ inputs.go-version }}
`

// theActionWithNoDefault declares the same input without a default, so the file fixes no
// version of its own and there is nothing to compare.
const theActionWithNoDefault = `
name: "Go module and build cache"
description: "Restore the Go caches"
inputs:
  go-version:
    description: "Go toolchain version"
    required: true
`

// flowAndMatrixWorkflow writes the same key in the two forms a line scan cannot see: a
// flow mapping and a matrix list.
const flowAndMatrixWorkflow = `
name: Matrix
on: [pull_request]
jobs:
  verify:
    strategy:
      matrix:
        go-version: ['1.24', '1.27']
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with: {go-version: '1.24', cache: false}
`

// Positive: a shipped composite action is audited too. docs/adoption.md documents calling
// praetor-adopt without the go-version input, so its default is the toolchain every
// adopter's runner installs, and the value sits on a `default:` line the key is not on.
func TestAuditGoToolchain_Positive_ReportsACompositeActionInputDefault(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/actions/praetor-adopt/action.yml": theActionThisRepositoryShipped,
	})
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(findings), findings)
	}
	if findings[0].File != ".github/actions/praetor-adopt/action.yml" ||
		findings[0].Line != 8 || findings[0].Pin != "1.24" {
		t.Errorf("unexpected finding: %+v", findings[0])
	}
}

// Positive: a flow mapping and a matrix list pin the toolchain just as a block key does.
func TestAuditGoToolchain_Positive_ReportsFlowAndMatrixPins(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/workflows/matrix.yml": flowAndMatrixWorkflow,
	})
	if len(findings) != 2 {
		t.Fatalf("expected the matrix leg and the flow mapping, got %d: %v", len(findings), findings)
	}
	if findings[0].Line != 8 || findings[1].Line != 12 {
		t.Errorf("lines %d and %d do not point at the matrix leg and the flow mapping",
			findings[0].Line, findings[1].Line)
	}
	for _, finding := range findings {
		if finding.Pin != "1.24" {
			t.Errorf("unexpected finding: %+v", finding)
		}
	}
}

// Negative: an action input that declares no default, and a composite action that mirrors
// the directive, report nothing.
func TestAuditGoToolchain_Negative_AcceptsActionsThatFixNoStaleVersion(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/actions/go-cache/action.yml": theActionWithNoDefault,
		".github/actions/praetor-adopt/action.yml": strings.Replace(
			theActionThisRepositoryShipped, `"1.24"`, `"1.27"`, 1),
	})
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d: %v", len(findings), findings)
	}
}

// Negative: setup-go's documented version syntax is wider than a dotted number -- its
// README lists `1.25.x`, `1.x`, `1.24.0-rc.1`, `^1.25.1` and `stable`. A form that
// satisfies the directive must audit clean; refusing it turned an idiomatic pin into a
// hard gate failure on a repository that was never behind.
func TestAuditGoToolchain_Negative_AcceptsSetupGoVersionSyntax(t *testing.T) {
	accepted := map[string]string{
		".github/workflows/patch.yml":    strings.Replace(mirroredWorkflow, "'1.27'", "'1.27.x'", 1),
		".github/workflows/minor.yml":    strings.Replace(mirroredWorkflow, "'1.27'", "'1.x'", 1),
		".github/workflows/rc.yml":       strings.Replace(mirroredWorkflow, "'1.27'", "'1.28.0-rc.1'", 1),
		".github/workflows/alias.yml":    strings.Replace(mirroredWorkflow, "'1.27'", "stable", 1),
		".github/workflows/range.yml":    strings.Replace(mirroredWorkflow, "'1.27'", "'^1.27.1'", 1),
		".github/workflows/rcdirect.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'1.27.0-rc.1'", 1),
	}
	findings, err := AuditGoToolchain(context.Background(), toolchainRepository(t, "1.27", accepted))
	if err != nil {
		t.Fatalf("a documented setup-go version form failed the audit: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d: %v", len(findings), findings)
	}
}

// Boundary: a wildcard or prerelease pin below the directive is still a finding. The
// widened syntax must not become a way to hide a stale toolchain.
func TestAuditGoToolchain_Boundary_ReportsStaleWildcardAndPrereleasePins(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/workflows/wild.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'1.24.x'", 1),
		".github/workflows/rc.yml":   strings.Replace(mirroredWorkflow, "'1.27'", "'1.24.0-rc.1'", 1),
	})
	if len(findings) != 2 {
		t.Fatalf("expected both stale pins, got %d: %v", len(findings), findings)
	}
}

// Boundary: an inventory larger than the audit's bound is refused, not truncated. A
// truncating scan returns a clean report for a repository it never finished reading.
func TestAuditToolchainFiles_Boundary_RefusesAnOverSizedInventory(t *testing.T) {
	stale := workflowFile{
		Name: "templates/go/Dockerfile.tmpl",
		Data: []byte(theTemplateThisRepositoryShipped),
	}
	files := make([]workflowFile, maxAuditedFiles+1)
	for i := range files {
		files[i] = stale
	}
	if _, err := auditToolchainFiles(files, "1.27"); err == nil {
		t.Fatal("an inventory beyond the audit's bound was accepted")
	}
	within, err := auditToolchainFiles(files[:maxAuditedFiles], "1.27")
	if err != nil {
		t.Fatalf("auditing a full inventory: %v", err)
	}
	if len(within) != maxAuditedFiles {
		t.Fatalf("a full inventory reported %d findings, not one per document (%d)",
			len(within), maxAuditedFiles)
	}
}

// Guard: the audit reads all three directories it claims to cover. Without this, a rename
// or a move leaves the mirroring guard asserting over an empty list, which cannot fail.
func TestAuditGoToolchain_Guard_EveryAuditedDirectoryIsRead(t *testing.T) {
	documents, err := auditedDocuments(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("reading the audited documents: %v", err)
	}
	prefixes := []string{
		workflowsRelativePath + "/",
		actionsRelativePath + "/",
		templatesDirectory + "/",
	}
	for _, prefix := range prefixes {
		if !documentRead(documents, prefix) {
			t.Errorf("nothing under %s was audited; the directory moved or was renamed", prefix)
		}
	}
}

// documentRead reports whether any audited document under prefix carried content.
func documentRead(documents []workflowFile, prefix string) bool {
	for i := range documents {
		if strings.HasPrefix(documents[i].Name, prefix) && len(documents[i].Data) > 0 {
			return true
		}
	}
	return false
}
