// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

// fixtureArchetypeGitleaks is a facet declaring gitleaks.
const fixtureArchetypeGitleaks = `id: "security:high"
name: "High security"
linters:
  - "gitleaks"
`

// fixtureWorkflowWithGitleaks is a workflow with a run step executing gitleaks.
const fixtureWorkflowWithGitleaks = `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - name: Run Gitleaks
        run: gitleaks detect --verbose
`

// fixtureWorkflowWithoutGitleaks is a workflow with no step running gitleaks.
const fixtureWorkflowWithoutGitleaks = `name: CI
on: push
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Build
        run: echo "building..."
`

func writeLinterFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestAuditDeclaredLinters_RedGreen proves cordanaLLM/praetor#1289:
// A repository declaring gitleaks with a workflow step running it passes,
// and the same without the step fails with a finding naming gitleaks and its source.
func TestAuditDeclaredLinters_RedGreen(t *testing.T) {
	ctx := t.Context()

	t.Run("fails when declared linter is run by no workflow", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".github/workflows/ci.yml", fixtureWorkflowWithoutGitleaks)

		policy := &config.EffectivePolicy{
			CatalogArtifacts: []config.PolicyArtifact{
				{
					RelativePath: ".config/archetypes/facets/security-high.yaml",
					Content:      []byte(fixtureArchetypeGitleaks),
				},
			},
		}

		out, err := AuditDeclaredLinters(ctx, root, policy)
		if err == nil {
			t.Fatalf("expected audit failure when gitleaks is not run, got pass: %s", out)
		}
		if !strings.Contains(err.Error(), "gitleaks") {
			t.Fatalf("error should name missing linter gitleaks, got: %v", err)
		}
		if !strings.Contains(err.Error(), `facet "security:high"`) {
			t.Fatalf("error should name declaration source facet \"security:high\", got: %v", err)
		}
	})

	t.Run("passes when declared linter is run by workflow", func(t *testing.T) {
		root := t.TempDir()
		writeLinterFixtureFile(t, root, ".github/workflows/security.yml", fixtureWorkflowWithGitleaks)

		policy := &config.EffectivePolicy{
			CatalogArtifacts: []config.PolicyArtifact{
				{
					RelativePath: ".config/archetypes/facets/security-high.yaml",
					Content:      []byte(fixtureArchetypeGitleaks),
				},
			},
		}

		out, err := AuditDeclaredLinters(ctx, root, policy)
		if err != nil {
			t.Fatalf("expected audit pass when gitleaks is run, got error: %v", err)
		}
		if !strings.Contains(out, "[PASS]") {
			t.Fatalf("expected [PASS] verdict line, got: %s", out)
		}
	})
}
