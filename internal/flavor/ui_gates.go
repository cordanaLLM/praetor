// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package flavor

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/nodemanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/templates"
)

// UI gate paths, templates, and dependency constants (order engine-fr-rule-checks-20261027).
const (
	UIA11ySpecPath               = "tests/a11y.spec.ts"
	UIA11ySpecTemplate           = "svelte/a11y.spec.ts.tmpl"
	UIResponsivenessSpecPath     = "tests/responsiveness.spec.ts"
	UIResponsivenessSpecTemplate = "svelte/responsiveness.spec.ts.tmpl"
	AxePlaywrightPackage         = "@axe-core/playwright"
	AxePlaywrightPinnedVersion   = "4.11.1"
)

var (
	wcag22AATags = []string{"wcag2a", "wcag2aa", "wcag21a", "wcag21aa", "wcag22aa"}

	numericLatencyComparisonRegex = regexp.MustCompile(
		`(?i)(?:toBeLessThan(?:OrEqual)?|toBeGreaterThan(?:OrEqual)?)\s*\(\s*\d+|` +
			`(?:maxDuration\w*|latency\w*|threshold\w*)\s*(?:<=|<|>=|>|===|==)\s*\d+|` +
			`\d+\s*(?:<=|<|>=|>)\s*(?:maxDuration\w*|latency\w*|threshold\w*)`,
	)
)

// WCAG22AATags returns a copy of the five WCAG 2.2 AA axe tags required in tests/a11y.spec.ts.
func WCAG22AATags() []string {
	return append([]string(nil), wcag22AATags...)
}

// IsExactSemverPin reports whether version is an exact SemVer 2.0.0 version pin without a
// range comparator or leading "v".
func IsExactSemverPin(version string) bool {
	trimmed := strings.TrimSpace(version)
	if trimmed == "" || strings.HasPrefix(trimmed, "v") {
		return false
	}
	_, ok := semver.Parse(trimmed)
	return ok
}

func resolveUISpecContext(_ context.Context, repoPath string) (templates.Context, string) {
	manifestPath := filepath.Join(repoPath, config.ManifestFileName)
	manifest, err := config.LoadManifest(manifestPath)
	if err != nil || manifest == nil {
		return templates.Context{
			UIRoutes:     templates.Routes(config.DefaultUIRoute),
			UIAPIPattern: config.DefaultUIAPIPattern,
		}, ""
	}
	return templates.Context{
		UIRoutes:     templates.Routes(manifest.UIRoutes()...),
		UIAPIPattern: manifest.UIAPIPattern(),
	}, ""
}

func validUIA11ySpec(content []byte) bool {
	return carriesCode(content) && ValidateUIA11ySpecText(string(content)) == nil
}

func validUIResponsivenessSpec(content []byte) bool {
	return carriesCode(content) && ValidateUIResponsivenessSpecText(string(content)) == nil
}

