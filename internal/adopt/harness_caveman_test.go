package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/caveman"
	"github.com/cordanaLLM/praetor/internal/compiler"
)

// cavemanPlan is a declared verification plan, the common case of an adopted repository.
var cavemanPlan = &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}

// lintHarness lints text the way the context gate does: register block masked, rest checked.
func lintHarness(text string) caveman.Report {
	masked, _ := compiler.MaskRegisterBlock(text)
	return caveman.Check(masked, caveman.Options{})
}

// TestHarnessPassesCavemanLint: a freshly adopted repository must not fail its own gate on
// text praetor wrote, so the harness itself lints clean.
func TestHarnessPassesCavemanLint(t *testing.T) {
	harness, err := buildAgentHarness("fixture", "framework", cavemanPlan)
	if err != nil {
		t.Fatal(err)
	}
	if report := lintHarness(harness); !report.Passed() || report.ProseWords == 0 {
		t.Fatalf("harness must lint clean: %+v", report.Findings)
	}
}

// TestHarnessCavemanLintSeesAdopterProse: the adopter's own part below the harness is linted
// too (operator decision 2026-09-18), so prose there fails.
func TestHarnessCavemanLintSeesAdopterProse(t *testing.T) {
	harness, err := buildAgentHarness("fixture", "framework", cavemanPlan)
	if err != nil {
		t.Fatal(err)
	}
	prose := "Search for an existing implementation before adding one. Grep the repository for the " +
		"capability and extend the code that is already there. Two implementations of one behavior are a " +
		"defect: they drift, and the second one stops matching the first.\n"
	if report := lintHarness(harness + harnessSeparator + "\n" + prose); report.Passed() {
		t.Fatal("adopter prose below the harness must fail the lint")
	}
}

// TestHarnessDropsDiagramKeepsAnchors: the mermaid chart is gone, and every anchor the
// harness parsers depend on is still there byte for byte.
func TestHarnessDropsDiagramKeepsAnchors(t *testing.T) {
	harness, err := buildAgentHarness("fixture", "framework", cavemanPlan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(harness, "```mermaid") || strings.Contains(harness, "flowchart") {
		t.Error("harness still carries the mermaid chart")
	}
	for _, anchor := range []string{"# fixture Agent Operating Harness", "## Core Directives & Invariants", harnessFooterHeading, harnessEndMarker} {
		if !strings.Contains(harness, anchor) {
			t.Errorf("harness lost anchor %q", anchor)
		}
	}
	if !hasHarness(harness) {
		t.Error("hasHarness no longer recognises the harness")
	}
	if tail, ok := splitHarnessTail(harness + harnessSeparator + "\nkept\n"); !ok || tail != "kept" {
		t.Errorf("splitHarnessTail = %q, %v", tail, ok)
	}
}

// TestGeneratedPersonasPassCavemanLint: the context gate lints every canonical persona
// (#369), and adoption writes two of them, so a fresh adoption must not hand the adopter a
// persona that fails praetor's own gate on its next push (#380, #381).
func TestGeneratedPersonasPassCavemanLint(t *testing.T) {
	personas := generatedPersonas()
	if len(personas) == 0 {
		t.Fatal("adoption generates no personas; the lint below would prove nothing")
	}
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		report := lintHarness(string(personas[i].content))
		if report.ProseWords == 0 {
			t.Fatalf("%s: lint saw no prose, so a passing verdict proves nothing", personas[i].rel)
		}
		if !report.Passed() {
			t.Fatalf("%s fails the caveman gate praetor itself enforces: %+v", personas[i].rel, report.Findings)
		}
	}
}

// Negative: the test above only holds because the lint rejects prose. A persona written the
// way these two were before #380, padded past caveman.DefaultMinProseWords so density is
// judged at all, must fail -- otherwise a green run means nothing.
func TestGeneratedPersonaLintRejectsProse(t *testing.T) {
	prose := "---\nname: repo-auditor\ndescription: \"Probe.\"\n---\n\n# Probe\n\n" +
		"You are the authoritative repository governance auditor. Your purpose is to run " +
		"autonomous sweeps across the codebase and the git commits to guarantee adherence " +
		"to the declared standards, and to report every finding to the operator who owns " +
		"the repository under audit.\n"
	report := lintHarness(prose)
	if report.ProseWords < caveman.DefaultMinProseWords {
		t.Fatalf("fixture must exceed the density judgment floor, got %d prose words", report.ProseWords)
	}
	if report.Passed() {
		t.Fatalf("article-heavy persona must fail the lint, got %+v", report)
	}
}

// Boundary: density is judged only from caveman.DefaultMinProseWords prose words up. A
// persona one word short of the floor keeps its articles, so the generated templates cannot
// rely on being short -- they have to be article-free, which is why the fix rewrote them
// rather than trimming them.
func TestGeneratedPersonaLintFloorLeavesShortTextUnjudged(t *testing.T) {
	// "The probe runs." repeated: three prose words each, one article each, well past the
	// 2.0/100 ceiling, and short enough that no other rule fires.
	sentences := (caveman.DefaultMinProseWords - 1) / 3
	short := "# Probe\n\n" + strings.Repeat("The probe runs. ", sentences) + "\n"
	report := lintHarness(short)
	if report.ProseWords >= caveman.DefaultMinProseWords {
		t.Fatalf("fixture must sit under the floor, got %d prose words", report.ProseWords)
	}
	if report.Density() <= caveman.DefaultMaxArticleDensity {
		t.Fatalf("fixture must break the ceiling to prove the floor exempts it, got %.1f", report.Density())
	}
	for _, finding := range report.Findings {
		if finding.Rule == caveman.RuleArticleDensity {
			t.Fatalf("text under the floor must not be judged for density, got %+v", report.Findings)
		}
	}
}
