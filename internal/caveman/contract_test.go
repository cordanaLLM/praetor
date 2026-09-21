package caveman

import (
	"fmt"
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

func TestRuntimeProfileRejectsSourceDocumentEscapes(t *testing.T) {
	base := "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none"
	cases := map[string]string{
		"off region":       "verdict: pass\nchanged: none\nran: none\nevidence: none\nopen: none\n" + OffMarker + "\nwe are probably ready\n" + OnMarker,
		"HTML comment":     base + "\n<!-- we are probably ready -->",
		"fenced prose":     base + "\n```text\nwe are probably ready\n```",
		"heading prose":    base + "\n## we are probably ready",
		"Setext heading":   base + "\nruntime result\n=====",
		"table structure":  base + "\n| key | value |\n| --- | --- |\n| state | ready |",
		"ledger structure": base + "\n- **State**: ready | **Owner**: agent",
		"quoted prose":     "verdict: pass\nchanged: none\nran: command \"we are probably ready\"\nevidence: none\nopen: none",
		"inline prose":     "verdict: pass\nchanged: none\nran: `we are probably ready`\nevidence: none\nopen: none",
		"trailing evidence": "verdict: pass\nchanged: none\nran: none\n" +
			"evidence: proof.json sha256:0123456789ab lines:1 we are probably ready\nopen: none",
		"malformed evidence": "verdict: pass\nchanged: none\nran: none\n" +
			"evidence: proof.json sha256:invalid lines:1\nopen: none",
		"plus trailing evidence": "verdict: pass\nchanged: none\nran: none\n" +
			"+ evidence: proof.json sha256:0123456789ab lines:1 extra\nopen: none",
		"uppercase evidence": "verdict: pass\nchanged: none\nran: none\n" +
			"Evidence: proof.json SHA256:invalid LINES:1\nopen: none",
		"spaced evidence markers": "verdict: pass\nchanged: none\nran: none\n" +
			"evidence: proof.json sha256 : invalid lines : 1\nopen: none",
		"zero-width grammar": "verdict: pass\nchanged: none\nran: w\u200be a\u200bre ready\n" +
			"evidence: none\nopen: none",
		"default-ignorable combining grammar": "verdict: pass\nchanged: none\nran: w\u034fe a\u034fre ready\n" +
			"evidence: none\nopen: none",
		"at punctuation grammar": "verdict: pass\nchanged: none\nran: the@@@ gate is@@@ ready\n" +
			"evidence: none\nopen: none",
		"at separator grammar": "verdict: pass\nchanged: none\nran: we@are ready\n" +
			"evidence: none\nopen: none",
		"at joined grammar": "verdict: pass\nchanged: none\nran: w@e ready\n" +
			"evidence: none\nopen: none",
		"backslash punctuation grammar": "verdict: pass\nchanged: none\nran: w\\e ready\n" +
			"evidence: none\nopen: none",
		"backslash separator grammar": "verdict: pass\nchanged: none\nran: we\\are ready\n" +
			"evidence: none\nopen: none",
		"slash separator grammar": "verdict: pass\nchanged: none\nran: we/are ready\n" +
			"evidence: none\nopen: none",
		"slash joined grammar": "verdict: pass\nchanged: none\nran: w/e ready\n" +
			"evidence: none\nopen: none",
		"leading hyphen grammar": "verdict: pass\nchanged: none\nran: -we ready\n" +
			"evidence: none\nopen: none",
		"HTML entity grammar": "verdict: pass\nchanged: none\nran: &#119;&#101; &#97;&#114;&#101; ready\n" +
			"evidence: none\nopen: none",
		"HTML tag grammar": "verdict: pass\nchanged: none\nran: <span>we</span> <span>are</span> ready\n" +
			"evidence: none\nopen: none",
		"Markdown link grammar": "verdict: pass\nchanged: none\nran: [we](x) [are](y) ready\n" +
			"evidence: none\nopen: none",
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if report := CheckRuntime(text, Options{Kind: KindReturn}); report.Passed() {
				t.Fatalf("runtime source escape passed: %q", text)
			}
		})
	}
}

