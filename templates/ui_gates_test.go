// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package templates_test

import (
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/templates"
)

var numericLatencyComparisonPattern = regexp.MustCompile(
	`(?i)(?:toBeLessThan(?:OrEqual)?|toBeGreaterThan(?:OrEqual)?)\s*\(\s*\d+|` +
		`(?:maxDuration\w*|latency\w*|threshold\w*)\s*(?:<=|<|>=|>|===|==)\s*\d+|` +
		`\d+\s*(?:<=|<|>=|>)\s*(?:maxDuration\w*|latency\w*|threshold\w*)`,
)

// TestUIGateSpecs_RenderedGolden verifies the rendered accessibility and responsiveness specs
// against their committed goldens and invariant properties (order engine-fr-rule-checks-20261027).
func TestUIGateSpecs_RenderedGolden(t *testing.T) {
	a11y, err := templates.RenderFile("svelte/a11y.spec.ts.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render svelte/a11y.spec.ts.tmpl: %v", err)
	}
	testsupport.AssertGolden(t, filepath.Join(renderedRoot, "svelte", "a11y.spec", "default.ts"), a11y)
	for _, tag := range []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"} {
		if !strings.Contains(a11y, tag) {
			t.Errorf("rendered a11y.spec.ts lacks tag %q", tag)
		}
	}
	if !strings.Contains(a11y, "@axe-core/playwright") || !strings.Contains(a11y, "violations") {
		t.Errorf("rendered a11y.spec.ts lacks axe builder or violation reporting:\n%s", a11y)
	}

	resp, err := templates.RenderFile("svelte/responsiveness.spec.ts.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render svelte/responsiveness.spec.ts.tmpl: %v", err)
	}
	testsupport.AssertGolden(t, filepath.Join(renderedRoot, "svelte", "responsiveness.spec", "default.ts"), resp)
	for _, required := range []string{
		"PRAETOR_UI_INPUT_LATENCY_MS",
		"PerformanceObserver",
		"durationThreshold: 16",
		"5000",
		"PENDING_STATE_DEADLINE_MS",
		"503",
		`page.route("**/api/**"`,
	} {
		if !strings.Contains(resp, required) {
			t.Errorf("rendered responsiveness.spec.ts lacks %q", required)
		}
	}
	if match := numericLatencyComparisonPattern.FindString(resp); match != "" {
		t.Errorf("rendered responsiveness.spec.ts holds numeric latency literal next to comparison: %q", match)
	}
}

// TestUIGateSpecs_MultiRouteAndAlias verifies custom UIRoutes and UIAPIPattern rendering and the
// tests/*.tmpl alias.
func TestUIGateSpecs_MultiRouteAndAlias(t *testing.T) {
	ctx := sampleContext
	ctx.UIRoutes = []string{"/", "/dashboard", "/settings"}
	ctx.UIAPIPattern = "**/v1/**"
	a11y, err := templates.RenderFile("tests/a11y.spec.ts.tmpl", ctx)
	if err != nil {
		t.Fatalf("render tests/a11y.spec.ts.tmpl: %v", err)
	}
	for _, route := range ctx.UIRoutes {
		if !strings.Contains(a11y, route) {
			t.Errorf("rendered a11y.spec.ts lacks route %q", route)
		}
	}
	resp, err := templates.RenderFile("tests/responsiveness.spec.ts.tmpl", ctx)
	if err != nil {
		t.Fatalf("render tests/responsiveness.spec.ts.tmpl: %v", err)
	}
	for _, route := range ctx.UIRoutes {
		if !strings.Contains(resp, route) {
			t.Errorf("rendered responsiveness.spec.ts lacks route %q", route)
		}
	}
	if !strings.Contains(resp, `page.route("**/v1/**"`) || strings.Contains(resp, `**/api/**`) {
		t.Errorf("rendered responsiveness.spec.ts did not apply custom UIAPIPattern:\n%s", resp)
	}
}

func TestUIGateSpecs_PendingStateDeadline(t *testing.T) {
	resp, err := templates.RenderFile("svelte/responsiveness.spec.ts.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render svelte/responsiveness.spec.ts.tmpl: %v", err)
	}
	if !strings.Contains(resp, "PENDING_STATE_DEADLINE_MS") ||
		!strings.Contains(resp, "SLOW_API_DELAY_MS / 2") ||
		strings.Contains(resp, "timeout: FRAME_BUDGET_MS") {
		t.Fatalf("rendered responsiveness.spec.ts lacks PENDING_STATE_DEADLINE_MS or still holds timeout: FRAME_BUDGET_MS")
	}
}

func TestUIGateSpecs_PlainThresholdCode(t *testing.T) {
	resp, err := templates.RenderFile("svelte/responsiveness.spec.ts.tmpl", sampleContext)
	if err != nil {
		t.Fatalf("render svelte/responsiveness.spec.ts.tmpl: %v", err)
	}
	if strings.Contains(resp, "Number(true)") ||
		strings.Contains(resp, "Number(false)") ||
		strings.Contains(resp, "env === process.env") ||
		!strings.Contains(resp, "parsed <= 0") ||
		!strings.Contains(resp, "let maxMeasured = 0") {
		t.Fatalf("rendered responsiveness.spec.ts still holds non-plain threshold or zero code")
	}
}