// ValidateUIA11ySpecText verifies that a rendered tests/a11y.spec.ts imports
// @axe-core/playwright, scans all five WCAG 2.2 AA tags, and reports violations.
func ValidateUIA11ySpecText(content string) error {
	if !strings.Contains(content, AxePlaywrightPackage) {
		return fmt.Errorf("%s does not reference %s", UIA11ySpecPath, AxePlaywrightPackage)
	}
	var missing []string
	for i := 0; i < len(wcag22AATags) && i < 8; i++ {
		tag := wcag22AATags[i]
		if !strings.Contains(content, tag) {
			missing = append(missing, tag)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("%s is missing required WCAG 2.2 AA axe tag(s): %s", UIA11ySpecPath, strings.Join(missing, ", "))
	}
	if !strings.Contains(content, "violations") {
		return fmt.Errorf("%s does not format axe violations in its failure assertion", UIA11ySpecPath)
	}
	return nil
}

// ValidateUIResponsivenessSpecText verifies that a rendered tests/responsiveness.spec.ts
// uses PerformanceObserver with durationThreshold, reads PRAETOR_UI_INPUT_LATENCY_MS from
// process.env without a numeric latency literal in the comparison, and includes the slow-API
// and HTTP 503 planted cases.
func ValidateUIResponsivenessSpecText(content string) error {
	if !strings.Contains(content, config.UIInputLatencyEnv) || !strings.Contains(content, "process.env") {
		return fmt.Errorf("%s does not read %s from process.env", UIResponsivenessSpecPath, config.UIInputLatencyEnv)
	}
	if !strings.Contains(content, "PerformanceObserver") || !strings.Contains(content, "durationThreshold") {
		return fmt.Errorf("%s lacks PerformanceObserver event durationThreshold instrumentation", UIResponsivenessSpecPath)
	}
	if match := numericLatencyComparisonRegex.FindString(content); match != "" {
		return fmt.Errorf("%s contains numeric latency literal in comparison (%s); read %s instead",
			UIResponsivenessSpecPath, match, config.UIInputLatencyEnv)
	}
	if !hasSlowAPIAnd503Cases(content) {
		return fmt.Errorf("%s lacks required slow-API (5000 ms) or HTTP 503 planted cases", UIResponsivenessSpecPath)
	}
	return nil
}

func hasSlowAPIAnd503Cases(content string) bool {
	hasSlowDelay := strings.Contains(content, "5000") || strings.Contains(content, "5_000")
	return hasSlowDelay && strings.Contains(content, "503")
}

var (
	ownerPrefixPattern = regexp.MustCompile(`(?i)\b(?:owner(?:\s+is)?|owned\s+by)[:\s]+([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?)`)
	ownerSuffixPattern = regexp.MustCompile(`(?i)\b([A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]+)?)(?:\s+[A-Za-z0-9_.-]+)?\s+as\s+owner\b`)
)

func extractExceptionOwner(reason string) string {
	if m := ownerPrefixPattern.FindStringSubmatch(reason); len(m) > 1 {
		return strings.Trim(m[1], ",;.")
	}
	if m := ownerSuffixPattern.FindStringSubmatch(reason); len(m) > 1 {
		return strings.Trim(m[1], ",;.")
	}
	return ""
}

// AuditUIGates runs the ui-gates audit check on repoPath (order engine-fr-rule-checks-20261027).
// It reuses FrontendSvelteFlavor.Detect to classify UI repositories; non-UI repositories return
// a [SKIP] line and a nil error.
func AuditUIGates(repoPath string, manifest *config.Manifest) (string, error) {
	return AuditUIGatesToday(repoPath, manifest, time.Now())
}

// AuditUIGatesToday runs the ui-gates audit check using today as the reference time for exception expiry.
func AuditUIGatesToday(repoPath string, manifest *config.Manifest, today time.Time) (string, error) {
	svelte := &FrontendSvelteFlavor{}
	if !svelte.Detect(repoPath) {
		return "[SKIP] UI gates: not a frontend-svelte repository.", nil
	}
	var gaps []string
	if gap := checkUIThresholdGap(manifest); gap != "" {
		gaps = append(gaps, gap)
	}
	sourceLine, sourceGap := checkUILatencySourceGap(manifest, today)
	if sourceGap != "" {
		gaps = append(gaps, sourceGap)
	}
	if gap := checkUIAxeDependencyGap(repoPath); gap != "" {
		gaps = append(gaps, gap)
	}
	if gap := checkUISpecFileGap(repoPath, UIA11ySpecPath, ValidateUIA11ySpecText); gap != "" {
		gaps = append(gaps, gap)
	}
	if gap := checkUISpecFileGap(repoPath, UIResponsivenessSpecPath, ValidateUIResponsivenessSpecText); gap != "" {
		gaps = append(gaps, gap)
	}
	if gap := checkUIMakefileGap(repoPath); gap != "" {
		gaps = append(gaps, gap)
	}
	matrixLine, matrixErr := auditUIScreenMatrix(repoPath, manifest, screenMatrixSchemaPin)
	if matrixErr != nil {
		gaps = append(gaps, matrixErr.Error())
	}
	if len(gaps) > 0 {
		return matrixLine, fmt.Errorf("[FAIL] UI gates: %s", strings.Join(gaps, "; "))
	}
	return matrixLine + "\n" + fmt.Sprintf(
		"[PASS] UI gates: %s and %s verified (ui.input_latency_ms=%d, %s, routes=%d).",
		UIA11ySpecPath, UIResponsivenessSpecPath, manifest.UI.InputLatencyMs, sourceLine, len(manifest.UIRoutes()),
	), nil
}

func checkUILatencySourceGap(manifest *config.Manifest, today time.Time) (string, string) {
	if manifest == nil || manifest.UI == nil || manifest.UI.InputLatencyMs <= 0 {
		return "", ""
	}
	if manifest.UI.InputLatencySource != "" {
		return fmt.Sprintf("source=%s", manifest.UI.InputLatencySource), ""
	}
	entries := config.ExceptionsFor(manifest.Exceptions, config.ExceptionRuleUILatencySource)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Expired(today) || entry.ExpiryCategory() == "" {
			continue
		}
		owner := extractExceptionOwner(entry.Reason)
		if owner == "" {
			continue
		}
		info := fmt.Sprintf("exception=%s (owner %s, expires %s)", entry.Rule, owner, entry.Expires)
		return info, ""
	}
	return "", "ui.input_latency_source missing: pin the measurement evidence the threshold comes from"
}

