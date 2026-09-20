package compiler

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	lintTerseAgents = "# Fixture Agent Operating Harness\n\n1. **Verify.** Run `make verify-all` before turn end.\n"
	lintProseAgents = "# Fixture\n\nSearch for an existing implementation before adding one. Grep the repository for the " +
		"capability and extend the code that is already there. Two implementations of one behavior are a " +
		"defect: they drift, and the second one stops matching the first.\n"
)

// lintFixture writes AGENTS.md into a fresh root and returns its path.
func lintFixture(t *testing.T, agents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "AGENTS.md")
	if err := os.WriteFile(path, []byte(agents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestCanonicalAgentsPassesCavemanLint runs the gate on this repository: the live AGENTS.md
// lints clean.
func TestCanonicalAgentsPassesCavemanLint(t *testing.T) {
	lint, err := LintContext(context.Background(), canonicalAgents)
	if err != nil {
		t.Fatal(err)
	}
	if !lint.Report.Passed() || lint.Report.ProseWords == 0 || lint.MaskedLines == 0 {
		t.Fatalf("live AGENTS.md: %+v", lint)
	}
}

func TestLintContextPositive(t *testing.T) {
	lint, err := LintContext(context.Background(), lintFixture(t, lintTerseAgents))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lint.Summary(), "caveman lint passed: 5 prose words, 0.0 articles per 100 (limit 2.0)") {
		t.Fatalf("terse AGENTS.md: %q", lint.Summary())
	}
}

func TestLintContextNegative(t *testing.T) {
	path := lintFixture(t, lintProseAgents)
	lint, err := LintContext(context.Background(), path)
	if !errors.Is(err, ErrContextProse) || lint.Report.Passed() {
		t.Fatalf("prose AGENTS.md must fail: err=%v", err)
	}
	for _, want := range []string{"C1 article-density", "praetorctl caveman check --kind=context " + path, "rewrite the flagged lines in caveman"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error lacks %q: %v", want, err)
		}
	}
	if strings.Contains(err.Error(), "opt out") {
		t.Errorf("the gate has no opt-out, so the error must not offer one: %v", err)
	}
	if _, err := LintContext(context.Background(), filepath.Join(filepath.Dir(path), "absent.md")); err == nil {
		t.Error("a missing AGENTS.md must fail")
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := LintContext(cancelled, path); err == nil {
		t.Error("a cancelled context must fail")
	}
}

func TestLintContextBoundary(t *testing.T) {
	// A manifest beside AGENTS.md changes nothing: the gate reads none, so neither
	// surfaces.agent nor any other row can switch it off.
	path := lintFixture(t, lintProseAgents)
	manifest := "version: 1\nregister:\n  surfaces:\n    agent: docs\n"
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), ".standards.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LintContext(context.Background(), path); !errors.Is(err, ErrContextProse) {
		t.Fatalf("surfaces.agent = docs must not switch the gate off: %v", err)
	}
	// An empty AGENTS.md has no prose to fail.
	if lint, err := LintContext(context.Background(), lintFixture(t, "")); err != nil || lint.Report.ProseWords != 0 {
		t.Fatalf("empty AGENTS.md: err=%v %+v", err, lint)
	}
	// Quoted findings are bounded; the rest are counted.
	many := strings.Repeat("please.\n\n", maxQuotedLintFindings+2)
	_, err := LintContext(context.Background(), lintFixture(t, many))
	if err == nil || !strings.Contains(err.Error(), "7 finding(s)") || !strings.Contains(err.Error(), "; +2 more") {
		t.Fatalf("finding bound: %v", err)
	}
}

// registerProse is prose between the register markers: the gate leaves it to its renderer.
var registerProse = config.RegisterBlockStart + "\n" +
	strings.TrimSuffix(strings.TrimPrefix(lintProseAgents, "# Fixture\n\n"), "\n") + "\n" + config.RegisterBlockEnd

