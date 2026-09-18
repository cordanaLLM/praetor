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
