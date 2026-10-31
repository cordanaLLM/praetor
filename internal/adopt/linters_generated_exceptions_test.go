// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

func sampleConfigContent(handRule, blockRules string) string {
	var sb strings.Builder
	sb.WriteString("version: \"2\"\nrun:\n  timeout: 10m\nlinters:\n  enable:\n    - gocyclo\n    - gocognit\n    - funlen\n    - forbidigo\n    - noctx\n  exclusions:\n    warn-unused: true\n    rules:\n")
	if handRule != "" {
		sb.WriteString(handRule)
		sb.WriteString("\n")
	}
	if blockRules != "" {
		sb.WriteString("      " + GeneratedExceptionsBegin + "\n")
		sb.WriteString(blockRules)
		sb.WriteString("      " + GeneratedExceptionsEnd + "\n")
	}
	return sb.String()
}

func sampleIssueText(linter, fn string) string {
	if fn == "" {
		fn = "TestSample"
	}
	switch linter {
	case "gocyclo":
		return "cyclomatic complexity 15 of func `" + fn + "` is high (> 11)"
	case "gocognit":
		return "cognitive complexity 20 of func `" + fn + "` is high (> 16)"
	case "funlen":
		return "Function '" + fn + "' is too long (70 > 58)"
	default:
		return "use of forbidden identifier"
	}
}

func sampleReportJSON(t *testing.T, pairs []LinterPair) []byte {
	t.Helper()
	rep := golangciReport{
		Issues: make([]golangciIssue, len(pairs)),
	}
	for i, p := range pairs {
		rep.Issues[i] = golangciIssue{
			FromLinter: p.Linter,
			Text:       sampleIssueText(p.Linter, p.Function),
			Pos: golangciPosition{
				Filename: p.File,
			},
		}
	}
	data, err := json.Marshal(rep)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	return data
}

func writeTempConfigAndReport(t *testing.T, cfgContent string, pairs []LinterPair) (string, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".golangci.yml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	repPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(repPath, sampleReportJSON(t, pairs), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return cfgPath, repPath
}

func parseTestDate(t *testing.T, dateStr string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		t.Fatalf("parse date %s: %v", dateStr, err)
	}
	return parsed
}

