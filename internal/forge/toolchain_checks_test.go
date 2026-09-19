package forge

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// theWorkflowThisRepositoryShipped is the adoption workflow's shape before the fix: the
// module required 1.27 and the job that builds the CLI set up 1.24, twice.
const theWorkflowThisRepositoryShipped = `
name: Praetor Fast Adoption Bot
on: [workflow_dispatch]
jobs:
  adopt:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/setup-go@v5
        with:
          go-version: '1.24'
          cache: false
      - uses: ./.github/actions/go-cache
        with:
          job: adopt
          go-version: '1.24'
`

const mirroredWorkflow = `
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
`

// theTemplateThisRepositoryShipped is the distroless template's build stage before the
// fix: every adopter scaffolding it got a builder a minor behind the module.
const theTemplateThisRepositoryShipped = `# Build stage
FROM golang:1.24-bookworm AS builder
WORKDIR /src
`

// toolchainRepository writes a repository whose go.mod declares directive and whose files
// are laid out at the given repository-relative paths.
func toolchainRepository(t *testing.T, directive string, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	manifest := "module example.com/adopter\n\ngo " + directive + "\n"
	if directive == "" {
		manifest = "module example.com/adopter\n"
	}
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("writing go.mod: %v", err)
	}
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("creating %s: %v", name, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("writing %s: %v", name, err)
		}
	}
	return root
}

func auditToolchain(t *testing.T, directive string, files map[string]string) []ToolchainFinding {
	t.Helper()
	findings, err := AuditGoToolchain(context.Background(), toolchainRepository(t, directive, files))
	if err != nil {
		t.Fatalf("auditing: %v", err)
	}
	return findings
}

// Positive: the defect this check exists for is reported, once per stale key, with the
// file and line a reader can open.
func TestAuditGoToolchain_Positive_ReportsAWorkflowBehindTheDirective(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/workflows/adopt.yml": theWorkflowThisRepositoryShipped,
		".github/workflows/ci.yml":    mirroredWorkflow,
	})
	if len(findings) != 2 {
		t.Fatalf("expected one finding per stale key, got %d: %v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.File != ".github/workflows/adopt.yml" || finding.Pin != "1.24" || finding.Directive != "1.27" {
			t.Errorf("unexpected finding: %+v", finding)
		}
	}
	if findings[0].Line != 10 || findings[1].Line != 15 {
		t.Errorf("lines %d and %d do not point at the two go-version keys", findings[0].Line, findings[1].Line)
	}
	if !strings.Contains(findings[0].String(), "adopt.yml:10: pins Go 1.24") {
		t.Errorf("finding does not name file, line and pin: %s", findings[0])
	}
}

// Positive: a shipped template is audited too, because its pin becomes every adopter's.
func TestAuditGoToolchain_Positive_ReportsAShippedTemplateBehindTheDirective(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		"templates/go/Dockerfile.distroless.tmpl": theTemplateThisRepositoryShipped,
	})
	if len(findings) != 1 {
		t.Fatalf("expected one finding, got %d: %v", len(findings), findings)
	}
	if findings[0].File != "templates/go/Dockerfile.distroless.tmpl" || findings[0].Line != 2 || findings[0].Pin != "1.24" {
		t.Errorf("unexpected finding: %+v", findings[0])
	}
}

// Negative: a repository whose copies mirror the directive reports nothing, and neither
// does a workflow that reads the version out of go.mod instead of restating it.
func TestAuditGoToolchain_Negative_AcceptsMirroredPins(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/workflows/ci.yml":       mirroredWorkflow,
		".github/workflows/security.yml": strings.Replace(mirroredWorkflow, "go-version: '1.27'", "go-version-file: 'go.mod'", 1),
		"templates/go/Dockerfile.tmpl":   strings.Replace(theTemplateThisRepositoryShipped, "1.24", "1.27", 1),
	})
	if len(findings) != 0 {
		t.Fatalf("mirrored repository reported %d findings: %v", len(findings), findings)
	}
}