func TestRuntimeProfileRejectsGrammarAcrossASCIIPunctuation(t *testing.T) {
	for separator := rune('!'); separator <= '~'; separator++ {
		if separator >= '0' && separator <= '9' || separator >= 'A' && separator <= 'Z' ||
			separator >= 'a' && separator <= 'z' {
			continue
		}
		t.Run(fmt.Sprintf("U+%04X", separator), func(t *testing.T) {
			text := fmt.Sprintf("verdict: pass\nchanged: none\nran: w%ce check\nevidence: none\nopen: none", separator)
			report := CheckRuntime(text, Options{Kind: KindReturn})
			if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
				t.Fatalf("ASCII punctuation U+%04X hid grammar: %+v", separator, report.Findings)
			}
		})
	}
}

func TestRuntimeProfileRejectsGrammarAcrossUnicodePunctuationAndSymbols(t *testing.T) {
	for name, separator := range map[string]rune{
		"fullwidth period": '\uFF0E',
		"middle dot":       '\u00B7',
		"em dash":          '\u2014',
		"fullwidth slash":  '\uFF0F',
		"division slash":   '\u2215',
		"one dot leader":   '\u2024',
	} {
		t.Run(name, func(t *testing.T) {
			text := fmt.Sprintf("verdict: pass\nchanged: none\nran: w%ce check\nevidence: none\nopen: none", separator)
			report := CheckRuntime(text, Options{Kind: KindReturn})
			if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
				t.Fatalf("Unicode separator U+%04X hid grammar: %+v", separator, report.Findings)
			}
		})
	}
}

func TestRuntimeProfileRejectsUnsafeControlCharacters(t *testing.T) {
	ranges := [][2]rune{{0x00, 0x08}, {0x0B, 0x1F}, {0x7F, 0x9F}}
	for _, bounds := range ranges {
		for control := bounds[0]; control <= bounds[1]; control++ {
			t.Run(fmt.Sprintf("U+%04X", control), func(t *testing.T) {
				text := fmt.Sprintf("verdict: pass\nchanged: none\nran: w%ce check\nevidence: none\nopen: none", control)
				report := CheckRuntime(text, Options{Kind: KindReturn})
				if !hasRule(report, RuleTerminalNoise) {
					t.Fatalf("unsafe control U+%04X passed: %+v", control, report.Findings)
				}
			})
		}
	}

	safeWhitespace := "verdict:\tpass\r\nchanged: none\r\nran: w\te check\r\nevidence: none\r\nopen: none"
	if report := CheckRuntime(safeWhitespace, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("tab and CRLF boundaries must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfilePreservesNarrowLiteralTokens(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: internal/we/is/value.go /work/we/is C:\\work\\we\\is\\value.go\n" +
		"ran: go test ./... -run TestValue --run=TestValue; command \"open: socket\"; agent@example.test; " +
		"<https://example.test/log> HTTPS://example.test/we/is\n" +
		"+ evidence: .workingdir/evidence/proof.json sha256:0123456789ab lines:1\n" +
		"open: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("runtime literals must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfilePreservesCompleteURLAndPathLiterals(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: C:/work/we/is/value.go internal/we/is/value.go:42 /work/we/is/value.go:42:7 " +
		"C:\\work\\we\\is\\value.go:42 C:/work/we/is/value.go:42\n" +
		"ran: https://example.test/log_(we)/is (HTTPS://example.test/log_(we)/is) " +
		"\"C:/work/we/is/value.go\"\n" +
		"evidence: none\n" +
		"open: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("complete URL and path literals must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfilePreservesVisibleCombiningText(t *testing.T) {
	for name, combining := range map[string]string{
		"ordinary acute": "cafe\u0301",
		"before CGJ":     "x\u034ey",
		"after CGJ":      "x\u0350y",
	} {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + combining + " check\nevidence: none\nopen: none"
			if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
				t.Fatalf("visible combining mark must pass: %+v", report.Findings)
			}
		})
	}
}

func TestRuntimeProfileRejectsVisibleCombiningGrammar(t *testing.T) {
	text := "verdict: pass\nchanged: none\nran: w\u0301e ready\nevidence: none\nopen: none"
	report := CheckRuntime(text, Options{Kind: KindReturn})
	if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
		t.Fatalf("visible combining mark hid grammar: %+v", report.Findings)
	}
}