func TestStripGolangciConfig_Positive(t *testing.T) {
	handRule := "      # category A, expires 2026-12-01, #100\n      - path: internal/hand/\n        linters:\n          - forbidigo"
	block := "      # category C, entered 2026-10-18, expires 2026-10-28, #1030\n      - path: ^internal/a_test\\.go$\n        linters:\n          - gocyclo\n"
	cfg := sampleConfigContent(handRule, block)
	dir := t.TempDir()
	src := filepath.Join(dir, "in.yml")
	out := filepath.Join(dir, "out.yml")
	if err := os.WriteFile(src, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := StripGolangciConfig(src, out); err != nil {
		t.Fatalf("StripGolangciConfig failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	content := string(data)
	if strings.Contains(content, GeneratedExceptionsBegin) || strings.Contains(content, "a_test.go") {
		t.Errorf("stripped config still contains generated block:\n%s", content)
	}
	if !strings.Contains(content, "relative-path-mode: gitroot") {
		t.Errorf("stripped config missing relative-path-mode: gitroot:\n%s", content)
	}
	if !strings.Contains(content, "uniq-by-line: false") {
		t.Errorf("stripped config missing uniq-by-line: false:\n%s", content)
	}
	if !strings.Contains(content, "max-issues-per-linter: 0") {
		t.Errorf("stripped config missing max-issues-per-linter: 0:\n%s", content)
	}
	if !strings.Contains(content, "internal/hand/") {
		t.Errorf("stripped config lost hand rule:\n%s", content)
	}
	var node yaml.Node
	if err := yaml.Unmarshal(data, &node); err != nil {
		t.Errorf("stripped config is not valid YAML: %v", err)
	}
}

func TestStripGolangciConfig_Negative(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.yml")
	if err := StripGolangciConfig(filepath.Join(dir, "absent.yml"), out); err == nil {
		t.Error("expected error for non-existent source file")
	}
}

func TestStripGolangciConfig_Boundary(t *testing.T) {
	dir := t.TempDir()
	noRunSrc := filepath.Join(dir, "norun.yml")
	noRunOut := filepath.Join(dir, "norun_out.yml")
	if err := os.WriteFile(noRunSrc, []byte("version: \"2\"\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := StripGolangciConfig(noRunSrc, noRunOut); err != nil {
		t.Fatalf("StripGolangciConfig norun failed: %v", err)
	}
	noRunData, err := os.ReadFile(noRunOut)
	if err != nil {
		t.Fatalf("read norun out: %v", err)
	}
	noRunStr := string(noRunData)
	if !strings.Contains(noRunStr, "relative-path-mode: gitroot") {
		t.Errorf("norun output missing relative-path-mode:\n%s", noRunStr)
	}
	if !strings.Contains(noRunStr, "uniq-by-line: false") {
		t.Errorf("norun output missing uniq-by-line: false:\n%s", noRunStr)
	}

	// Boundary: existing issues block gets uniq-by-line: false
	hasIssuesSrc := filepath.Join(dir, "hasissues.yml")
	hasIssuesOut := filepath.Join(dir, "hasissues_out.yml")
	if err := os.WriteFile(hasIssuesSrc, []byte("version: \"2\"\nissues:\n  exclude-use-default: false\n"), 0o600); err != nil {
		t.Fatalf("write hasissues: %v", err)
	}
	if err := StripGolangciConfig(hasIssuesSrc, hasIssuesOut); err != nil {
		t.Fatalf("StripGolangciConfig hasissues failed: %v", err)
	}
	hasIssuesData, err := os.ReadFile(hasIssuesOut)
	if err != nil {
		t.Fatalf("read hasissues out: %v", err)
	}
	hasIssuesStr := string(hasIssuesData)
	if !strings.Contains(hasIssuesStr, "uniq-by-line: false") {
		t.Errorf("hasissues output missing uniq-by-line: false:\n%s", hasIssuesStr)
	}
	if !strings.Contains(hasIssuesStr, "exclude-use-default: false") {
		t.Errorf("hasissues output lost original issue config:\n%s", hasIssuesStr)
	}
}

func TestGenerateExceptions_FormatAndOrder_Positive(t *testing.T) {
	handRule := "      # category A, expires 2026-12-01, #100\n      - path: internal/hand/\n        linters:\n          - forbidigo"
	cfgContent := sampleConfigContent(handRule, "")
	pairs := []LinterPair{
		{File: "internal/pkg/b_test.go", Linter: "forbidigo"},
		{File: "internal/pkg/b_test.go", Linter: "gocognit", Function: "TestBeta"},
		{File: "internal/pkg/a_test.go", Linter: "gocyclo", Function: "TestZeta"},
		{File: "internal/pkg/a_test.go", Linter: "gocyclo", Function: "TestAlpha"},
		{File: "internal/pkg/a_test.go", Linter: "funlen", Function: "TestLong"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stdout bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stdout:     &stdout,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions failed: %v", err)
	}
	if res.ExitCode != 0 || !res.Written || res.Count != 5 {
		t.Fatalf("unexpected result: code=%d written=%v count=%d", res.ExitCode, res.Written, res.Count)
	}

	assertFormattedGeneratedOrder(t, cfgPath, handRule)
}

func assertFormattedGeneratedOrder(t *testing.T, cfgPath, handRule string) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	result := string(data)
	if !strings.Contains(result, handRule) {
		t.Errorf("hand rule modified:\n%s", result)
	}
	expectedSnippet := "      # baseline #1030\n" +
		"      - path: ^internal/pkg/a_test\\.go$\n" +
		"        linters:\n" +
		"          - funlen\n" +
		"        text: Function 'TestLong'\n" +
		"      # baseline #1030\n" +
		"      - path: ^internal/pkg/a_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestAlpha`\n" +
		"      # baseline #1030\n" +
		"      - path: ^internal/pkg/a_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestZeta`\n" +
		"      # category C, entered 2026-10-18, expires 2026-10-28, #1030\n" +
		"      - path: ^internal/pkg/b_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n" +
		"      # baseline #1030\n" +
		"      - path: ^internal/pkg/b_test\\.go$\n" +
		"        linters:\n" +
		"          - gocognit\n" +
		"        text: func `TestBeta`\n"
	if !strings.Contains(result, expectedSnippet) {
		t.Errorf("generated block order/format mismatch. Expected snippet:\n%s\nGot:\n%s", expectedSnippet, result)
	}
}

func TestGenerateExceptions_Ratchet_Positive_LowerCountRenews(t *testing.T) {
	oldBlock := "      # category C, entered 2026-10-01, expires 2026-10-11, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n" +
		"      # category C, entered 2026-10-01, expires 2026-10-11, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - noctx\n" +
		"      # category C, entered 2026-10-01, expires 2026-10-11, #1030\n" +
		"      - path: ^internal/b_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n"
	cfgContent := sampleConfigContent("", oldBlock)
	pairs := []LinterPair{
		{File: "internal/a_test.go", Linter: "forbidigo"},
		{File: "internal/a_test.go", Linter: "noctx"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stdout bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stdout:     &stdout,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions failed: %v", err)
	}
	if res.ExitCode != 0 || !res.Written || res.Count != 2 {
		t.Fatalf("unexpected ratchet result: code=%d written=%v count=%d", res.ExitCode, res.Written, res.Count)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	result := string(data)
	if strings.Contains(result, "b_test.go") {
		t.Errorf("clean pair b_test.go was not dropped:\n%s", result)
	}
	if !strings.Contains(result, "entered 2026-10-18, expires 2026-10-28") {
		t.Errorf("dates were not renewed:\n%s", result)
	}
}

func TestGenerateExceptions_Ratchet_Negative_NewFindingRefused(t *testing.T) {
	block := "      # baseline #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestAlpha`\n"
	cfgContent := sampleConfigContent("", block)
	pairs := []LinterPair{
		{File: "internal/a_test.go", Linter: "gocyclo", Function: "TestAlpha"},
		{File: "internal/b_test.go", Linter: "forbidigo"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stderr bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions error: %v", err)
	}
	if res.ExitCode != 3 || res.Written {
		t.Fatalf("expected exit 3 and no write, got code=%d written=%v", res.ExitCode, res.Written)
	}
	expectedMsg := "new finding in internal/b_test.go (forbidigo): fix it, a generated exception never grows"
	if !strings.Contains(res.Message, expectedMsg) {
		t.Errorf("message mismatch. Want %q, got %q", expectedMsg, res.Message)
	}
}

func TestGenerateExceptions_Ratchet_Negative_ExpiredRefused(t *testing.T) {
	block := "      # category C, entered 2026-10-01, expires 2026-10-11, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n" +
		"      # category C, entered 2026-10-01, expires 2026-10-11, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - noctx\n"
	cfgContent := sampleConfigContent("", block)
	pairs := []LinterPair{
		{File: "internal/a_test.go", Linter: "forbidigo"},
		{File: "internal/a_test.go", Linter: "noctx"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stderr bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions error: %v", err)
	}
	if res.ExitCode != 3 || res.Written {
		t.Fatalf("expected exit 3 and no write, got code=%d written=%v", res.ExitCode, res.Written)
	}
	expectedMsg := "count did not drop: 2 recorded, 2 measured; renewal needs a lower count"
	if !strings.Contains(res.Message, expectedMsg) {
		t.Errorf("message mismatch. Want %q, got %q", expectedMsg, res.Message)
	}
}

func TestGenerateExceptions_Ratchet_Boundary_LiveEntriesNoOp(t *testing.T) {
	block := "      # category C, entered 2026-10-18, expires 2026-10-28, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n" +
		"      # category C, entered 2026-10-18, expires 2026-10-28, #1030\n" +
		"      - path: ^internal/a_test\\.go$\n" +
		"        linters:\n" +
		"          - noctx\n"
	cfgContent := sampleConfigContent("", block)
	pairs := []LinterPair{
		{File: "internal/a_test.go", Linter: "forbidigo"},
		{File: "internal/a_test.go", Linter: "noctx"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stdout bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stdout:     &stdout,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions error: %v", err)
	}
	if res.ExitCode != 0 || res.Written {
		t.Fatalf("expected exit 0 and no write, got code=%d written=%v", res.ExitCode, res.Written)
	}
	expectedMsg := "unchanged: 2 entries live until 2026-10-28"
	if !strings.Contains(res.Message, expectedMsg) {
		t.Errorf("message mismatch. Want %q, got %q", expectedMsg, res.Message)
	}
}

func TestGenerateExceptions_PlantedGocycloNewFinding(t *testing.T) {
	block := "      # baseline #1030\n" +
		"      - path: ^internal/existing_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestExisting`\n"
	cfgContent := sampleConfigContent("", block)
	pairs := []LinterPair{
		{File: "internal/existing_test.go", Linter: "gocyclo", Function: "TestExisting"},
		{File: "internal/existing_test.go", Linter: "gocyclo", Function: "TestPlantedNewFunc"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	var stderr bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      today,
		Write:      true,
		Stderr:     &stderr,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions error: %v", err)
	}
	if res.ExitCode != 3 || res.Written {
		t.Fatalf("expected exit 3 refusing planted finding in already baselined file, got code=%d written=%v", res.ExitCode, res.Written)
	}
	want := "new finding in internal/existing_test.go (gocyclo) TestPlantedNewFunc: fix it, a generated exception never grows"
	if !strings.Contains(res.Message, want) {
		t.Errorf("refusal message = %q, want %q", res.Message, want)
	}
}

func TestGenerateExceptions_BaselineShrinkAndHold(t *testing.T) {
	block := "      # baseline #1030\n" +
		"      - path: ^internal/existing_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestCleaned`\n" +
		"      # baseline #1030\n" +
		"      - path: ^internal/existing_test\\.go$\n" +
		"        linters:\n" +
		"          - gocyclo\n" +
		"        text: func `TestStillOverCap`\n" +
		"      # category C, entered 2026-10-10, expires 2026-10-20, #1030\n" +
		"      - path: ^internal/existing_test\\.go$\n" +
		"        linters:\n" +
		"          - forbidigo\n"
	cfgContent := sampleConfigContent("", block)
	pairs := []LinterPair{
		{File: "internal/existing_test.go", Linter: "forbidigo"},
		{File: "internal/existing_test.go", Linter: "gocyclo", Function: "TestStillOverCap"},
	}
	cfgPath, repPath := writeTempConfigAndReport(t, cfgContent, pairs)
	today := parseTestDate(t, "2026-10-18")

	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath, ReportPath: repPath, Issue: 1030, Category: "C", Today: today, Write: true,
	})
	if err != nil || res.ExitCode != 0 || !res.Written || res.Count != 2 {
		t.Fatalf("shrink baseline: err=%v res=%+v", err, res)
	}
	assertBaselineShrunkAndDatedPreserved(t, cfgPath, repPath, today)
}

func assertBaselineShrunkAndDatedPreserved(t *testing.T, cfgPath, repPath string, today time.Time) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, "TestCleaned") {
		t.Errorf("cleaned function was not dropped:\n%s", text)
	}
	if !strings.Contains(text, "TestStillOverCap") {
		t.Errorf("remaining function was dropped:\n%s", text)
	}
	// forbidigo count did not drop, so its dates must not advance to 2026-10-18.
	if !strings.Contains(text, "entered 2026-10-10, expires 2026-10-20") {
		t.Errorf("dated forbidigo rule dates moved without a lower dated count:\n%s", text)
	}
	res2, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath, ReportPath: repPath, Issue: 1030, Category: "C", Today: today, Write: true,
	})
	if err != nil || res2.ExitCode != 0 || res2.Written {
		t.Fatalf("second run should be unchanged no-op: err=%v res=%+v", err, res2)
	}
}

func TestGenerateExceptions_TextRegexp_DelimitersAndQuoting(t *testing.T) {
	cases := []struct {
		linter   string
		fn       string
		wantRe   string
		matchMsg string
		noMatch  string
	}{
		{
			linter:   "gocyclo",
			fn:       "TestX",
			wantRe:   "func `TestX`",
			matchMsg: "cyclomatic complexity 14 of func `TestX` is high (> 11)",
			noMatch:  "cyclomatic complexity 14 of func `TestXY` is high (> 11)",
		},
		{
			linter:   "funlen",
			fn:       "TestX",
			wantRe:   "Function 'TestX'",
			matchMsg: "Function 'TestX' is too long (65 > 58)",
			noMatch:  "Function 'TestXY' is too long (65 > 58)",
		},
		{
			linter:   "gocognit",
			fn:       "(*forgeStub).handler",
			wantRe:   `func ` + "`" + `\(\*forgeStub\)\.handler` + "`",
			matchMsg: "cognitive complexity 22 of func `(*forgeStub).handler` is high (> 16)",
			noMatch:  "cognitive complexity 22 of func `(forgeStub).handler` is high (> 16)",
		},
		{
			linter:   "gocyclo",
			fn:       "TestMeta[pkg.Type+1]($a|^b?)",
			wantRe:   `func ` + "`" + `TestMeta\[pkg\.Type\+1\]\(\$a\|\^b\?\)` + "`",
			matchMsg: "cyclomatic complexity 12 of func `TestMeta[pkg.Type+1]($a|^b?)` is high (> 11)",
			noMatch:  "cyclomatic complexity 12 of func `TestMeta[pkgXType+1]($a|^b?)` is high (> 11)",
		},
	}
	for _, tc := range cases {
		assertTextRegexpCase(t, tc.linter, tc.fn, tc.wantRe, tc.matchMsg, tc.noMatch)
	}
}

func assertTextRegexpCase(t *testing.T, linter, fn, wantRe, matchMsg, noMatch string) {
	t.Helper()
	gotRe := functionTextRegexp(linter, fn)
	if gotRe != wantRe {
		t.Fatalf("%s(%q) regexp = %q, want %q", linter, fn, gotRe, wantRe)
	}
	re, err := regexp.Compile(gotRe)
	if err != nil {
		t.Fatalf("compile %q: %v", gotRe, err)
	}
	if !re.MatchString(matchMsg) {
		t.Errorf("regexp %q did not match %q", gotRe, matchMsg)
	}
	if re.MatchString(noMatch) {
		t.Errorf("regexp %q matched %q", gotRe, noMatch)
	}
	if extracted := extractIssueFunction(linter, matchMsg); extracted != fn {
		t.Errorf("extractIssueFunction(%q, %q) = %q, want %q", linter, matchMsg, extracted, fn)
	}
	if unquoted := unquoteRuleText(linter, gotRe); unquoted != fn {
		t.Errorf("unquoteRuleText(%q, %q) = %q, want %q", linter, gotRe, unquoted, fn)
	}
}

func writeSameLineTestFiles(t *testing.T) (string, string) {
	t.Helper()
	cfgContent := sampleConfigContent("", "")
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, ".golangci.yml")
	if err := os.WriteFile(cfgPath, []byte(cfgContent), 0o600); err != nil {
		t.Fatalf("write cfg: %v", err)
	}
	repJSON := `{
		"Issues": [
			{"FromLinter": "gocyclo", "Text": "cyclomatic complexity 14 of func ` + "`TestSameLine`" + ` is high (> 11)", "Pos": {"Filename": "internal/pkg/foo.go", "Line": 42}},
			{"FromLinter": "gocognit", "Text": "cognitive complexity 19 of func ` + "`TestSameLine`" + ` is high (> 16)", "Pos": {"Filename": "internal/pkg/foo.go", "Line": 42}}
		]
	}`
	repPath := filepath.Join(dir, "report.json")
	if err := os.WriteFile(repPath, []byte(repJSON), 0o600); err != nil {
		t.Fatalf("write report: %v", err)
	}
	return cfgPath, repPath
}

func assertSameLineReportPairs(t *testing.T, pairs []LinterPair) {
	t.Helper()
	if len(pairs) != 2 {
		t.Fatalf("expected 2 pairs from same-line report, got %d: %v", len(pairs), pairs)
	}
	if pairs[0].Linter != "gocognit" || pairs[1].Linter != "gocyclo" {
		t.Fatalf("expected gocognit and gocyclo pairs, got: %v", pairs)
	}
}

func assertSameLineGeneratedConfig(t *testing.T, cfgPath string) {
	t.Helper()
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	outContent := string(data)
	if !strings.Contains(outContent, "gocognit") || !strings.Contains(outContent, "gocyclo") {
		t.Fatalf("expected both rules in generated block:\n%s", outContent)
	}
	// Count occurrences of path rule
	if strings.Count(outContent, "^internal/pkg/foo\\.go$") != 2 {
		t.Fatalf("expected 2 rules for internal/pkg/foo.go, got:\n%s", outContent)
	}
}

func TestGenerateExceptions_SameLineDifferentLintersYieldsTwoPairs(t *testing.T) {
	cfgPath, repPath := writeSameLineTestFiles(t)

	pairs, err := readReportPairs(repPath, cfgPath)
	if err != nil {
		t.Fatalf("readReportPairs failed: %v", err)
	}
	assertSameLineReportPairs(t, pairs)

	var stdout bytes.Buffer
	res, err := GenerateExceptions(GenerateExceptionsOptions{
		ConfigPath: cfgPath,
		ReportPath: repPath,
		Issue:      1030,
		Category:   "C",
		Today:      parseTestDate(t, "2026-10-18"),
		Write:      true,
		Stdout:     &stdout,
	})
	if err != nil {
		t.Fatalf("GenerateExceptions failed: %v", err)
	}
	if res.ExitCode != 0 || !res.Written || res.Count != 2 {
		t.Fatalf("unexpected result: code=%d written=%v count=%d", res.ExitCode, res.Written, res.Count)
	}

	assertSameLineGeneratedConfig(t, cfgPath)
}

func pushSequenceChildren(stack []*yaml.Node, node *yaml.Node) []*yaml.Node {
	limit := len(node.Content)
	if limit > maxGolangciLines {
		limit = maxGolangciLines
	}
	for i := limit - 1; i >= 0; i-- {
		stack = append(stack, node.Content[i])
	}
	return stack
}

func checkMappingDuplicates(t *testing.T, stack []*yaml.Node, node *yaml.Node) []*yaml.Node {
	t.Helper()
	seen := make(map[string]bool, len(node.Content)/2)
	for i := 0; i+1 < len(node.Content) && i < maxYamlMappingKeys; i += 2 {
		keyNode := node.Content[i]
		if seen[keyNode.Value] {
			t.Fatalf("duplicate key %q detected in mapping at line %d", keyNode.Value, keyNode.Line)
		}
		seen[keyNode.Value] = true
		stack = append(stack, node.Content[i+1])
	}
	return stack
}

func assertNoDuplicateKeys(t *testing.T, root *yaml.Node) {
	t.Helper()
	if root == nil {
		return
	}
	stack := []*yaml.Node{root}
	const maxNodes = 50000
	visited := 0
	for len(stack) > 0 && visited < maxNodes {
		visited++
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node == nil {
			continue
		}
		switch node.Kind {
		case yaml.DocumentNode, yaml.SequenceNode:
			stack = pushSequenceChildren(stack, node)
		case yaml.MappingNode:
			stack = checkMappingDuplicates(t, stack, node)
		}
	}
}

func assertMappingScalarValue(t *testing.T, mapping *yaml.Node, key, want string) {
	t.Helper()
	node := findYAMLMappingKey(mapping, key)
	if node == nil {
		t.Fatalf("missing key %q in mapping", key)
	}
	if node.Value != want {
		t.Errorf("key %q = %q, want %q", key, node.Value, want)
	}
}

func TestStripGolangciConfig_FlowStyleAndDuplicateKeys_Positive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "flow.yml")
	out := filepath.Join(dir, "flow_out.yml")
	content := "version: \"2\"\nissues: {max-same-issues: 3, uniq-by-line: true}\nrun: {relative-path-mode: cfg, timeout: 5m}\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write flow.yml: %v", err)
	}

	if err := StripGolangciConfig(src, out); err != nil {
		t.Fatalf("StripGolangciConfig failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	assertNoDuplicateKeys(t, &root)

	if len(root.Content) == 0 {
		t.Fatal("empty yaml document")
	}
	doc := root.Content[0]

	runNode := findYAMLMappingKey(doc, "run")
	if runNode == nil {
		t.Fatal("missing run mapping")
	}
	assertMappingScalarValue(t, runNode, "relative-path-mode", "gitroot")
	assertMappingScalarValue(t, runNode, "timeout", "5m")

	issuesNode := findYAMLMappingKey(doc, "issues")
	if issuesNode == nil {
		t.Fatal("missing issues mapping")
	}
	assertMappingScalarValue(t, issuesNode, "max-issues-per-linter", "0")
	assertMappingScalarValue(t, issuesNode, "max-same-issues", "0")
	assertMappingScalarValue(t, issuesNode, "uniq-by-line", "false")
}

func TestStripGolangciConfig_CommentDoesNotBlockKeys_Positive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "comment.yml")
	out := filepath.Join(dir, "comment_out.yml")
	content := "# note: max-issues-per-linter: 0 in comment\nversion: \"2\"\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write comment.yml: %v", err)
	}

	if err := StripGolangciConfig(src, out); err != nil {
		t.Fatalf("StripGolangciConfig failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	assertNoDuplicateKeys(t, &root)

	doc := root.Content[0]
	issuesNode := findYAMLMappingKey(doc, "issues")
	if issuesNode == nil {
		t.Fatal("missing issues mapping")
	}
	assertMappingScalarValue(t, issuesNode, "max-issues-per-linter", "0")
}

func TestStripGolangciConfig_MissingRunAndIssues_Positive(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "missing.yml")
	out := filepath.Join(dir, "missing_out.yml")
	content := "version: \"2\"\nlinters:\n  enable:\n    - gocyclo\n"
	if err := os.WriteFile(src, []byte(content), 0o600); err != nil {
		t.Fatalf("write missing.yml: %v", err)
	}

	if err := StripGolangciConfig(src, out); err != nil {
		t.Fatalf("StripGolangciConfig failed: %v", err)
	}

	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("unmarshal output: %v", err)
	}
	assertNoDuplicateKeys(t, &root)

	doc := root.Content[0]
	runNode := findYAMLMappingKey(doc, "run")
	if runNode == nil {
		t.Fatal("missing run mapping")
	}
	assertMappingScalarValue(t, runNode, "relative-path-mode", "gitroot")

	issuesNode := findYAMLMappingKey(doc, "issues")
	if issuesNode == nil {
		t.Fatal("missing issues mapping")
	}
	assertMappingScalarValue(t, issuesNode, "max-issues-per-linter", "0")
	assertMappingScalarValue(t, issuesNode, "max-same-issues", "0")
	assertMappingScalarValue(t, issuesNode, "uniq-by-line", "false")
}
