package flavor_test

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// ciWorkflowStub is the placeholder the scaffolder used to write at .github/workflows/ci.yml.
const ciWorkflowStub = "# ci.yml configuration for acme/widget\n"

// missingPaths names the templates an audit reported missing.
func missingPaths(report *flavor.FlavorAuditReport) []string {
	paths := make([]string, 0, len(report.MissingTemplates))
	for _, tmpl := range report.MissingTemplates {
		paths = append(paths, tmpl.Path)
	}
	return paths
}

// BUG-028/BUG-029: the comment placeholder scored as the workflow it stood in for. It is a
// missing template now, and a missing template fails the audit whatever the score.
func TestAuditFlavor_Negative_CommentStubTemplateIsMissing(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, map[string]string{".github/workflows/ci.yml": ciWorkflowStub})
	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if got := missingPaths(report); !slices.Equal(got, []string{".github/workflows/ci.yml"}) {
		t.Fatalf("want exactly the stubbed workflow missing, got %v", got)
	}
	if report.Passed {
		t.Fatalf("a repository whose CI workflow is a comment must fail, got %+v", report)
	}
}

// Regression guard for #379's file policy: a directory at .github/workflows/ci.yml configures
// nothing, so it must stay a missing template now that readRequiredFile decides what counts.
// It is not a fix of this change: main's util.FileExists already refused directories. The
// test pins that behavior through the new code path.
func TestAuditFlavor_Negative_DirectoryAtWorkflowPathIsMissing(t *testing.T) {
	emptyPATH(t)
	repo := conformingGoLibrary(t, nil)
	workflow := filepath.Join(repo, ".github", "workflows", "ci.yml")
	if err := os.Remove(workflow); err != nil {
		t.Fatalf("remove workflow: %v", err)
	}
	if err := os.MkdirAll(workflow, 0o700); err != nil {
		t.Fatalf("mkdir at the workflow path: %v", err)
	}
	report, err := flavor.AuditFlavor(repo, "go-library")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if got := missingPaths(report); !slices.Equal(got, []string{".github/workflows/ci.yml"}) || report.Passed {
		t.Fatalf("a directory is not a workflow: missing %v, passed %v", got, report.Passed)
	}
}

// symlinkOrSkip creates a symbolic link or skips: creating one needs a privilege Windows
// grants only in developer mode, and a check that cannot run there is skipped, not passed.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symbolic links unavailable on this host (%v); the symlink policy is covered where they are", err)
	}
}

