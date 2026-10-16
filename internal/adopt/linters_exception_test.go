// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

// Positive: fresh adoption writes exceptions entries for declared linters that adoption
// scaffolds no CI jobs for, and the declared-linters gate passes with excused lines.
func TestAdopt_DeclaredLintersExceptionsFreshAdoption(t *testing.T) {
	ctx := t.Context()
	repoPath := newTestRepo(t, "fresh-adopt-linters")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\n\nfunc main() {}\n")

	source := newLintersLockSource(t)
	rep := adoptWithSource(t, repoPath, source, false)

	manifestData, err := os.ReadFile(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := config.DecodeManifest(manifestData)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}

	today := time.Now()
	if err := config.ValidateExceptions(manifest.Exceptions, today); err != nil {
		t.Fatalf("exceptions in fresh manifest failed validation: %v", err)
	}

	linterExceptions := config.ExceptionsFor(manifest.Exceptions, config.ExceptionRuleDeclaredLinters)
	if len(linterExceptions) == 0 {
		t.Fatalf("expected declared-linters exceptions in fresh adoption, got none")
	}

	for i := 0; i < len(linterExceptions) && i < config.MaxExceptions; i++ {
		entry := linterExceptions[i]
		if !strings.Contains(entry.Reason, "adoption scaffolds no CI job for") {
			t.Errorf("entry %s has unexpected reason: %s", entry.Tool, entry.Reason)
		}
	}

	policy := rep.EffectivePolicy
	if policy == nil {
		t.Fatalf("expected EffectivePolicy in adoption report, got nil")
	}

	out, err := AuditDeclaredLinters(ctx, DeclaredLintersOptions{
		RootDir:    repoPath,
		Policy:     policy,
		Exceptions: manifest.Exceptions,
		Today:      today,
	})
	if err != nil {
		t.Fatalf("AuditDeclaredLinters failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "[PASS]") || !strings.Contains(out, "excepted") {
		t.Fatalf("expected audit to pass with excepted lines, got:\n%s", out)
	}
}

// Negative: a fixture whose workflow already runs gitleaks gets no gitleaks exception entry.
func TestAdopt_DeclaredLintersWorkflowRunsLinterGetsNoEntry(t *testing.T) {
	repoPath := newTestRepo(t, "workflow-runs-linter")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\n\nfunc main() {}\n")
	mustWrite(t, filepath.Join(repoPath, ".github", "workflows", "security.yml"), `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: gitleaks detect --verbose
`)

	source := newLintersLockSource(t)
	adoptWithSource(t, repoPath, source, false)
	exceptions := readDeclaredLinterExceptions(t, repoPath)
	if len(exceptions) == 0 {
		t.Fatalf("expected declared-linters exceptions for other linters, got none")
	}
	for i := 0; i < len(exceptions) && i < config.MaxExceptions; i++ {
		if exceptions[i].Tool == "gitleaks" {
			t.Fatalf("found gitleaks exception when workflow already runs it: %+v", exceptions[i])
		}
	}
}

// Re-adoption keeps existing live entries without duplication.
func TestAdopt_DeclaredLintersReAdoptPreservesEntries(t *testing.T) {
	repoPath := newTestRepo(t, "readopt-preserves")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\n\nfunc main() {}\n")

	source := newLintersLockSource(t)
	adoptWithSource(t, repoPath, source, false)
	initialExceptions := readDeclaredLinterExceptions(t, repoPath)
	if len(initialExceptions) == 0 {
		t.Fatalf("expected initial declared-linters exceptions, got 0")
	}

	adoptWithSource(t, repoPath, source, true)
	readoptExceptions := readDeclaredLinterExceptions(t, repoPath)
	if len(readoptExceptions) != len(initialExceptions) {
		t.Fatalf("re-adoption duplicated or dropped entries: before %d, after %d", len(initialExceptions), len(readoptExceptions))
	}
}

// Re-adoption drops entries for linters that are now run by workflows.
func TestAdopt_DeclaredLintersReAdoptDropsWhenWorkflowAdded(t *testing.T) {
	repoPath := newTestRepo(t, "readopt-drops")
	mustWrite(t, filepath.Join(repoPath, "main.go"), "package main\n\nfunc main() {}\n")

	source := newLintersLockSource(t)
	adoptWithSource(t, repoPath, source, false)
	initialExceptions := readDeclaredLinterExceptions(t, repoPath)
	initialCount := len(initialExceptions)
	if initialCount == 0 {
		t.Fatalf("expected initial declared-linters exceptions, got 0")
	}

	mustWrite(t, filepath.Join(repoPath, ".github", "workflows", "security.yml"), `name: Security
on: push
jobs:
  lint:
    runs-on: ubuntu-latest
    steps:
      - run: gitleaks detect --verbose
`)
	adoptWithSource(t, repoPath, source, true)
	afterExceptions := readDeclaredLinterExceptions(t, repoPath)
	for i := 0; i < len(afterExceptions) && i < config.MaxExceptions; i++ {
		if afterExceptions[i].Tool == "gitleaks" {
			t.Fatalf("gitleaks exception not dropped after adding workflow: %+v", afterExceptions[i])
		}
	}
	if len(afterExceptions) != initialCount-1 {
		t.Fatalf("expected count %d after dropping gitleaks, got %d", initialCount-1, len(afterExceptions))
	}
}

func newLintersLockSource(t *testing.T) string {
	t.Helper()
	bodies := map[string]string{
		"security:high": "id: \"security:high\"\nname: \"Security High\"\nlinters:\n  - gitleaks\n  - semgrep\n  - trivy\n",
		"template-seed": "id: \"template-seed\"\nname: \"Template Seed\"\nlinters:\n  - govet\n",
	}
	return newCatalogLockSource(t, &config.Manifest{Version: 1,
		Profiles: []string{"framework", "template-seed", "native-gpu-systems", "app-service", "os-image"},
		Facets:   []string{"security:high", "api:public-contract", "docs:seo-portal", "agent:sandboxed", "custom:facet"}}, bodies)
}

func readDeclaredLinterExceptions(t *testing.T, repoPath string) []config.Exception {
	t.Helper()
	manifestData, err := os.ReadFile(filepath.Join(repoPath, manifestFile))
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	manifest, err := config.DecodeManifest(manifestData)
	if err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	return config.ExceptionsFor(manifest.Exceptions, config.ExceptionRuleDeclaredLinters)
}