func TestRuntimeProfileRejectsSegmentedFlags(t *testing.T) {
	for _, flag := range []string{"--we.", "--w.e", "--w-e", "--w_e"} {
		t.Run(flag, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + flag + "\nevidence: none\nopen: none"
			report := CheckRuntime(text, Options{Kind: KindReturn})
			if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
				t.Fatalf("segmented flag %q hid grammar: %+v", flag, report.Findings)
			}
		})
	}
	valid := "verdict: pass\nchanged: none\nran: command --max-words=3 --write-output --run=TestValue\nevidence: none\nopen: none"
	if report := CheckRuntime(valid, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("technical flags must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfileRejectsSegmentedPhrases(t *testing.T) {
	cases := map[string]string{
		"prob.ably":    RuleHedge,
		"note.that":    RuleFiller,
		"in.order.to":  RuleFiller,
		"as.requested": RuleFiller,
		"note\nthat":   RuleFiller,
	}
	for phrase, rule := range cases {
		t.Run(strings.ReplaceAll(phrase, "\n", "-newline-"), func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + phrase + "\nevidence: none\nopen: none"
			if report := CheckRuntime(text, Options{Kind: KindReturn}); !hasRule(report, rule) {
				t.Fatalf("segmented phrase %q missing %s: %+v", phrase, rule, report.Findings)
			}
		})
	}
}