// Boundary: the symlink policy readRequiredFile shares with settings. A link resolving to a
// valid file inside the repository counts; the same file reached outside it does not.
func TestTemplateSatisfied_Boundary_SymlinkPolicy(t *testing.T) {
	item := flavor.TemplateItem{Path: ".github/workflows/ci.yml", Validator: func(content []byte) bool {
		return strings.Contains(string(content), "jobs:")
	}}
	repo := repoWithFiles(t, map[string]string{"ci/workflow.yml": fixtureWorkflow})
	if err := os.MkdirAll(filepath.Join(repo, ".github", "workflows"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	link := filepath.Join(repo, ".github", "workflows", "ci.yml")
	symlinkOrSkip(t, filepath.Join(repo, "ci", "workflow.yml"), link)
	if !flavor.TemplateSatisfied(repo, item) {
		t.Error("a link to a valid workflow inside the repository must satisfy the template")
	}

	outside := filepath.Join(t.TempDir(), "workflow.yml")
	if err := os.WriteFile(outside, []byte(fixtureWorkflow), 0o600); err != nil {
		t.Fatalf("write the escape target: %v", err)
	}
	if err := os.Remove(link); err != nil {
		t.Fatalf("remove link: %v", err)
	}
	symlinkOrSkip(t, outside, link)
	if flavor.TemplateSatisfied(repo, item) {
		t.Error("a link resolving outside the repository must not satisfy the template")
	}
}

// Boundary: apply never writes the canonical name beside an alternative the repository
// already carries, even one the audit does not accept. A comment-only tsconfig.base.json
// fails carriesCode and a link out of the repository fails the confinement policy, yet each
// is the file the toolchain reads: a scaffolded rival beside it is the contradictory second
// config AltPaths exists to prevent. The audit still reports the template missing.
func TestApplyFlavor_Boundary_AlternativeTheAuditRejectsBlocksARival(t *testing.T) {
	repo := repoWithFiles(t, map[string]string{"tsconfig.base.json": "// compiler options live upstream\n"})
	shared := filepath.Join(t.TempDir(), "eslint.config.js")
	if err := os.WriteFile(shared, []byte("export default [];\n"), 0o600); err != nil {
		t.Fatalf("write the shared config: %v", err)
	}
	symlinkOrSkip(t, shared, filepath.Join(repo, "eslint.config.js"))
	report, err := flavor.ApplyFlavor(t.Context(), repo, "typescript-node", false)
	if err != nil || len(report.Errors) > 0 {
		t.Fatalf("apply: err %v, recorded %v", err, report.Errors)
	}
	for _, rival := range []string{"tsconfig.json", "eslint.config.mjs"} {
		if _, err := os.Stat(filepath.Join(repo, rival)); !os.IsNotExist(err) {
			t.Errorf("apply wrote %s beside the alternative in use: %v", rival, err)
		}
		if !slices.Contains(report.SkippedTemplates, rival) {
			t.Errorf("%s not reported skipped: %+v", rival, report)
		}
	}
	audit, err := flavor.AuditFlavor(repo, "typescript-node")
	if err != nil {
		t.Fatalf("audit: %v", err)
	}
	if got := missingPaths(audit); !slices.Contains(got, "tsconfig.json") || !slices.Contains(got, "eslint.config.mjs") {
		t.Errorf("the audit accepted alternatives it cannot vouch for: missing %v", got)
	}
}

// Positive: go-library's manifest and lockfile belong to adoption. Flavor apply reports them
// as deferred to it instead of writing the comment placeholders it used to.
func TestApplyFlavor_Positive_DefersProducerOwnedTemplates(t *testing.T) {
	repo := t.TempDir()
	report, err := flavor.ApplyFlavor(t.Context(), repo, "go-library", false)
	if err != nil || len(report.Errors) > 0 {
		t.Fatalf("apply: err %v, recorded %v", err, report.Errors)
	}
	want := []string{".standards.yaml (praetorctl adopt)", ".standards.lock (praetorctl adopt)"}
	if !slices.Equal(report.DeferredTemplates, want) {
		t.Fatalf("deferred %v, want %v", report.DeferredTemplates, want)
	}
	for _, name := range []string{".standards.yaml", ".standards.lock"} {
		if _, err := os.Stat(filepath.Join(repo, name)); !os.IsNotExist(err) {
			t.Errorf("flavor apply wrote producer-owned %s: %v", name, err)
		}
	}
	if !slices.Contains(report.CreatedTemplates, ".github/workflows/ci.yml") {
		t.Errorf("the CI workflow was not scaffolded: %+v", report)
	}
}

// Boundary: --force replaces scaffolded files, never a producer-owned one. It used to swap
// a real .standards.yaml for the one-line placeholder.
func TestApplyFlavor_Boundary_ForceKeepsAProducerOwnedFile(t *testing.T) {
	manifest := "version: 1\nprofiles: [framework]\n"
	repo := repoWithFiles(t, map[string]string{".standards.yaml": manifest})
	if _, err := flavor.ApplyFlavor(t.Context(), repo, "go-library", true); err != nil {
		t.Fatalf("apply --force: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".standards.yaml"))
	if err != nil || string(data) != manifest {
		t.Fatalf("--force changed .standards.yaml: %q, %v", data, err)
	}
}

// #410, end to end: the scaffolded config reports a known secret, and the comment-only
// placeholder it replaced reports nothing, which is why validGitleaksConfig refuses it.
func TestScaffoldedGitleaksConfigDetectsAKnownProbe(t *testing.T) {
	scanner, err := exec.LookPath("gitleaks")
	if err != nil {
		t.Skip("gitleaks is not on PATH; validGitleaksConfig covers the configuration shape on every host")
	}
	body, err := templates.RenderFile("native/.gitleaks.toml.tmpl", templates.Context{RepoName: "widget", Owner: "acme"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	dir := t.TempDir()
	scaffolded, stub := filepath.Join(dir, "scaffolded.toml"), filepath.Join(dir, "stub.toml")
	for name, content := range map[string]string{scaffolded: body, stub: "# .gitleaks.toml configuration for acme/widget\n"} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if !gitleaksReportsProbe(t, scanner, dir, scaffolded) {
		t.Error("the scaffolded config did not report the probe secret")
	}
	if gitleaksReportsProbe(t, scanner, dir, stub) {
		t.Error("the comment-only config reported the probe; the #410 premise no longer holds")
	}
}

// gitleaksReportsProbe scans a synthetic AWS access key id through gitleaks stdin with one
// config. The key is assembled at run time so this source file carries no secret-shaped
// literal for the repository's own scan to report.
func gitleaksReportsProbe(t *testing.T, scanner, dir, config string) bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	defer cancel()
	probe := "AWS_ACCESS_KEY_ID=" + "AKIA" + "ABCDEFGHIJKLMNOP" + "\n"
	var stdout bytes.Buffer
	stderr, err := util.RunCommandStream(ctx, dir, scanner, strings.NewReader(probe), &stdout, 1<<16,
		"stdin", "--no-banner", "--redact=100", "--log-level=warn", "--config", config)
	found := strings.Contains(string(stderr)+stdout.String(), "leaks found")
	if err != nil && !found {
		t.Fatalf("gitleaks failed without reporting a finding: %v\n%s", err, stderr)
	}
	return found
}
