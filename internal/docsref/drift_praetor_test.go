// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// praetorSurfaces loads the docs_surfaces this repository declares for itself.
func praetorSurfaces(t *testing.T) []config.DocsSurface {
	t.Helper()
	manifest, err := config.LoadManifest(filepath.Join("..", "..", config.ManifestFileName))
	if err != nil {
		t.Fatalf("load the repository manifest: %v", err)
	}
	if len(manifest.DocsSurfaces) == 0 {
		t.Fatal("the repository manifest declares no docs_surfaces")
	}
	return manifest.DocsSurfaces
}

// praetorFindings runs the drift check over changed against this repository's surfaces.
func praetorFindings(t *testing.T, changed ...string) []string {
	t.Helper()
	report, err := Drift(changed, praetorSurfaces(t), nil)
	if err != nil {
		t.Fatalf("Drift(%v): %v", changed, err)
	}
	return report.Findings
}

// wantOneFinding fails unless changed yields exactly one finding naming label.
func wantOneFinding(t *testing.T, label string, changed ...string) {
	t.Helper()
	found := praetorFindings(t, changed...)
	if len(found) != 1 || !strings.HasPrefix(found[0], label+": ") {
		t.Errorf("change %v: findings %q, want one for %q", changed, found, label)
	}
}

// wantClean fails unless changed yields no finding.
func wantClean(t *testing.T, changed ...string) {
	t.Helper()
	if found := praetorFindings(t, changed...); len(found) != 0 {
		t.Errorf("change %v: unexpected findings %q", changed, found)
	}
}

// Positive: each surface this repository declares requires its guide, and the same change
// with that guide is clean.
func TestPraetorSurfacesPositive(t *testing.T) {
	pairs := []struct{ label, surface, guide string }{
		{"archetype catalog", ".config/archetypes/os-image.yaml", "docs/guides/archetype-authoring.md"},
		{"facet catalog", ".config/archetypes/facets/security-high.yaml", "docs/guides/archetype-authoring.md"},
		{"flavor definitions", "internal/flavor/definitions.go", "docs/guides/onboarding.md"},
		{"effective policy resolution", "internal/config/effective_digest.go", "docs/guides/effective-policy.md"},
		{"effective policy resolution", "internal/config/effective.go", "docs/guides/effective-policy.md"},
		{"text register policy", "internal/config/register_emission.go", "docs/guides/text-register.md"},
		{"HISS rule matchers", "internal/hiss/go_callgraph.go", "docs/standards/hiss-01-recursion.md"},
		{"HISS-20 coverage engine", "internal/hisscoverage/engine.go", "docs/guides/hiss-coverage.md"},
		{"git hook scripts", ".config/lefthook/scripts/checkpoint_scope.py", "docs/guides/git-hooks.md"},
		{"hook configuration", "lefthook.yml", "docs/guides/git-hooks.md"},
		{"development MCP server", "cmd/standards-mcp/main.go", "docs/guides/development-mcp.md"},
		{"managed README governance contract", "cmd/standardsctl/audit_readme.go", "docs/guides/adoption-verification.md"},
		{"agent-hook entrypoint", "cmd/standardsctl/hook.go", "docs/guides/agent-hooks.md"},
		{"platform neutrality gate", "scripts/portability_selftest.py", "docs/standards/hiss-21-platform-neutrality.md"},
		{"workstation install/status", "scripts/dev_install.py", "docs/guides/workstation-update.md"},
		{"Markdown documentation governance", "tools/docsurface/catalog.mjs", "docs/guides/documentation-governance.md"},
		{"Markdown documentation governance", "internal/cifilter/filter.go", "docs/guides/documentation-governance.md"},
		{"documentation reference and drift gate", "internal/config/docs_surfaces.go", "docs/guides/documentation-drift.md"},
		{"documentation reference and drift gate", "cmd/standardsctl/docs_references.go", "docs/guides/documentation-drift.md"},
	}
	for _, pair := range pairs {
		wantOneFinding(t, pair.label, pair.surface)
		wantClean(t, pair.surface, pair.guide)
	}
}

// Negative: a change touching no declared surface is never accused, test files of a surface
// included, and neither a decision record nor an unrelated guide satisfies a surface.
func TestPraetorSurfacesNegative(t *testing.T) {
	wantClean(t, "internal/worktree/worktree.go", "internal/util/util.go", "cmd/standardsctl/main.go", "internal/adopt/adopt_test.go")
	wantClean(t, "internal/config/effective_digest_test.go", "internal/config/register_authority_test.go")
	wantClean(t, ".config/lefthook/scripts/test_hooks.py", ".config/lefthook/scripts/test_checkpoint.py")
	wantClean(t, "internal/agenthook/policy_test.go", "internal/docsref/drift_test.go")
	wantOneFinding(t, "archetype catalog", ".config/archetypes/web-package.yaml", "docs/adr/0200-some-decision.md")
	wantOneFinding(t, "effective policy resolution", "internal/config/effective_load.go", "docs/guides/onboarding.md")
	wantOneFinding(t, "text register policy", "internal/config/register.go", "docs/guides/effective-policy.md")
}

// Boundary: two unrelated surfaces in one change report separately, and an empty change is
// clean.
func TestPraetorSurfacesBoundary(t *testing.T) {
	if found := praetorFindings(t, ".config/archetypes/os-image.yaml", "lefthook.yml"); len(found) != 2 {
		t.Fatalf("two surfaces must report separately: %q", found)
	}
	wantClean(t)
}
