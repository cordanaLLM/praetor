package adopt

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Positive: every governance document adoption scaffolds passes markdownlint's default
// rules without disabling any of them.
func TestScaffoldedGovernanceDocumentsPassDefaultMarkdownlint(t *testing.T) {
	documents := map[string]string{
		contributingFile: buildContributingGuide("acme-widgets"),
		prTemplateFile:   buildPullRequestTemplate(),
		securityFile:     buildSecurityPolicy(),
		adrIndexFile:     buildADRIndex(),
		adrTemplateFile:  buildADRTemplate(),
	}
	for name, doc := range documents {
		if findings := testsupport.MarkdownFindings(doc); len(findings) != 0 {
			t.Errorf("%s breaks markdownlint defaults: %v\n%s", name, findings, doc)
		}
		if strings.Contains(doc, "markdownlint-disable") {
			t.Errorf("%s disables a lint rule it should satisfy", name)
		}
	}
}

// Positive: the personas adoption writes pass markdownlint's default rules; their front
// matter is skipped as markdownlint skips it, so the heading and fence rules and the line
// limit apply to the persona body.
func TestGeneratedPersonasPassDefaultMarkdownlint(t *testing.T) {
	personas := generatedPersonas()
	if len(personas) == 0 {
		t.Fatal("adoption generates no personas; the lint below would prove nothing")
	}
	for i := 0; i < len(personas) && i < maxTranspileTargets; i++ {
		doc := string(personas[i].content)
		if findings := testsupport.MarkdownFindings(doc); len(findings) != 0 {
			t.Errorf("%s breaks markdownlint defaults: %v\n%s", personas[i].rel, findings, doc)
		}
		if strings.Contains(doc, "markdownlint-disable") {
			t.Errorf("%s disables a lint rule it should satisfy", personas[i].rel)
		}
	}
}

// adoptionHarness renders the harness for a fixture repository.
func adoptionHarness(t *testing.T) string {
	t.Helper()
	harness, err := buildAgentHarness("fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}})
	if err != nil {
		t.Fatal(err)
	}
	return harness
}

// Positive and negative: the harness passes with its own scoped MD013 disable, and that
// disable ends at the harness, so a long line in the repository's own instructions is
// still reported while the repository's H1 is not (MD025 is disabled for the tail only).
func TestHarnessLintScopeEndsAtTheRepositoryInstructions(t *testing.T) {
	harness := adoptionHarness(t)
	if findings := testsupport.MarkdownFindings(harness); len(findings) != 0 {
		t.Fatalf("harness breaks markdownlint defaults: %v", findings)
	}
	if strings.Contains(harness, "MD013 MD025") {
		t.Fatal("harness still disables MD013 and MD025 together from its first line")
	}
	tail := "# Repository Rules\n\n" + strings.Repeat("repository prose ", 8) + "\n"
	merged := harness + harnessSeparator + "\n" + tail
	findings := testsupport.MarkdownFindings(merged)
	if len(findings) != 1 || !strings.Contains(findings[0], "MD013") {
		t.Fatalf("merged AGENTS.md findings = %v, want exactly the tail's long line", findings)
	}
}

// Boundary: the scope comments sit inside the harness boundary, so splitting a merged file
// at the end marker leaves them with the harness and a force refresh cannot copy them into
// the preserved tail.
func TestHarnessLintScopeStaysInsideTheHarnessBoundary(t *testing.T) {
	harness := adoptionHarness(t)
	end := strings.Index(harness, harnessEndMarker)
	scope := strings.Index(harness, harnessLintScopeEnd)
	if scope < 0 || end < 0 || scope > end {
		t.Fatalf("lint scope end at %d, harness end marker at %d", scope, end)
	}
	tail, ok := splitHarnessTail(harness + harnessSeparator + "\n# Repository Rules\n")
	if !ok || strings.Contains(tail, "markdownlint") {
		t.Fatalf("tail after split = %q, %v", tail, ok)
	}
}
