package flavor_test

// The bodies an adopter actually receives. Before this file the only assertion on a
// scaffolded template was os.Stat on the path (flavor_test.go, TestApplyFlavor_NewArchetypes),
// so every version, schema and image reference inside a scaffolded file could change, or
// rot, without a test noticing. HISS-20 wants the claim replayable in both directions: each
// case below fails if the value regresses AND fails if the value is merely absent.
//
// Every body now comes from templates/ (TemplateItem.Source), so the file a reviewer reads
// there is the file an adopter receives. TestScaffoldedBodiesAreTheShippedTemplates holds
// that equality for every flavor; the remaining cases pin what the bodies must say.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/templates"
)

// digestPinned matches a FROM line carrying tag plus an explicit sha256 digest, optionally
// naming the stage.
var digestPinned = regexp.MustCompile(`^FROM \S+:[^@\s]+@sha256:[0-9a-f]{64}(?: AS \S+)?$`)

// scaffoldInto applies a flavor to an empty repository and returns the file bodies.
//
// The report is read, not discarded: ApplyFlavor returns (report, nil) even when
// applySingleTemplate recorded a mkdir or write failure in report.Errors, so a helper that
// drops it reports a write failure as "this flavor scaffolds no such file" and hides the
// recorded cause.
func scaffoldInto(t *testing.T, flavorName string, want ...string) map[string]string {
	t.Helper()
	root := t.TempDir()
	report, err := flavor.ApplyFlavor(t.Context(), root, flavorName, false)
	if err != nil {
		t.Fatalf("apply %s: %v", flavorName, err)
	}
	if len(report.Errors) > 0 {
		t.Fatalf("apply %s recorded scaffolding errors: %s", flavorName, strings.Join(report.Errors, "; "))
	}
	bodies := make(map[string]string, len(want))
	for _, rel := range want {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("%s scaffolded no %s (created: %v, skipped: %v): %v", flavorName, rel, report.CreatedTemplates, report.SkippedTemplates, err)
		}
		bodies[rel] = string(data)
	}
	return bodies
}

// fromLines returns every FROM instruction of a Dockerfile, in order.
func fromLines(body string) []string {
	var froms []string
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(line, "FROM ") {
			froms = append(froms, line)
		}
	}
	return froms
}

// BUG-581: the scaffolded Dockerfile was a single distroless stage copying a binary that
// nothing had built into the context, so `docker build .` failed on every adopter. It must
// now build from source: a Go builder stage whose output the distroless runtime copies.
func TestScaffoldedDockerfileBuildsFromSourceOnADigestPinnedDebian13Runtime(t *testing.T) {
	body := scaffoldInto(t, "go-service", "Dockerfile")["Dockerfile"]
	froms := fromLines(body)
	if len(froms) != 2 {
		t.Fatalf("expected a builder and a runtime stage, got FROM lines %q in:\n%s", froms, body)
	}
	for _, from := range froms {
		if !digestPinned.MatchString(from) {
			t.Errorf("HISS-11: stage is not tag-plus-digest pinned: %q", from)
		}
	}
	if !strings.HasPrefix(froms[0], "FROM golang:") || !strings.HasSuffix(froms[0], " AS builder") {
		t.Errorf("first stage is not the Go builder: %q", froms[0])
	}
	if !strings.Contains(froms[1], "gcr.io/distroless/static-debian13:nonroot") {
		t.Errorf("runtime stage is not the Debian 13 distroless image: %q", froms[1])
	}
	for _, want := range []string{"RUN CGO_ENABLED=0", "COPY --from=builder ", "USER 65532:65532"} {
		if !strings.Contains(body, want) {
			t.Errorf("emitted Dockerfile lacks %q:\n%s", want, body)
		}
	}
	// Negative: the rolling alias resolves to the same digest today and diverges the
	// day distroless promotes it, so its absence is the property under test.
	if strings.Contains(body, "gcr.io/distroless/static:nonroot") {
		t.Errorf("emitted Dockerfile fell back to the rolling static alias: %q", body)
	}
}

