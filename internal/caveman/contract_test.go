package caveman

import (
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestContractDemonstratedProseFails(t *testing.T) {
	text := readFixture(t, filepath.Join("testdata", "contract", "fail-demonstrated-prose.md"))
	if got := len(strings.Fields(text)); got != 56 {
		t.Fatalf("regression fixture has %d words, want 56", got)
	}
	report := Check(text, Options{Kind: KindMessage})
	if report.Passed() || !hasRule(report, RuleGrammar) {
		t.Fatalf("full prose must fail %s: %+v", RuleGrammar, report)
	}
}

func TestContractGrammarClasses(t *testing.T) {
	for name, text := range map[string]string{
		"article":    "a gate",
		"pronoun":    "we finished",
		"copula":     "gate is ready",
		"auxiliary":  "gate will run",
		"politeness": "thanks",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{Kind: KindMessage}); !hasRule(report, RuleGrammar) {
				t.Fatalf("%s must fail %s: %+v", text, RuleGrammar, report.Findings)
			}
		})
	}
	for name, text := range map[string]string{
		"framing": "it looks like gate failed",
		"hedge":   "gate probably failed",
		"please":  "please rerun gate",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{Kind: KindMessage}); report.Passed() {
				t.Fatalf("%s must fail strict grammar", text)
			}
		})
	}
}

func TestContractGrammarContractionsAndModals(t *testing.T) {
	for name, text := range map[string]string{
		"pronoun contraction":       "we're ready",
		"copula contraction":        "gate isn't ready",
		"modal contraction":         "gate can't pass",
		"modal":                     "gate should pass",
		"auxiliary contraction":     "they've finished",
		"curly pronoun contraction": "we’re ready",
		"curly copula contraction":  "gate isn’t ready",
		"curly auxiliary":           "they’ve finished",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{Kind: KindMessage}); !hasRule(report, RuleGrammar) {
				t.Fatalf("%q must fail %s: %+v", text, RuleGrammar, report.Findings)
			}
		})
	}
	for _, modal := range []string{"can", "could", "may", "might", "must", "shall", "should", "would"} {
		t.Run("modal "+modal, func(t *testing.T) {
			text := "gate " + modal + " pass"
			if report := Check(text, Options{Kind: KindMessage}); !hasRule(report, RuleGrammar) {
				t.Fatalf("%q must fail %s: %+v", text, RuleGrammar, report.Findings)
			}
		})
	}
}

func TestContractTableCells(t *testing.T) {
	text := "| key | status |\n| :--- | :--- |\n| gate | we are probably ready |"
	report := Check(text, Options{Kind: KindMessage})
	for _, rule := range []string{RuleGrammar, RuleHedge} {
		if !hasRule(report, rule) {
			t.Errorf("table cell missing %s finding: %+v", rule, report.Findings)
		}
	}
	if got, _ := Compress(text); got != text {
		t.Fatalf("table delimiters changed while linting support was added:\n%s", got)
	}
	long := "| key | detail |\n| :--- | :--- |\n| gate | " + words(DefaultMaxSentenceWords+1) + " |"
	if report := Check(long, Options{Kind: KindContext}); !hasRule(report, RuleLongSentence) {
		t.Fatalf("long table-cell prose must fail %s: %+v", RuleLongSentence, report.Findings)
	}
}

func TestContractBriefSchema(t *testing.T) {
	positive := strings.Join([]string{
		"goal: enforce Caveman contract",
		"inputs: internal/caveman/check.go",
		"return: verdict, changed, ran, evidence, open",
		"evidence: none",
		"task: ci_debugging",
	}, "\n")
	if report := Check(positive, Options{Kind: KindBrief}); !report.Passed() {
		t.Fatalf("documented brief shape must pass: %+v", report.Findings)
	}
	for name, text := range map[string]string{
		"goal not first": "inputs: internal/caveman/check.go\ngoal: enforce contract\nreturn: verdict\nevidence: none\ntask: ci_debugging",
		"missing task":   "goal: enforce contract\ninputs: internal/caveman/check.go\nreturn: verdict\nevidence: none",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{Kind: KindBrief}); report.Passed() || !hasRule(report, RuleMessageShape) {
				t.Fatalf("invalid brief must fail %s: %+v", RuleMessageShape, report.Findings)
			}
		})
	}
}

