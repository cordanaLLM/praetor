package templates_test

import (
	"maps"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/templates"
)

// renderedRoot holds, relative to this directory, the committed renderings of each YAML
// template whose actions go beyond a leading comment (renderedYAMLTemplates): one file per
// distinct rendering, at <renderedRoot>/<template path without .tmpl>/<variant><extension>. The
// yamllint gate (scripts/test_emitted_yaml_lint.py, make hooks-lint) lints these files, because
// it cannot lint a body that still carries actions. TestBranchingYAMLTemplateRenderingsAreCommitted
// keeps them equal to every rendering the template can produce.
const renderedRoot = "testdata/rendered"

// hostedGateVariant names the one rendering of a template whose only actions, beside a leading
// comment, render the hosted gate shape (templates/hostedgate.go): it reads no repository fact.
const hostedGateVariant = "default"

// hostedGateYAMLTemplates are the CI workflow templates that render the hosted gate shape and
// read no repository fact; the Node CI body renders it too and is a branching template.
var hostedGateYAMLTemplates = []string{
	"flutter/ci-flutter.yml.tmpl", "go/ci-go.yml.tmpl", "jvm/ci-jvm.yml.tmpl", "rust/ci-rust.yml.tmpl",
}

// branchingVariant is one Context a branching template renders against, and the file name its
// rendering is committed under.
type branchingVariant struct {
	name string
	ctx  templates.Context
}

// branchingYAMLTemplates maps each YAML template whose actions read repository facts to every
// combination of the values of the fields it reads.
func branchingYAMLTemplates() map[string][]branchingVariant {
	return map[string][]branchingVariant{
		"flutter/analysis_options.yaml.tmpl": dartLintVariants(),
		"node/ci-node.yml.tmpl":              nodeVariants(),
	}
}

// renderedYAMLTemplates maps each YAML template whose actions go beyond a leading comment to the
// contexts it renders against: every branching template's variants (branchingYAMLTemplates), and
// one sample context for each body that renders the hosted gate shape alone.
func renderedYAMLTemplates() map[string][]branchingVariant {
	rendered := branchingYAMLTemplates()
	for _, name := range hostedGateYAMLTemplates {
		rendered[name] = []branchingVariant{{name: hostedGateVariant, ctx: sampleContext}}
	}
	return rendered
}

func dartLintVariants() []branchingVariant {
	variants := make([]branchingVariant, 0, 3)
	for _, lints := range []string{"", "lints", "flutter_lints"} {
		ctx := sampleContext
		ctx.DartLints = lints
		name := lints
		if name == "" {
			name = "none"
		}
		variants = append(variants, branchingVariant{name: name, ctx: ctx})
	}
	return variants
}

// nodeVariants is every manager with every combination of the Yarn facts, so a fact that
// changed another manager's body would add a rendering the committed set lacks.
func nodeVariants() []branchingVariant {
	variants := make([]branchingVariant, 0, 32)
	for _, manager := range []string{"npm", "pnpm", "yarn", "bun"} {
		for flags := range 8 {
			node := templates.NodeContext{Manager: manager, YarnBerry: flags&4 != 0, Lint: flags&2 != 0, Build: flags&1 != 0}
			ctx := sampleContext
			ctx.Node = node
			variants = append(variants, branchingVariant{name: nodeVariantName(node), ctx: ctx})
		}
	}
	return variants
}

func nodeVariantName(node templates.NodeContext) string {
	parts := []string{node.Manager}
	for _, fact := range []struct {
		set   bool
		label string
	}{{node.YarnBerry, "berry"}, {node.Lint, "lint"}, {node.Build, "build"}} {
		if fact.set {
			parts = append(parts, fact.label)
		}
	}
	return strings.Join(parts, "-")
}

// distinctRenderings renders every variant of one template and returns {file name: body} for
// each distinct body, named after the first variant producing it.
func distinctRenderings(t *testing.T, name string, variants []branchingVariant) map[string]string {
	t.Helper()
	extension := path.Ext(strings.TrimSuffix(name, ".tmpl"))
	files := map[string]string{}
	var seen []string
	for _, variant := range variants {
		body, err := templates.RenderFile(name, variant.ctx)
		if err != nil {
			t.Fatalf("render %s as %s: %v", name, variant.name, err)
		}
		if slices.Contains(seen, body) {
			continue
		}
		seen = append(seen, body)
		files[variant.name+extension] = body
	}
	return files
}

// strayRenderings returns the entries of a rendering directory that no variant produces.
func strayRenderings(entries []os.DirEntry, want map[string]string) []string {
	var stray []string
	for _, entry := range entries {
		if _, ok := want[entry.Name()]; !ok {
			stray = append(stray, entry.Name())
		}
	}
	return stray
}

// Positive: every distinct rendering of each branching template, and the one rendering of each
// hosted gate CI template, is committed byte for byte, and negative: a committed file no variant
// renders any longer is stale. Regenerate with
// PRAETOR_UPDATE_GOLDEN=1 go test ./templates -run TestBranchingYAMLTemplateRenderingsAreCommitted
// and delete what the stale check names.
func TestBranchingYAMLTemplateRenderingsAreCommitted(t *testing.T) {
	for name, variants := range renderedYAMLTemplates() {
		dir := filepath.Join(renderedRoot, filepath.FromSlash(strings.TrimSuffix(name, ".tmpl")))
		want := distinctRenderings(t, name, variants)
		for file, body := range want {
			testsupport.AssertGolden(t, filepath.Join(dir, file), body)
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("list %s: %v", dir, err)
		}
		if stray := strayRenderings(entries, want); len(stray) > 0 {
			t.Errorf("%s holds renderings no variant of %s produces: %v", dir, name, stray)
		}
	}
}

// Boundary: the variants reach every branch, and only the facts a manager reads split its
// body: three Dart configs; npm, pnpm and Bun one job each whatever the Yarn facts say; Yarn
// one job per combination of its three facts.
func TestBranchingYAMLTemplateVariantsReachEveryBranch(t *testing.T) {
	templatesByName := branchingYAMLTemplates()
	dart := distinctRenderings(t, "flutter/analysis_options.yaml.tmpl", templatesByName["flutter/analysis_options.yaml.tmpl"])
	if got := slices.Sorted(maps.Keys(dart)); !slices.Equal(got, []string{"flutter_lints.yaml", "lints.yaml", "none.yaml"}) {
		t.Errorf("Dart renderings = %v", got)
	}
	node := distinctRenderings(t, "node/ci-node.yml.tmpl", templatesByName["node/ci-node.yml.tmpl"])
	want := []string{"bun.yml", "npm.yml", "pnpm.yml", "yarn-berry-build.yml", "yarn-berry-lint-build.yml", "yarn-berry-lint.yml",
		"yarn-berry.yml", "yarn-build.yml", "yarn-lint-build.yml", "yarn-lint.yml", "yarn.yml"}
	if got := slices.Sorted(maps.Keys(node)); !slices.Equal(got, want) {
		t.Errorf("Node renderings = %v, want %v", got, want)
	}
}

// Negative: a committed file that no variant renders is reported, and boundary: a directory
// holding exactly the renderings reports nothing.
func TestStrayRenderings(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"npm.yml", "deno.yml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := strayRenderings(entries, map[string]string{"npm.yml": "---\n"}); !slices.Equal(got, []string{"deno.yml"}) {
		t.Errorf("stray renderings = %v, want [deno.yml]", got)
	}
	if got := strayRenderings(entries, map[string]string{"npm.yml": "", "deno.yml": ""}); len(got) != 0 {
		t.Errorf("stray renderings = %v, want none", got)
	}
}
