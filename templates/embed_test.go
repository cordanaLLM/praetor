package templates_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/templates"
)

// sampleContext is what flavor scaffolding passes: a repository identity and nothing else.
var sampleContext = templates.Context{RepoName: "widget", Owner: "acme"}

func TestRender_Positive(t *testing.T) {
	text := "# {{ .Owner }}/{{ .RepoName }}\nArchetype: {{ .Archetype }}\nRunner: {{ .RunnerTag }}"
	ctx := templates.Context{
		RepoName:  "praetor",
		Owner:     "cordanaLLM",
		Archetype: "framework",
		RunnerTag: "arc-runner-set-linux-amd64",
	}
	res, err := templates.Render("test", text, ctx)
	if err != nil {
		t.Fatalf("expected template render to succeed: %v", err)
	}
	if !strings.Contains(res, "# cordanaLLM/praetor") {
		t.Errorf("expected rendered owner/repo, got: %s", res)
	}
	if !strings.Contains(res, "Runner: arc-runner-set-linux-amd64") {
		t.Errorf("expected rendered runner tag, got: %s", res)
	}
}

func TestRender_Negative_And_Boundary(t *testing.T) {
	ctx := templates.Context{RepoName: "praetor"}
	if _, err := templates.Render("invalid", "{{ .RepoName ", ctx); err == nil {
		t.Error("expected syntax error on unclosed action, got nil")
	}
	if _, err := templates.Render("missing", "{{ range .UnknownField }}{{ end }}", ctx); err == nil {
		t.Error("expected execution error on missing field, got nil")
	}
	// BUG-582: templates/agent named {{.Repo}}, a field Context never declared, so they
	// could not render even had anything read them.
	if _, err := templates.Render("repo", "{{ .Repo }}", ctx); err == nil {
		t.Error("expected an undeclared field to fail rendering, got nil")
	}
	if _, err := templates.Render("empty", "", ctx); err == nil {
		t.Error("expected error on empty template string, got nil")
	}
}

// Positive: every embedded body renders against the context scaffolding passes, is not
// empty, and leaves no action delimiter behind. This is the gate for BUG-582's class of
// defect: a body naming an undeclared field fails here, not in an adopter's repository.
func TestEveryShippedTemplateRenders(t *testing.T) {
	names, err := templates.Names()
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(names) < 10 {
		t.Fatalf("expected the shipped template tree, listed only %v", names)
	}
	for _, name := range names {
		body, err := templates.RenderFile(name, sampleContext)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if strings.TrimSpace(body) == "" {
			t.Errorf("%s renders to an empty file", name)
		}
		for _, delim := range []string{templates.FileLeftDelim, templates.FileRightDelim} {
			if strings.Contains(body, delim) {
				t.Errorf("%s leaves %q in the rendered body", name, delim)
			}
		}
	}
}

// Boundary: go:embed skips dot-files under a directory pattern, and several shipped bodies
// are dot-files. Losing them would make RenderFile fail only at scaffold time.
func TestNames_Boundary_IncludesDotFiles(t *testing.T) {
	names, err := templates.Names()
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	for _, want := range []string{"go/.golangci.yml.tmpl", "go/.gosec.json.tmpl", "native/.gitleaks.toml.tmpl", "osimage/.yamllint.yml.tmpl"} {
		if !slices.Contains(names, want) {
			t.Errorf("embedded tree lacks %s: %v", want, names)
		}
	}
}

// Boundary: GitHub Actions expressions use "${{ }}", which text/template's default
// delimiters would parse as actions. They must reach the adopter verbatim.
func TestRenderFile_Boundary_GitHubExpressionsSurvive(t *testing.T) {
	body, err := templates.RenderFile("go/ci-go.yml.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render ci workflow: %v", err)
	}
	for _, expression := range []string{"${{ runner.os }}", "${{ hashFiles('**/go.sum') }}", "${{ github.sha }}"} {
		if !strings.Contains(body, expression) {
			t.Errorf("rendered workflow lost %q", expression)
		}
	}
}

// Positive: a template comment carries a maintainer's note without emitting it, and a file
// action substitutes the repository identity.
func TestRenderFile_Positive_CommentsVanishAndIdentityRenders(t *testing.T) {
	golangci, err := templates.RenderFile("go/.golangci.yml.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render golangci config: %v", err)
	}
	if !strings.HasPrefix(golangci, "version: \"2\"\n") {
		t.Errorf("template comment leaked into the rendered body: %q", golangci)
	}
	gitleaks, err := templates.RenderFile("native/.gitleaks.toml.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render gitleaks config: %v", err)
	}
	if !strings.Contains(gitleaks, "acme/widget") {
		t.Errorf("repository identity did not render: %q", gitleaks)
	}
}

// Negative: a name outside the embedded tree is an error, not an empty body.
func TestRenderFile_Negative_UnknownTemplate(t *testing.T) {
	for _, name := range []string{"go/absent.tmpl", "", "../go.mod", "embed.go"} {
		if body, err := templates.RenderFile(name, sampleContext); err == nil {
			t.Errorf("RenderFile(%q) = %q, want an error", name, body)
		}
	}
}