func TestContractReturnSchema(t *testing.T) {
	positive := readFixture(t, filepath.Join("testdata", "contract", "pass-return-after.md"))
	if report := Check(positive, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("skill return example must pass: %+v", report.Findings)
	}
	quotedField := "verdict: pass\nchanged: none\nran: command \"open: socket\"\nevidence: none\nopen: none"
	if report := Check(quotedField, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("quoted error field name must stay protected: %+v", report.Findings)
	}
	for name, text := range map[string]string{
		"verdict not first": "changed: none\nverdict: pass\nran: go test ./...\nevidence: none\nopen: none",
		"missing evidence":  "verdict: pass\nchanged: none\nran: go test ./...\nopen: none",
		"fields share line": "verdict: pass. changed: none\nran: go test ./...\nevidence: none\nopen: none",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{Kind: KindReturn}); report.Passed() || !hasRule(report, RuleMessageShape) {
				t.Fatalf("invalid return must fail %s: %+v", RuleMessageShape, report.Findings)
			}
		})
	}
	missing := "verdict: pass\nchanged: none\nran: command \"open: socket\"\nevidence: none"
	if report := Check(missing, Options{Kind: KindReturn}); !hasFinding(report, RuleMessageShape, "missing open field") {
		t.Fatalf("embedded quoted field must not satisfy required open field: %+v", report.Findings)
	}
	embedded := "verdict: pass\nchanged: none\nran: command open: socket\nevidence: none"
	if report := Check(embedded, Options{Kind: KindReturn}); !hasFinding(report, RuleMessageShape, "missing open field") {
		t.Fatalf("embedded value label must not satisfy required open field: %+v", report.Findings)
	}
}

func TestContractSkillExamples(t *testing.T) {
	for _, name := range []string{"pass-message-after.md", "pass-boundary-clarity.md"} {
		if report := Check(readFixture(t, filepath.Join("testdata", "contract", name)), Options{Kind: KindMessage}); !report.Passed() {
			t.Errorf("%s must pass: %+v", name, report.Findings)
		}
	}
	if report := Check(readFixture(t, filepath.Join("testdata", "contract", "fail-message-before.md")), Options{Kind: KindMessage}); report.Passed() {
		t.Fatal("skill's prose Before example must fail")
	}
}

func TestContractProtectedText(t *testing.T) {
	text := strings.Join([]string{
		"`we are probably ready`",
		"internal/we/is/file.go",
		`error: "we are probably blocked"`,
		"error: 'we are probably blocked'",
		"error: ‘we are probably blocked’",
		"https://example.test/we/are/probably/ready",
		OffMarker,
		"We are probably ready.",
		OnMarker,
	}, "\n")
	if report := Check(text, Options{Kind: KindMessage}); !report.Passed() {
		t.Fatalf("protected code/path/error/URL/off-region text must pass: %+v", report.Findings)
	}
}

func TestContractCoverageClassifiesEverySkillRule(t *testing.T) {
	wantMechanical := map[MessageKind]string{
		KindMessage: "1",
		KindBrief:   "1,5",
		KindReturn:  "1,5,7",
		KindContext: "",
	}
	for _, kind := range []MessageKind{KindMessage, KindBrief, KindReturn, KindContext} {
		report := Check("verdict: pass", Options{Kind: kind})
		if len(report.Coverage) != SkillRuleCount {
			t.Fatalf("%s coverage has %d rows, want %d", kind, len(report.Coverage), SkillRuleCount)
		}
		for _, row := range report.Coverage {
			if row.Enforcement != EnforcementMechanical && row.Enforcement != EnforcementAdvisory {
				t.Errorf("%s rule %d has unclassified enforcement %q", kind, row.SkillRule, row.Enforcement)
			}
		}
		var mechanical []string
		for _, row := range report.Coverage {
			if row.Enforcement == EnforcementMechanical {
				mechanical = append(mechanical, strconv.Itoa(row.SkillRule))
			}
		}
		if got := strings.Join(mechanical, ","); got != wantMechanical[kind] {
			t.Errorf("%s mechanical rules = %q, want %q", kind, got, wantMechanical[kind])
		}
	}
}

func TestMessageKindBoundary(t *testing.T) {
	if !MessageKind("").Valid() {
		t.Fatal("zero kind must remain valid for source compatibility")
	}
	if MessageKind("unknown").Valid() {
		t.Fatal("unknown kind must be invalid")
	}
	if report := Check("gate ready", Options{}); report.Kind != KindContext {
		t.Fatalf("zero options kind = %q, want %q", report.Kind, KindContext)
	}
}

func hasRule(report Report, rule string) bool {
	for _, finding := range report.Findings {
		if finding.Rule == rule {
			return true
		}
	}
	return false
}

func hasFinding(report Report, rule, excerpt string) bool {
	for _, finding := range report.Findings {
		if finding.Rule == rule && finding.Excerpt == excerpt {
			return true
		}
	}
	return false
}