func TestScaffoldedGolangciConfigCarriesTheV2Schema(t *testing.T) {
	body := scaffoldInto(t, "go-service", ".golangci.yml")[".golangci.yml"]
	if !strings.HasPrefix(body, "version: \"2\"\n") {
		t.Errorf("golangci-lint v2 rejects a configuration without a leading version key: %q", body)
	}
	for _, v1Only := range []string{"exclude-use-default", "max-issues-per-linter", "max-same-issues"} {
		if strings.Contains(body, v1Only) {
			t.Errorf("v1-only key %q survived in a v2 configuration", v1Only)
		}
	}
	// The correctness subset the scaffold promises, named one by one so dropping any
	// single linter is a failing test rather than a silent narrowing.
	for _, linter := range []string{"govet", "staticcheck", "errcheck", "errorlint", "nilerr", "unused", "ineffassign", "bodyclose", "noctx"} {
		if !strings.Contains(body, "    - "+linter+"\n") {
			t.Errorf("enabled set lost %q", linter)
		}
	}
}

// Every scaffolded file is its embedded template rendered for the repository, byte for byte.
// This replaces a single-file equality test between templates/go/.golangci.yml.tmpl and a Go
// string literal, a pair that had already drifted once (#336): there is no second copy left.
func TestScaffoldedBodiesAreTheShippedTemplates(t *testing.T) {
	sourced := 0
	for _, flv := range flavor.List() {
		root := t.TempDir()
		report, err := flavor.ApplyFlavor(t.Context(), root, flv.Name(), false)
		if err != nil || len(report.Errors) > 0 {
			t.Fatalf("apply %s: err %v, recorded %v", flv.Name(), err, report.Errors)
		}
		for _, tmpl := range flv.RequiredTemplates() {
			if tmpl.Source == "" {
				continue
			}
			sourced++
			got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(tmpl.Path)))
			if err != nil {
				t.Errorf("%s scaffolded no %s: %v", flv.Name(), tmpl.Path, err)
				continue
			}
			// With no origin remote, ResolveRepoIdentity reads <owner>/<repo> off the path.
			identity := templates.Context{RepoName: filepath.Base(root), Owner: filepath.Base(filepath.Dir(root))}
			want, err := templates.RenderFile(tmpl.Source, identity)
			if err != nil {
				t.Fatalf("render %s: %v", tmpl.Source, err)
			}
			if string(got) != want {
				t.Errorf("%s: scaffolded %s differs from %s", flv.Name(), tmpl.Path, tmpl.Source)
			}
		}
	}
	if sourced == 0 {
		t.Fatal("no template declares a Source; the equality would hold vacuously")
	}
}

func TestScaffoldedRustfmtNamesThe2024Edition(t *testing.T) {
	body := scaffoldInto(t, "rust-systems", "rustfmt.toml")["rustfmt.toml"]
	if !strings.Contains(body, "edition = \"2024\"\n") {
		t.Errorf("rustfmt.toml is not on the 2024 edition: %q", body)
	}
	if strings.Contains(body, "edition = \"2021\"") {
		t.Errorf("rustfmt.toml still carries the 2021 edition: %q", body)
	}
}

// BUG-028/BUG-029: the workflows were one-line comments. They are workflows now, and the
// GitHub expressions in them reach the adopter verbatim rather than as template actions.
func TestScaffoldedWorkflowsCarryJobsAndGitHubExpressions(t *testing.T) {
	bodies := scaffoldInto(t, "go-service", ".github/workflows/ci.yml", ".github/workflows/security.yml")
	for rel, body := range bodies {
		if !strings.Contains(body, "\njobs:\n") {
			t.Errorf("%s declares no jobs: %q", rel, body)
		}
		if strings.HasPrefix(body, "# "+filepath.Base(rel)+" configuration for ") {
			t.Errorf("%s is still the comment placeholder: %q", rel, body)
		}
		if !strings.Contains(body, "${{ runner.os }}") {
			t.Errorf("%s lost its GitHub expressions in rendering", rel)
		}
	}
	for _, name := range []string{"rust-systems", "typescript-node", "jvm-service", "mobile-flutter"} {
		body := scaffoldInto(t, name, ".github/workflows/ci.yml")[".github/workflows/ci.yml"]
		if !strings.Contains(body, "\njobs:\n") || !strings.Contains(body, "runs-on: ubuntu-26.04") {
			t.Errorf("%s scaffolds a CI workflow without a pinned job: %q", name, body)
		}
	}
}

// #410: a comment-only .gitleaks.toml replaced gitleaks' built-in rules with none. The
// scaffolded file extends them.
func TestScaffoldedGitleaksConfigExtendsTheDefaultRules(t *testing.T) {
	body := scaffoldInto(t, "native-gpu-systems", ".gitleaks.toml")[".gitleaks.toml"]
	if !strings.Contains(body, "\n[extend]\nuseDefault = true\n") {
		t.Errorf("scaffolded gitleaks config does not extend the default rules: %q", body)
	}
}