// Boundary: a workflow that sets no Go up, a value an expression supplies, a moving tag
// and a repository with neither workflows nor templates are all outside the check's reach
// -- there is no version in the file to compare.
func TestAuditGoToolchain_Boundary_IgnoresWhatDeclaresNoVersion(t *testing.T) {
	findings := auditToolchain(t, "1.27", map[string]string{
		".github/workflows/docs.yml":    "name: Docs\non: [push]\njobs:\n  build:\n    runs-on: ubuntu-latest\n    steps:\n      - run: mkdocs build\n",
		".github/workflows/reuse.yml":   strings.Replace(mirroredWorkflow, "'1.27'", "${{ inputs.go-version }}", 1),
		"templates/go/Dockerfile.tmpl":  "FROM golang:latest AS builder\n",
		"templates/node/Dockerfile.tmp": "FROM node:22-bookworm AS builder\n",
	})
	if len(findings) != 0 {
		t.Fatalf("expected no findings, got %d: %v", len(findings), findings)
	}
	if bare := auditToolchain(t, "1.27", nil); len(bare) != 0 {
		t.Fatalf("a repository with no workflows reported %d findings: %v", len(bare), bare)
	}
}

// Boundary: the comparison is on version components, not text. A patch level satisfies a
// minor directive, a newer minor satisfies an older one, and a minor-only pin is below a
// directive that names a patch level.
func TestAuditGoToolchain_Boundary_ComparesVersionComponents(t *testing.T) {
	satisfying := map[string]string{
		".github/workflows/patch.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'1.27.1'", 1),
		".github/workflows/newer.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'1.28'", 1),
		".github/workflows/major.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'2.0'", 1),
	}
	if findings := auditToolchain(t, "1.27", satisfying); len(findings) != 0 {
		t.Fatalf("newer pins reported %d findings: %v", len(findings), findings)
	}
	behind := auditToolchain(t, "1.27.1", map[string]string{".github/workflows/ci.yml": mirroredWorkflow})
	if len(behind) != 1 || behind[0].Pin != "1.27" || behind[0].Directive != "1.27.1" {
		t.Fatalf("a minor-only pin below a patch-level directive was not reported: %v", behind)
	}
}

// Boundary: what the audit cannot decide fails it. A manifest with no directive, a pin
// that is not a version, a missing manifest and an absent context are errors, never an
// empty finding list a caller would read as a clean repository.
func TestAuditGoToolchain_Boundary_RefusesWhatItCannotCompare(t *testing.T) {
	_, err := AuditGoToolchain(context.Background(), toolchainRepository(t, "", nil))
	if err == nil {
		t.Error("a manifest without a go directive was accepted")
	}
	malformed := toolchainRepository(t, "1.27", map[string]string{
		".github/workflows/ci.yml": strings.Replace(mirroredWorkflow, "'1.27'", "'1.x'", 1),
	})
	if _, err = AuditGoToolchain(context.Background(), malformed); err == nil {
		t.Error("a pin that is not a dotted number was accepted")
	} else if !strings.Contains(err.Error(), "ci.yml:10") {
		t.Errorf("the error does not point at the pin: %v", err)
	}
	if _, err = AuditGoToolchain(context.Background(), filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Error("a repository without a go.mod was accepted")
	}
	var absent context.Context
	if _, err = AuditGoToolchain(absent, filepath.Join("..", "..")); err == nil {
		t.Error("expected a nil-context error, got nil")
	}
}

// Guard: this repository's own workflows and the templates it ships must mirror its
// directive. This is what stops the copies drifting again the next time go.mod moves.
func TestAuditGoToolchain_Guard_ThisRepositoryMirrorsItsDirective(t *testing.T) {
	findings, err := AuditGoToolchain(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("auditing the repository: %v", err)
	}
	for _, finding := range findings {
		t.Errorf("%s", finding)
	}
}

// Guard: the audit reads the repository's own templates, so a rename or a move that left
// it auditing nothing would be a silent pass rather than a failure.
func TestAuditGoToolchain_Guard_TheShippedTemplatesAreAudited(t *testing.T) {
	files, err := readTemplateFiles(context.Background(), filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("reading shipped templates: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no container template was audited; templates/*/Dockerfile* moved or was renamed")
	}
	for i := range files {
		if !strings.HasPrefix(files[i].Name, "templates/") || len(files[i].Data) == 0 {
			t.Errorf("unexpected template entry: %q (%d bytes)", files[i].Name, len(files[i].Data))
		}
	}
}
