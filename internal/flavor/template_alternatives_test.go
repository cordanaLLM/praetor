package flavor_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/flavor"
)

// A repository is conforming when it carries the modern spelling of a config file.
// ESLint 9 replaced .eslintrc.json with eslint.config.*, and a workspace commonly keeps
// its compiler options in tsconfig.base.json; demanding the older name failed repositories
// that are correctly configured.
func TestTemplateSatisfied_AcceptsAlternatives(t *testing.T) {
	item := flavor.TemplateItem{
		Path:     ".eslintrc.json",
		AltPaths: []string{"eslint.config.js", "eslint.config.ts"},
	}

	for _, name := range []string{".eslintrc.json", "eslint.config.js", "eslint.config.ts"} {
		tmp := t.TempDir()
		if err := os.WriteFile(filepath.Join(tmp, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		if !flavor.TemplateSatisfied(tmp, item) {
			t.Errorf("%s present but template reported missing", name)
		}
	}
}

func TestTemplateSatisfied_Negative(t *testing.T) {
	tmp := t.TempDir()
	item := flavor.TemplateItem{Path: ".eslintrc.json", AltPaths: []string{"eslint.config.js"}}
	if flavor.TemplateSatisfied(tmp, item) {
		t.Error("empty repository satisfied a required template")
	}
	// A similarly-named file that is not an accepted alternative must not satisfy it.
	if err := os.WriteFile(filepath.Join(tmp, "eslint.config.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if flavor.TemplateSatisfied(tmp, item) {
		t.Error("unlisted eslint.config.json satisfied the template")
	}
}

// Boundary: no alternatives declared falls back to exact-path behaviour. A directory at the
// template path is covered by TestTemplateSatisfied_Negative_DirectoryIsNotATemplate.
func TestTemplateSatisfied_Boundary(t *testing.T) {
	tmp := t.TempDir()
	bare := flavor.TemplateItem{Path: "tsconfig.json"}
	if flavor.TemplateSatisfied(tmp, bare) {
		t.Error("missing file with no AltPaths reported present")
	}
	if err := os.WriteFile(filepath.Join(tmp, "tsconfig.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !flavor.TemplateSatisfied(tmp, bare) {
		t.Error("present file with no AltPaths reported missing")
	}

	empty := flavor.TemplateItem{Path: "tsconfig.json", AltPaths: []string{}}
	if !flavor.TemplateSatisfied(tmp, empty) {
		t.Error("empty AltPaths slice changed exact-path behaviour")
	}
}

// The typescript-node flavor must accept a flat-config ESM workspace, which is what
// every current SvelteKit/Vite monorepo looks like.
func TestTypeScriptNodeFlavor_AcceptsFlatConfigWorkspace(t *testing.T) {
	tmp := t.TempDir()
	for _, f := range []string{"package.json", "tsconfig.base.json", "eslint.config.ts"} {
		if err := os.WriteFile(filepath.Join(tmp, f), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tmp, ".github", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".github", "workflows", "ci.yml"), []byte("name: CI\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := flavor.AuditFlavor(tmp, "typescript-node")
	if err != nil {
		t.Fatalf("AuditFlavor: %v", err)
	}
	if report.TemplatesPresent != report.TemplatesTotal {
		t.Errorf("templates %d/%d present, missing %v",
			report.TemplatesPresent, report.TemplatesTotal, report.MissingTemplates)
	}
}
