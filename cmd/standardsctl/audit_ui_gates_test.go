// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/templates"
)

const validUIRepoMakefile = ".PHONY: verify-all ui-gates\n" +
	"verify-all: ui-gates\n\n" +
	"ui-gates:\n" +
	"\t@$$($(PRAETORCTL) ui env) && npx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts\n"

func writeUIGateRepoFixture(t *testing.T, withSpecs bool) string {
	t.Helper()
	dir := t.TempDir()
	pkgJSON := `{"name":"ui-app","devDependencies":{"@axe-core/playwright":"4.11.1","@playwright/test":"1.52.0"}}`
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte(pkgJSON), 0o600); err != nil {
		t.Fatalf("write package.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "svelte.config.js"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatalf("write svelte.config.js: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte(validUIRepoMakefile), 0o600); err != nil {
		t.Fatalf("write Makefile: %v", err)
	}
	if !withSpecs {
		return dir
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o750); err != nil {
		t.Fatalf("mkdir tests: %v", err)
	}
	ctx := templates.Context{RepoName: "ui-app", Owner: "acme", UIRoutes: templates.Routes("/", "/settings")}
	a11y, err := templates.RenderFile(flavor.UIA11ySpecTemplate, ctx)
	if err != nil {
		t.Fatalf("render a11y spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, flavor.UIA11ySpecPath), []byte(a11y), 0o600); err != nil {
		t.Fatalf("write a11y spec: %v", err)
	}
	resp, err := templates.RenderFile(flavor.UIResponsivenessSpecTemplate, ctx)
	if err != nil {
		t.Fatalf("render responsiveness spec: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, flavor.UIResponsivenessSpecPath), []byte(resp), 0o600); err != nil {
		t.Fatalf("write responsiveness spec: %v", err)
	}
	return dir
}

func TestAuditUIGates_MissingSpecsFails(t *testing.T) {
	dir := writeUIGateRepoFixture(t, false)
	m := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}}}
	_, err := captureStdout(t, func() error {
		return auditUIGates(dir, m)
	})
	if err == nil || !strings.Contains(err.Error(), flavor.UIA11ySpecPath) || !strings.Contains(err.Error(), flavor.UIResponsivenessSpecPath) {
		t.Fatalf("auditUIGates(missing specs) error = %v, want missing %s and %s", err, flavor.UIA11ySpecPath, flavor.UIResponsivenessSpecPath)
	}
}

func TestAuditUIGates_RenderedSpecsAndThresholdPasses(t *testing.T) {
	dir := writeUIGateRepoFixture(t, true)
	m := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/", "/settings"}}}
	out, err := captureStdout(t, func() error {
		return auditUIGates(dir, m)
	})
	if err != nil {
		t.Fatalf("auditUIGates(valid UI repo) unexpected error: %v", err)
	}
	if !strings.Contains(out, "[PASS] UI gates:") {
		t.Fatalf("auditUIGates(valid UI repo) output = %q, want [PASS] UI gates:", out)
	}
}

func TestAuditUIGates_MissingVerifyAllTargetFails(t *testing.T) {
	dir := writeUIGateRepoFixture(t, true)
	if err := os.WriteFile(filepath.Join(dir, "Makefile"), []byte("verify-all:\n\t@echo ok\n"), 0o600); err != nil {
		t.Fatalf("write Makefile without ui-gates: %v", err)
	}
	m := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}}}
	_, err := captureStdout(t, func() error {
		return auditUIGates(dir, m)
	})
	if err == nil || !strings.Contains(err.Error(), "ui-gates") {
		t.Fatalf("auditUIGates(missing ui-gates target) error = %v, want substring ui-gates", err)
	}
}

func TestAuditUIGates_MissingThresholdFails(t *testing.T) {
	dir := writeUIGateRepoFixture(t, true)
	m := &config.Manifest{}
	_, err := captureStdout(t, func() error {
		return auditUIGates(dir, m)
	})
	if err == nil || !strings.Contains(err.Error(), "ui.input_latency_ms") {
		t.Fatalf("auditUIGates(missing threshold) error = %v, want substring ui.input_latency_ms", err)
	}
}

func TestAuditUIGates_DroppedAxeTagsFails(t *testing.T) {
	dir := writeUIGateRepoFixture(t, true)
	a11yPath := filepath.Join(dir, flavor.UIA11ySpecPath)
	raw, err := os.ReadFile(a11yPath)
	if err != nil {
		t.Fatalf("read a11y spec: %v", err)
	}
	mutated := strings.ReplaceAll(string(raw), "wcag22aa", "wcag20-custom")
	if err := os.WriteFile(a11yPath, []byte(mutated), 0o600); err != nil {
		t.Fatalf("write mutated a11y spec: %v", err)
	}
	m := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}}}
	_, err = captureStdout(t, func() error {
		return auditUIGates(dir, m)
	})
	if err == nil || !strings.Contains(err.Error(), "wcag22aa") {
		t.Fatalf("auditUIGates(dropped axe tag) error = %v, want missing wcag22aa", err)
	}
}

func TestAuditUIGates_NonUIFixtureSkips(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/svc\n\ngo 1.25.0\n"), 0o600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return auditUIGates(dir, &config.Manifest{})
	})
	if err != nil {
		t.Fatalf("auditUIGates(non-UI repo) unexpected error: %v", err)
	}
	if !strings.Contains(out, "[SKIP] UI gates: not a frontend-svelte repository.") {
		t.Fatalf("auditUIGates(non-UI repo) output = %q, want [SKIP] UI gates", out)
	}
}

func TestUIEnvCommand_PositiveAndNegative(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, ".standards.yaml")
	validYAML := "ui:\n  input_latency_ms: 120\n  routes:\n    - /\n"
	if err := os.WriteFile(manifestPath, []byte(validYAML), 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	out, err := captureStdout(t, func() error {
		return runUI([]string{"env", "--config", manifestPath})
	})
	if err != nil {
		t.Fatalf("runUI(env) unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "export PRAETOR_UI_INPUT_LATENCY_MS=120" {
		t.Fatalf("runUI(env) output = %q, want %q", strings.TrimSpace(out), "export PRAETOR_UI_INPUT_LATENCY_MS=120")
	}

	missingYAML := "version: 1\n"
	if err := os.WriteFile(manifestPath, []byte(missingYAML), 0o600); err != nil {
		t.Fatalf("write manifest without ui: %v", err)
	}
	if _, err := captureStdout(t, func() error {
		return runUI([]string{"env", "--config", manifestPath})
	}); err == nil || !strings.Contains(err.Error(), "ui.input_latency_ms") {
		t.Fatalf("runUI(missing ui) error = %v, want substring ui.input_latency_ms", err)
	}
}
