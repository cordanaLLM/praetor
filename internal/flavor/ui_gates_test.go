// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/flavor"
	"github.com/cordanaLLM/praetor/templates"
)

const validUIMakefileFixture = ".PHONY: verify-all ui-gates\n" +
	"verify-all: ui-gates\n\n" +
	"ui-gates:\n" +
	"\t@$$($(PRAETORCTL) ui env) && npx playwright test tests/a11y.spec.ts tests/responsiveness.spec.ts\n"

func renderUISpecFixture(t *testing.T, dir string) {
	t.Helper()
	ctx := templates.Context{RepoName: "ui-fixture", Owner: "acme", UIRoutes: templates.Routes("/")}
	a11y, err := templates.RenderFile(flavor.UIA11ySpecTemplate, ctx)
	if err != nil {
		t.Fatalf("render a11y spec: %v", err)
	}
	resp, err := templates.RenderFile(flavor.UIResponsivenessSpecTemplate, ctx)
	if err != nil {
		t.Fatalf("render responsiveness spec: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "tests"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, flavor.UIA11ySpecPath), []byte(a11y), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, flavor.UIResponsivenessSpecPath), []byte(resp), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeBaseSvelteDir(t *testing.T, pkgJSON string) string {
	t.Helper()
	uiDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(uiDir, "package.json"), []byte(pkgJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "svelte.config.js"), []byte("export default {};\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(uiDir, "Makefile"), []byte(validUIMakefileFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return uiDir
}

func TestAuditUIGates_Boundary_NonUISkips(t *testing.T) {
	nonUI := t.TempDir()
	line, err := flavor.AuditUIGates(nonUI, &config.Manifest{})
	if err != nil || !strings.HasPrefix(line, "[SKIP] UI gates:") {
		t.Fatalf("non-UI AuditUIGates = (%q, %v), want [SKIP]", line, err)
	}
}

func TestAuditUIGates_PositiveAndMissingSpecs(t *testing.T) {
	uiDir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	withThreshold := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}, ScreenMatrix: "screen-matrix.json"}}
	if _, err := flavor.AuditUIGates(uiDir, withThreshold); err == nil ||
		!strings.Contains(err.Error(), flavor.UIA11ySpecPath) ||
		!strings.Contains(err.Error(), flavor.UIResponsivenessSpecPath) {
		t.Fatalf("missing specs AuditUIGates err = %v, want missing spec paths", err)
	}

	renderUISpecFixture(t, uiDir)
	writeMatrixFixture(t, uiDir, "{}", true)
	passLine, err := flavor.AuditUIGates(uiDir, withThreshold)
	if err != nil || !strings.Contains(passLine, "[PASS] UI gates:") {
		t.Fatalf("valid UI AuditUIGates = (%q, %v), want [PASS]", passLine, err)
	}
}

func TestAuditUIGates_Negative_ThresholdAndDependencyPin(t *testing.T) {
	uiDir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	renderUISpecFixture(t, uiDir)
	if _, err := flavor.AuditUIGates(uiDir, &config.Manifest{}); err == nil || !strings.Contains(err.Error(), "ui.input_latency_ms") {
		t.Fatalf("absent threshold err = %v, want ui.input_latency_ms", err)
	}

	withThreshold := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}, ScreenMatrix: "screen-matrix.json"}}
	if err := os.WriteFile(filepath.Join(uiDir, "package.json"), []byte(`{"name":"ui","devDependencies":{"@axe-core/playwright":"^4.11.1"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := flavor.AuditUIGates(uiDir, withThreshold); err == nil || !strings.Contains(err.Error(), flavor.AxePlaywrightPackage) {
		t.Fatalf("unpinned axe err = %v, want %s", err, flavor.AxePlaywrightPackage)
	}
}

func TestValidateUISpecTexts_Negative(t *testing.T) {
	if err := flavor.ValidateUIA11ySpecText("import AxeBuilder from '@axe-core/playwright';\nconst tags = ['wcag2a'];\n"); err == nil || !strings.Contains(err.Error(), "wcag22aa") {
		t.Fatalf("ValidateUIA11ySpecText missing tags err = %v, want wcag22aa", err)
	}
	if err := flavor.ValidateUIResponsivenessSpecText("const x = 1;\n"); err == nil || !strings.Contains(err.Error(), config.UIInputLatencyEnv) {
		t.Fatalf("ValidateUIResponsivenessSpecText missing env err = %v, want %s", err, config.UIInputLatencyEnv)
	}
	if err := flavor.ValidateUIResponsivenessSpecText("const x = process.env.PRAETOR_UI_INPUT_LATENCY_MS;\nnew PerformanceObserver(() => {}).observe({ durationThreshold: 16 });\nexpect(maxDurationMs).toBeLessThanOrEqual(100);\n// 5000 503\n"); err == nil || !strings.Contains(err.Error(), "numeric latency literal") {
		t.Fatalf("ValidateUIResponsivenessSpecText numeric literal err = %v, want numeric latency literal", err)
	}
}

func TestAuditUIGates_MissingVerifyAllTargetFails(t *testing.T) {
	uiDir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	renderUISpecFixture(t, uiDir)
	if err := os.WriteFile(filepath.Join(uiDir, "Makefile"), []byte("verify-all:\n\t@echo ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	withThreshold := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100, Routes: []string{"/"}, ScreenMatrix: "screen-matrix.json"}}
	if _, err := flavor.AuditUIGates(uiDir, withThreshold); err == nil || !strings.Contains(err.Error(), "ui-gates") {
		t.Fatalf("missing verify-all ui-gates AuditUIGates err = %v, want ui-gates target failure", err)
	}
}

func TestAuditUIGates_ScreenMatrixMissingKey(t *testing.T) {
	dir := writeBaseSvelteDir(t, `{"name":"ui","devDependencies":{"@axe-core/playwright":"4.11.1"}}`)
	renderUISpecFixture(t, dir)
	manifest := &config.Manifest{UI: &config.UIPolicy{InputLatencyMs: 100}}
	if _, err := flavor.AuditUIGates(dir, manifest); err == nil || !strings.Contains(err.Error(), "missing required ui.screen_matrix") {
		t.Fatalf("missing matrix key must fail naming ui.screen_matrix: %v", err)
	}
}