func TestMaskRegisterBlock(t *testing.T) {
	content := "# Title\n\nterse line\n" + registerProse + "\ntail\n"
	masked, lines := MaskRegisterBlock(content)
	if lines != 3 || strings.Contains(masked, "implementation") || !strings.Contains(masked, "terse line") || !strings.Contains(masked, "tail") {
		t.Fatalf("masked %d lines:\n%s", lines, masked)
	}
	if strings.Count(masked, "\n") != strings.Count(content, "\n") {
		t.Error("masking must keep every other line on its number")
	}
	// No markers, one marker, or markers inside a fence: nothing is masked.
	for name, text := range map[string]string{
		"no markers": lintProseAgents,
		"unbalanced": config.RegisterBlockStart + "\nprose\n",
		"fenced":     "```md\n" + registerProse + "\n```\n",
	} {
		if got, n := MaskRegisterBlock(text); got != text || n != 0 {
			t.Errorf("%s: masked %d lines", name, n)
		}
	}
}

func TestLintAgentTextPositive(t *testing.T) {
	report, err := LintAgentText(".agents/skills/example/SKILL.md", lintTerseAgents)
	if err != nil || !report.Passed() {
		t.Fatalf("terse persona/skill text must pass: err=%v report=%+v", err, report)
	}
}

func TestLintAgentTextNegative(t *testing.T) {
	report, err := LintAgentText(".agents/skills/example/SKILL.md", lintProseAgents)
	if !errors.Is(err, ErrAgentTextProse) || report.Passed() {
		t.Fatalf("prose persona/skill text must fail: err=%v report=%+v", err, report)
	}
	if !strings.Contains(err.Error(), ".agents/skills/example/SKILL.md") {
		t.Errorf("error must name the label: %v", err)
	}
}

// wordsWithBreaks returns n prose words without articles, with a period every 8 words, so
// the text carries no C1 article-density or C5 long-sentence finding of its own.
func wordsWithBreaks(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		sb.WriteString("gate")
		if i%8 == 0 {
			sb.WriteString(".")
		}
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String())
}

func TestLintAgentTextBoundary(t *testing.T) {
	// A word ceiling breach fails even when the article density and every other C1-C6 rule
	// pass: the ceiling is independent of the prose rules.
	report, err := LintAgentText("over.md", wordsWithBreaks(AgentTextCeiling+50))
	if !errors.Is(err, ErrAgentTextProse) || report.Passed() {
		t.Fatalf("text over the ceiling must fail even with clean prose: err=%v report=%+v", err, report)
	}
	if report.ProseWords <= AgentTextCeiling {
		t.Fatalf("fixture must exceed the ceiling: %d words", report.ProseWords)
	}
	foundCeiling := false
	for _, f := range report.Findings {
		if f.Rule == "C7 word-ceiling" {
			foundCeiling = true
		}
	}
	if !foundCeiling {
		t.Errorf("findings must include the word-ceiling rule: %v", report.Findings)
	}
	// Exactly at the ceiling passes.
	at := strings.TrimSuffix(strings.Repeat("gate.\n", AgentTextCeiling), "\n")
	if _, err := LintAgentText("at.md", at); err != nil {
		t.Fatalf("exactly at the ceiling must pass: %v", err)
	}
}

func TestLintContextLeavesRegisterBlockToRenderer(t *testing.T) {
	lint, err := LintContext(context.Background(), lintFixture(t, lintTerseAgents+"\n"+config.RegisterSectionPrefix+registerProse+"\n"))
	if err != nil || lint.MaskedLines != 3 || !strings.HasSuffix(lint.Summary(), "3 register block lines left to the renderer") {
		t.Fatalf("prose inside the block must not fail the gate: err=%v summary=%q", err, lint.Summary())
	}
	// The same prose outside the markers fails.
	if _, err := LintContext(context.Background(), lintFixture(t, lintTerseAgents+"\n"+lintProseAgents)); !errors.Is(err, ErrContextProse) {
		t.Fatalf("prose outside the block must fail: %v", err)
	}
}