func checkUIThresholdGap(manifest *config.Manifest) string {
	if manifest == nil || manifest.UI == nil || manifest.UI.InputLatencyMs <= 0 {
		return "missing required ui.input_latency_ms in .standards.yaml"
	}
	if err := manifest.UI.Validate(); err != nil {
		return err.Error()
	}
	return ""
}

func checkUIAxeDependencyGap(repoPath string) string {
	raw, ok := readRequiredFile(repoPath, "package.json")
	if !ok {
		return "package.json is missing or unreadable"
	}
	pkg, err := nodemanifest.ParseManifest(raw)
	if err != nil {
		return err.Error()
	}
	if len(pkg.DevDependencies) == 0 && len(pkg.Dependencies) == 0 {
		return ""
	}
	ver, hasDev := pkg.DevDependencies[AxePlaywrightPackage]
	if !hasDev {
		return fmt.Sprintf("package.json devDependencies is missing %s", AxePlaywrightPackage)
	}
	if !IsExactSemverPin(ver) {
		return fmt.Sprintf("package.json devDependencies[%q] = %q must be pinned to an exact version", AxePlaywrightPackage, ver)
	}
	return ""
}

func checkUISpecFileGap(repoPath, relPath string, validate func(string) error) string {
	content, ok := readRequiredFile(repoPath, relPath)
	if !ok {
		return fmt.Sprintf("missing required spec %s", relPath)
	}
	if err := validate(string(content)); err != nil {
		return err.Error()
	}
	return ""
}

func checkUIMakefileGap(repoPath string) string {
	raw, ok := readRequiredFile(repoPath, "Makefile")
	if !ok {
		return "Makefile is missing verify-all target ui-gates"
	}
	normalized, _ := util.NormalizeLineEndings(string(raw))
	if !verifyAllRunsUIGates(normalized) {
		return "Makefile verify-all does not include target ui-gates"
	}
	recipe, ok := util.MakefileTargetRecipe(normalized, "ui-gates")
	if !ok || !strings.Contains(recipe, "ui env") || !strings.Contains(recipe, "playwright test") ||
		!strings.Contains(recipe, UIA11ySpecPath) || !strings.Contains(recipe, UIResponsivenessSpecPath) {
		return fmt.Sprintf("Makefile target ui-gates must run praetorctl ui env and playwright test on %s and %s", UIA11ySpecPath, UIResponsivenessSpecPath)
	}
	return ""
}

func verifyAllRunsUIGates(normalized string) bool {
	if !util.MakefileHasTarget(normalized, "verify-all") || !util.MakefileHasTarget(normalized, "ui-gates") {
		return false
	}
	if recipe, ok := util.MakefileTargetRecipe(normalized, "verify-all"); ok && strings.Contains(recipe, "ui-gates") {
		return true
	}
	lines := strings.Split(normalized, "\n")
	for i := 0; i < len(lines) && i < util.MaxMakefileLines; i++ {
		if makefileLineHasPrereq(lines[i], "verify-all", "ui-gates") {
			return true
		}
	}
	return false
}

func makefileLineHasPrereq(line, target, prereq string) bool {
	if strings.HasPrefix(line, "\t") || strings.HasPrefix(strings.TrimSpace(line), "#") {
		return false
	}
	head, prereqs, found := strings.Cut(line, ":")
	if !found {
		return false
	}
	return slices.Contains(strings.Fields(head), target) && slices.Contains(strings.Fields(prereqs), prereq)
}
