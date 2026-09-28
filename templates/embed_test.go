package templates_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/templates"
)

// sampleContext is what flavor scaffolding passes to a body that reads nothing from the
// repository: its identity. A body that does (TemplateItem.Resolve in internal/flavor) renders
// the zero value of those facts here.
var sampleContext = templates.Context{RepoName: "widget", Owner: "acme"}

// The Dart analyzer config includes the rule set Context.DartLints names, and no include
// otherwise (BUG-1010).
func TestRenderFile_DartAnalysisIncludesOnlyTheNamedLintPackage(t *testing.T) {
	for lints, include := range map[string]string{
		// Positive: each lint package flavor apply can name.
		"flutter_lints": "include: package:flutter_lints/flutter.yaml\n",
		"lints":         "include: package:lints/recommended.yaml\n",
		// Boundary: the zero value, and negative: a value no branch knows.
		"":              "",
		"pedantic_mono": "",
	} {
		ctx := sampleContext
		ctx.DartLints = lints
		body, err := templates.RenderFile("flutter/analysis_options.yaml.tmpl", ctx)
		if err != nil {
			t.Fatalf("render %q: %v", lints, err)
		}
		if hasInclude := strings.Contains("\n"+body, "\ninclude:"); hasInclude != (include != "") {
			t.Errorf("%q: include present = %v, want %v:\n%s", lints, hasInclude, include != "", body)
		}
		if !strings.Contains(body, include) {
			t.Errorf("%q: body lacks %q:\n%s", lints, include, body)
		}
		if !strings.Contains(body, "\nlinter:\n  rules:\n") {
			t.Errorf("%q: body lost its linter rules:\n%s", lints, body)
		}
	}
}

// The Node CI body installs with the manager Context.Node names (BUG-1011).
func TestRenderFile_NodeCIInstallsWithTheNamedManager(t *testing.T) {
	render := func(node templates.NodeContext) string {
		t.Helper()
		ctx := sampleContext
		ctx.Node = node
		body, err := templates.RenderFile("node/ci-node.yml.tmpl", ctx)
		if err != nil {
			t.Fatalf("render %+v: %v", node, err)
		}
		return body
	}
	// Positive: each manager's locked install, and no npm install beside another manager's.
	for manager, install := range map[string]string{
		"npm":  "run: npm ci\n",
		"pnpm": "run: pnpm install --frozen-lockfile\n",
		"yarn": "run: yarn install --frozen-lockfile\n",
		"bun":  "run: bun install --frozen-lockfile\n",
	} {
		body := render(templates.NodeContext{Manager: manager})
		if !strings.Contains(body, install) {
			t.Errorf("%s: body lacks %q:\n%s", manager, install, body)
		}
		if manager != "npm" && strings.Contains(body, "npm ci") {
			t.Errorf("%s: body still installs with npm ci:\n%s", manager, body)
		}
	}
	// Boundary: the zero value is the npm job, byte for byte.
	if zero, npm := render(templates.NodeContext{}), render(templates.NodeContext{Manager: "npm"}); zero != npm {
		t.Errorf("the zero NodeContext does not render the npm job:\n%s", zero)
	}
	// Boundary: Yarn 2+ switches the flag, and Yarn runs lint and build only where they exist.
	berry := render(templates.NodeContext{Manager: "yarn", YarnBerry: true, Build: true})
	if !strings.Contains(berry, "run: yarn install --immutable\n") || !strings.Contains(berry, "run: yarn run build\n") {
		t.Errorf("Yarn 2+ body lacks --immutable or its build step:\n%s", berry)
	}
	// Negative: a script package.json lacks gets no Yarn step, and the Yarn-only facts change
	// nothing for another manager.
	if strings.Contains(berry, "yarn run lint") {
		t.Errorf("Yarn body runs a lint script package.json lacks:\n%s", berry)
	}
	if pnpm := render(templates.NodeContext{Manager: "pnpm", YarnBerry: true, Lint: true}); pnpm != render(templates.NodeContext{Manager: "pnpm"}) {
		t.Errorf("Yarn-only facts changed the pnpm body:\n%s", pnpm)
	}
}

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
	if !strings.HasPrefix(golangci, "---\nversion: \"2\"\n") {
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
