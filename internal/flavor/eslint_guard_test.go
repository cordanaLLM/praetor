package flavor

import (
	"path/filepath"
	"strings"
	"testing"
)

// legacyESLintNames are the eslintrc spellings ESLint v10 cannot load.
var legacyESLintNames = map[string]bool{
	".eslintrc": true, ".eslintrc.js": true, ".eslintrc.cjs": true,
	".eslintrc.json": true, ".eslintrc.yaml": true, ".eslintrc.yml": true,
}

// Negative: no flavor may scaffold or require a legacy eslintrc file. The shipped catalog already
// enforces this for profiles; flavors are a second surface that guard never read, which is how
// both JavaScript flavors kept scaffolding a format praetor itself refuses (#137, BUG-953).
// AltPaths may still name them: accepting an existing file asks nobody to create one.
func TestFlavors_Negative_NoFlavorScaffoldsLegacyESLintConfig(t *testing.T) {
	flavors := List()
	if len(flavors) == 0 {
		t.Fatal("no flavors registered, so this guard would pass having checked nothing")
	}
	for _, f := range flavors {
		for _, tmpl := range f.RequiredTemplates() {
			if legacyESLintNames[filepath.Base(tmpl.Path)] {
				t.Errorf("flavor %s scaffolds %s, a legacy eslintrc file ESLint v10 cannot load", f.Name(), tmpl.Path)
			}
		}
	}
}

// Negative, and the broader defect found alongside #137: a template with no content case falls
// through to a "# ... configuration" default. For a JavaScript or TypeScript file that is not a
// comment but a syntax error -- frontend-svelte scaffolded exactly that as .eslintrc.cjs.
func TestFlavors_Negative_NoJavaScriptTemplateFallsThroughToACommentDefault(t *testing.T) {
	scripts := map[string]bool{".js": true, ".mjs": true, ".cjs": true, ".ts": true, ".mts": true, ".cts": true}
	for _, f := range List() {
		for _, tmpl := range f.RequiredTemplates() {
			if !scripts[filepath.Ext(tmpl.Path)] || tmpl.ContentFunc != nil {
				continue
			}
			body := templateContent(tmpl, "repo", "owner")
			if strings.HasPrefix(strings.TrimSpace(body), "#") {
				t.Errorf("flavor %s scaffolds %s starting with '#', which is a JavaScript syntax error", f.Name(), tmpl.Path)
			}
		}
	}
}

// Positive: the scaffolded flat config is what @eslint/js documents. plugins must name js or
// extends: ["js/recommended"] resolves to nothing, so both are asserted, not just the import.
func TestESLintFlatConfig_Positive_ResolvesTheRecommendedConfig(t *testing.T) {
	for _, want := range []string{`import js from "@eslint/js"`, `plugins: { js }`, `extends: ["js/recommended"]`, "export default"} {
		if !strings.Contains(eslintFlatConfig, want) {
			t.Errorf("scaffolded flat config lacks %q", want)
		}
	}
	if eslintConfigPath != "eslint.config.mjs" {
		t.Errorf("scaffold path %q is not unconditionally ESM", eslintConfigPath)
	}
}

// Boundary: the scaffolded path is not also listed as its own alternative, and every flat-config
// name ESLint searches is either the path or an alternative, so no conforming repository is missed.
func TestESLintAlternatives_Boundary_CoverEveryFlatConfigName(t *testing.T) {
	accepted := map[string]bool{eslintConfigPath: true}
	for _, alt := range eslintConfigAlternatives {
		if alt == eslintConfigPath {
			t.Errorf("%s is listed as its own alternative", alt)
		}
		accepted[alt] = true
	}
	for _, name := range []string{"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs",
		"eslint.config.ts", "eslint.config.mts", "eslint.config.cts"} {
		if !accepted[name] {
			t.Errorf("flat-config name %s is neither scaffolded nor accepted", name)
		}
	}
}
