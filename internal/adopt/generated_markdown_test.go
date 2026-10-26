package adopt

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
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

// skillLintRules are the markdownlint rules the register skills shipped before #70 broke in a
// repository that lints every Markdown file with the default rules, per skill, at its last
// text before the fix.
var skillLintRules = map[string]struct {
	prior string
	rules []string
}{
	"social-text": {"ship-1099.SKILL.md.prior", []string{"MD013"}},
	"caveman":     {"ship-850.SKILL.md.prior", []string{"MD013", "MD058"}},
	"adhd-format": {"ship-850.SKILL.md.prior", []string{"MD013", "MD022", "MD032"}},
}

// Positive: the register skills adoption installs, in .agents/skills and in the copy Claude
// Code reads, pass markdownlint's default rules without disabling any of them, so an adopter
// whose Markdown gate covers the whole tree stays green after adoption (#70). The audit holds
// each client copy to its source byte for byte, so an adopter cannot fix a finding there.
func TestAdopt_Positive_InstalledSkillsPassDefaultMarkdownlint(t *testing.T) {
	repoPath := newTestRepo(t, "register-skills-markdownlint")
	adoptWithSource(t, repoPath, newAdoptLockSource(t), false)
	bundle := config.RegisterSkillBundle()
	if len(bundle) != len(skillLintRules) {
		t.Fatalf("the bundle holds %d skills, skillLintRules %d", len(bundle), len(skillLintRules))
	}
	for _, dir := range []string{compiler.CanonicalSkillsRel, ".claude/skills"} {
		for _, name := range bundle {
			rel := compiler.SkillEntryRel(dir, name)
			doc := repoText(t, repoPath, rel)
			if findings := testsupport.MarkdownFindings(doc); len(findings) != 0 {
				t.Errorf("%s breaks markdownlint defaults: %v", rel, findings)
			}
			if strings.Contains(doc, "markdownlint-disable") {
				t.Errorf("%s disables a lint rule it should satisfy", rel)
			}
		}
	}
}

// Negative (rule 13): the same check refuses each skill's last text before the fix, naming
// every rule that text broke under markdownlint-cli2 with the default rules, so the clean
// result above is not a check that finds nothing.
func TestInstalledSkillLintRefusesThePriorTexts(t *testing.T) {
	for name, want := range skillLintRules {
		prior := mustRead(t, filepath.Join(priorSkillFixtures, name, want.prior))
		findings := strings.Join(testsupport.MarkdownFindings(prior), "\n")
		for _, rule := range want.rules {
			if !strings.Contains(findings, rule) {
				t.Errorf("%s/%s: findings do not report %s:\n%s", name, want.prior, rule, findings)
			}
		}
	}
}

// Boundary: one blank line taken away around a heading, a quoted list or a table of an
// installed skill is reported under the rule that asks for it, so the check sees each of the
// three structures the skills hold, not only a long line.
func TestInstalledSkillLintSeesEachStructure(t *testing.T) {
	cases := []struct{ skill, glued, rule string }{
		{"adhd-format", "\n\n> ### ", "MD022"},
		{"adhd-format", ":\n>\n> - ", "MD032"},
		{"caveman", " |\n\n2. ", "MD058"},
	}
	for _, tc := range cases {
		// A CRLF checkout (core.autocrlf=true on Windows) holds the same text.
		doc := strings.ReplaceAll(sourceSkillText(t, tc.skill), "\r\n", "\n")
		if strings.Count(doc, tc.glued) != 1 {
			t.Fatalf("%s holds %q %d times, want once", tc.skill, tc.glued, strings.Count(doc, tc.glued))
		}
		planted := strings.Replace(doc, tc.glued, glue(tc.glued), 1)
		for ending, text := range map[string]string{"LF": planted, "CRLF": strings.ReplaceAll(planted, "\n", "\r\n")} {
			findings := testsupport.MarkdownFindings(text)
			if !slices.ContainsFunc(findings, func(finding string) bool { return strings.Contains(finding, tc.rule) }) {
				t.Errorf("%s (%s) without the blank line in %q: findings %v do not report %s", tc.skill, ending, tc.glued, findings, tc.rule)
			}
		}
	}
}

// glue returns separator without its blank line: the first empty line, or the first line
// holding only a quote marker.
func glue(separator string) string {
	for _, blank := range []string{"\n>\n", "\n\n"} {
		if strings.Contains(separator, blank) {
			return strings.Replace(separator, blank, "\n", 1)
		}
	}
	return separator
}

// adoptionHarness renders the harness for a fixture repository.
func adoptionHarness(t *testing.T) string {
	t.Helper()
	harness, err := buildAgentHarness(adoptedFacts("", "fixture", "framework", &VerificationPlan{Status: verificationDeclared, Build: [][]string{{"make", "build"}}, Test: [][]string{{"make", "test"}}}))
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

// Regression (engine-compile-context-md013-restore-20261026): under a repository markdownlint
// configuration where MD013 is disabled, the rendered harness and tail instructions produce no
// MD013 finding after the harness boundary. On base (before the restore fix), the inline enable
// pragma turned MD013 back on, flagging tail lines over the limit.
func TestHarnessLintScope_PreservesRepoMD013Disabled(t *testing.T) {
	harness := adoptionHarness(t)
	tail := "# Repository Rules\n\n" + strings.Repeat("repository prose ", 8) + "\n"
	merged := harness + harnessSeparator + "\n" + tail
	findings := testsupport.MarkdownFindingsWith(merged, "MD013")
	if len(findings) != 0 {
		t.Fatalf("merged AGENTS.md with MD013 off reported findings: %v", findings)
	}
}

// Planted negative: verifies that an inline markdownlint-enable MD013 at harness scope end
// overrides repository-level MD013: false, proving the checker detects the failure mode.
func TestHarnessLintScope_PlantedEnableFailsUnderRepoMD013Disabled(t *testing.T) {
	tail := "# Repository Rules\n\n" + strings.Repeat("repository prose ", 8) + "\n"

	// Inline enable overrides repo config -> finding reported (planted failure proof).
	enableHarness := "# Title\n\n<!-- markdownlint-disable MD013 -->\nshort\n<!-- markdownlint-enable MD013 -->\n<!-- markdownlint-disable MD025 -->\n"
	enableMerged := enableHarness + harnessSeparator + "\n" + tail
	enableFindings := testsupport.MarkdownFindingsWith(enableMerged, "MD013")
	if len(enableFindings) == 0 || !strings.Contains(strings.Join(enableFindings, "\n"), "MD013") {
		t.Fatalf("expected planted inline enable to report MD013 finding, got: %v", enableFindings)
	}

	// Restore pragma respects repo config -> no finding.
	restoreHarness := "<!-- markdownlint-capture -->\n<!-- markdownlint-disable MD013 -->\n# Title\n\nshort\n<!-- markdownlint-restore -->\n<!-- markdownlint-disable MD025 -->\n"
	restoreMerged := restoreHarness + harnessSeparator + "\n" + tail
	restoreFindings := testsupport.MarkdownFindingsWith(restoreMerged, "MD013")
	if len(restoreFindings) != 0 {
		t.Fatalf("expected restore form to produce no findings under MD013 off, got: %v", restoreFindings)
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
	_, tail, ok := splitHarnessTail(harness + harnessSeparator + "\n# Repository Rules\n")
	if !ok || strings.Contains(tail, "markdownlint") {
		t.Fatalf("tail after split = %q, %v", tail, ok)
	}
}