func TestRuntimeProfilePreservesPathEmailAndCombiningBoundaries(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: internal/(we)/is/value.go\n" +
		"ran: notify we@example.com. cafe\u0301 check\n" +
		"evidence: none\nopen: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("path, sentence email, and visible combining prose must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfileRejectsCompatibilityAndSeparatorEvasions(t *testing.T) {
	cases := map[string]struct {
		value string
		rule  string
		want  string
	}{
		"line separator":      {"value\u2028check", RuleTerminalNoise, "U+2028 line separator"},
		"paragraph separator": {"value\u2029check", RuleTerminalNoise, "U+2029 line separator"},
		"modifier apostrophe": {"we\u02bcre ready", RuleGrammar, `pronoun "we're"`},
		"modifier hedge":      {"prob\u02bcably", RuleHedge, "probably"},
		"fullwidth letters":   {"\uff57\uff45 ready", RuleGrammar, `pronoun "we"`},
		"fullwidth flag":      {"\uff0d\uff0d\uff57\uff0d\uff45", RuleGrammar, `pronoun "we"`},
		"circled letters":     {"ⓦⓔ ready", RuleGrammar, `pronoun "we"`},
		"bold letters":        {"𝐰𝐞 ready", RuleGrammar, `pronoun "we"`},
		"double-struck":       {"𝕨𝕖 ready", RuleGrammar, `pronoun "we"`},
		"modifier letters":    {"ʷᵉ ready", RuleGrammar, `pronoun "we"`},
		"pseudo path":         {"we／are／value．go", RuleGrammar, `pronoun "we"`},
		"pseudo mail":         {"we＠example．com", RuleGrammar, `pronoun "we"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + tc.value + "\nevidence: none\nopen: none"
			report := CheckRuntime(text, Options{Kind: KindReturn})
			if !hasFinding(report, tc.rule, tc.want) {
				t.Fatalf("compatibility escape passed: %+v", report.Findings)
			}
		})
	}
}

func TestRuntimeProfileRejectsApostropheConfusables(t *testing.T) {
	for name, apostrophe := range map[string]rune{
		"turned comma":          '\u02BB',
		"prime":                 '\u02B9',
		"acute accent":          '\u02CA',
		"grave accent":          '\u02CB',
		"small letter saltillo": '\uA78C',
	} {
		for form, value := range map[string]string{
			"plain":    fmt.Sprintf("we%cre", apostrophe),
			"adjacent": fmt.Sprintf("(we%cre),", apostrophe),
		} {
			t.Run(name+"/"+form, func(t *testing.T) {
				text := "verdict: pass\nchanged: none\nran: " + value + " ready\nevidence: none\nopen: none"
				report := CheckRuntime(text, Options{Kind: KindReturn})
				if !hasFinding(report, RuleGrammar, `pronoun "we're"`) {
					t.Fatalf("apostrophe confusable U+%04X hid grammar: %+v", apostrophe, report.Findings)
				}
			})
		}
	}
}

func TestRuntimeProfileRejectsCompatibilityLetterFamilies(t *testing.T) {
	for name, value := range map[string]string{
		"legacy script":       "𝓌ℯ",
		"parenthesized Latin": "⒲⒠",
		"small capitals":      "ᴡᴇ",
		"modifier subscript":  "ʷₑ",
	} {
		for form, candidate := range map[string]string{
			"plain":    value,
			"adjacent": "(" + value + "),",
		} {
			t.Run(name+"/"+form, func(t *testing.T) {
				text := "verdict: pass\nchanged: none\nran: " + candidate + " ready\nevidence: none\nopen: none"
				report := CheckRuntime(text, Options{Kind: KindReturn})
				if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
					t.Fatalf("compatibility letters %q hid grammar: %+v", candidate, report.Findings)
				}
			})
		}
	}
}

func TestRuntimeProfilePreservesConfusableNonGrammar(t *testing.T) {
	values := []string{"rock\u02BBn", "rock\u02B9n", "rock\u02CAn", "rock\u02CBn", "rock\uA78Cn", "𝓌ℯb", "⒲⒠b", "ᴡᴇb", "ʷₑb"}
	text := "verdict: pass\nchanged: none\nran: " + strings.Join(values, " ") + "\nevidence: none\nopen: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("non-grammar compatibility text must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfilePreservesMSVCDefineFlags(t *testing.T) {
	valid := []string{"/DDEBUG", "/DDEBUG=1", "/Dwe", "/Dwe=are", "(/Dwe=are),"}
	text := "verdict: pass\nchanged: none\nran: clang-cl " + strings.Join(valid, " ") + " source.c\nevidence: none\nopen: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("valid /Dmacro[=value] flags must pass: %+v", report.Findings)
	}
	for _, token := range []string{"/D-we", "/D=we", "/D.we"} {
		t.Run(token, func(t *testing.T) {
			candidate := "verdict: pass\nchanged: none\nran: " + token + " ready\nevidence: none\nopen: none"
			report := CheckRuntime(candidate, Options{Kind: KindReturn})
			if !hasFinding(report, RuleGrammar, `pronoun "we"`) {
				t.Fatalf("invalid MSVC define lookalike hid grammar: %+v", report.Findings)
			}
		})
	}
	for token, want := range map[string]bool{
		"/D_": true, "/D_A9=1": true, "/D9A": false, "/DNAME=": false, "/DNAME==1": false,
	} {
		if got := protectedLiteralToken(token); got != want {
			t.Errorf("protectedLiteralToken(%q) = %v, want %v", token, got, want)
		}
	}
}

func TestCompatibilityFoldBoundary(t *testing.T) {
	input := "\uFF00\uFF01\uFF21\uFF5E\uFF5F\u3000\u2018\u2019\u02BC"
	want := "\uFF00!A~\uFF5F '''"
	if got := compatibilityFold(input); got != want {
		t.Fatalf("compatibilityFold(%q) = %q, want %q", input, got, want)
	}
	if report := Check("\uff54\uff48\uff45 "+sentences(39), Options{}); report.Articles != 1 {
		t.Fatalf("fullwidth article must reach density accounting: %+v", report)
	}
}

func TestCompatibilityFoldApostropheConfusableBoundary(t *testing.T) {
	for _, char := range []rune{'‘', '’', '\u02BC', '\u02BB', '\u02B9', '\u02CA', '\u02CB', '\uA78C'} {
		if got := compatibilityRune(char); got != '\'' {
			t.Errorf("compatibilityRune(U+%04X) = U+%04X, want apostrophe", char, got)
		}
	}
	for _, char := range []rune{'\u02BA', '\u02BD', '\u02C9', '\u02CC', '\uA78B', '\uA78D'} {
		if got := compatibilityRune(char); got != char {
			t.Errorf("adjacent U+%04X folded to U+%04X", char, got)
		}
	}
}

func TestCompatibilityFoldLetterFamilies(t *testing.T) {
	for name, tc := range map[string]struct {
		input string
		want  string
	}{
		"circled bounds":            {"ⒶⓏⓐⓩ", "AZaz"},
		"mathematical bold":         {"𝐀𝐙𝐚𝐳", "AZaz"},
		"mathematical double":       {"𝕒𝕫", "az"},
		"mathematical monospace":    {"𝙰𝚉𝚊𝚣", "AZaz"},
		"legacy mathematical":       {"ℂℊℋℌℍℎℐℑℒℓℕℙℚℛℜℝℤℨℬℭℯℰℱℳℴ", "CgHHHhIILlNPQRRRZZBCeEFMo"},
		"parenthesized bounds":      {"\u249B⒜⒵Ⓐ", "\u249BazA"},
		"small capitals":            {"ᴡᴇ", "we"},
		"modifier letters":          {"ᵃᵉⁱᵒᵘʷ", "aeiouw"},
		"subscript letters":         {"ₐₑₒₓₕₖₗₘₙₚₛₜ", "aeoxhklmnpst"},
		"adjacent values unchanged": {"⓪\U0001D3FF", "⓪\U0001D3FF"},
	} {
		t.Run(name, func(t *testing.T) {
			if got := compatibilityFold(tc.input); got != tc.want {
				t.Fatalf("compatibilityFold(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
	for _, token := range []string{"we／are／value．go", "we＠example．com"} {
		if protectedGrammarToken(token) {
			t.Errorf("compatibility punctuation manufactured protected literal: %q", token)
		}
	}
}

func TestRuntimeProfilePreservesWrappedPlatformLiteralsAndShortFlags(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: (internal/(we)/is/value.go). internal/(we)/is/ internal\\(we)\\is\\ " +
		"internal/(we)/is/value.go:42). (C:\\café\\(we)\\is\\value.go:42). " +
		"C:\\café\\(we)\\is\\value.go:42). (\\\\sérver\\share\\(we)\\is\\value.go). " +
		"\\\\sérver\\share\\(we)\\is\\value.go:42).\n" +
		"ran: notify (we@example.com). -I -i -a /I clang -I/usr/include source.c " +
		"clang -I=/usr/include source.c rock\u02bcn\n" +
		"evidence: none\nopen: none"
	if report := CheckRuntime(text, Options{Kind: KindReturn}); !report.Passed() {
		t.Fatalf("wrapped platform literals and short flags must pass: %+v", report.Findings)
	}
}

func TestRuntimeProfileRejectsPathAndFlagLookalikes(t *testing.T) {
	text := "verdict: pass\nchanged: none\nran: we/are /we w\\e C:we --i -we -w.e /w.e\nevidence: none\nopen: none"
	report := CheckRuntime(text, Options{Kind: KindReturn})
	for _, finding := range []string{`pronoun "we"`, `pronoun "i"`} {
		if !hasFinding(report, RuleGrammar, finding) {
			t.Errorf("lookalike missing %q: %+v", finding, report.Findings)
		}
	}
	at := strings.Repeat("x/", maxLiteralPathSegments)
	over := at + "x/"
	if !protectedPath(at) || protectedPath(over) {
		t.Fatalf("path segment bound mismatch: at=%v over=%v", protectedPath(at), protectedPath(over))
	}
	base := "internal/(we)/is/value.go:42"
	atWrappers := base + strings.Repeat(")", maxLiteralWrapperDepth)
	overWrappers := base + strings.Repeat(")", maxLiteralWrapperDepth+1)
	if !protectedGrammarToken(atWrappers) || protectedGrammarToken(overWrappers) {
		t.Fatalf("wrapper bound mismatch: at=%v over=%v", protectedGrammarToken(atWrappers), protectedGrammarToken(overWrappers))
	}
	for _, flag := range []string{"-I=", "-Ivalue", "-w.e", "/w.e"} {
		if shortTechnicalFlag(flag) {
			t.Errorf("short-option lookalike protected: %q", flag)
		}
	}
}

func TestRuntimeProfilePhraseFindingsUseSourceLineOnce(t *testing.T) {
	sameLine := "verdict: pass\nchanged: none\nran: alpha\nnote that\nevidence: none\nopen: none"
	wrapped := "verdict: pass\nchanged: none\nran: note\nthat\nevidence: none\nopen: none"
	for name, tc := range map[string]struct {
		text string
		line int
	}{"same line": {sameLine, 4}, "wrapped": {wrapped, 3}} {
		t.Run(name, func(t *testing.T) {
			report := CheckRuntime(tc.text, Options{Kind: KindReturn})
			matches := 0
			for _, finding := range report.Findings {
				if finding.Rule == RuleFiller && finding.Excerpt == "note that" {
					matches++
					if finding.Line != tc.line {
						t.Errorf("finding line = %d, want %d", finding.Line, tc.line)
					}
				}
			}
			if matches != 1 {
				t.Fatalf("note that findings = %d, want 1: %+v", matches, report.Findings)
			}
		})
	}
}

func TestRuntimeProfileJoinsWrappedSentences(t *testing.T) {
	text := strings.TrimSpace(strings.Repeat("alpha ", 20)) + "\n" + strings.TrimSpace(strings.Repeat("beta ", 20))
	if report := CheckRuntime(text, Options{Kind: KindMessage}); !hasRule(report, RuleLongSentence) {
		t.Fatalf("wrapped 40-word sentence escaped runtime ceiling: %+v", report.Findings)
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
