package caveman

import (
	"reflect"
	"strings"
	"testing"
)

// words returns n prose words without articles and without a sentence break.
func words(n int) string {
	return strings.TrimSpace(strings.Repeat("gate ", n))
}

// sentences returns n prose words without articles, split into ten-word sentences so that
// no sentence trips the length rule.
func sentences(n int) string {
	var sb strings.Builder
	for i := 1; i <= n; i++ {
		sb.WriteString("gate")
		if i%10 == 0 {
			sb.WriteString(".")
		}
		sb.WriteString(" ")
	}
	return strings.TrimSpace(sb.String())
}

func rulesOf(r Report) []string {
	out := make([]string, 0, len(r.Findings))
	for _, f := range r.Findings {
		out = append(out, f.Rule)
	}
	return out
}

func TestCheckPositive(t *testing.T) {
	cases := map[string]struct {
		text string
		want []string
	}{
		"ansi escape":    {"build \x1b[31mred\x1b[0m output", []string{RuleTerminalNoise}},
		"ansi in fence":  {"```\n\x1b[1mbold log\x1b[m\n```", []string{RuleTerminalNoise}},
		"CGJ":            {"w\u034fe", []string{RuleTerminalNoise}},
		"CGJ in fence":   {"```\nw\u034fe\n```", []string{RuleTerminalNoise}},
		"text variation": {"ready\ufe0e", []string{RuleTerminalNoise}},
		"emoji":          {"done 🎉", []string{RuleTerminalNoise}},
		"filler please":  {"Please rerun sync.", []string{RuleFiller}},
		"in order to":    {"rerun sync in order to push", []string{RuleFiller}},
		"hedge actually": {"gate actually passed", []string{RuleHedge}},
		"unclosed off":   {OffMarker + "\nsample", []string{RuleUnclosedOff}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := rulesOf(Check(tc.text, Options{})); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("rules = %v, want %v", got, tc.want)
			}
		})
	}
	report := Check("The gate and the sync. "+sentences(39), Options{})
	if report.Articles != 2 || report.ProseWords != 44 || report.Passed() {
		t.Fatalf("articles %d of %d words, passed %v; want 2 of 44 and a density failure", report.Articles, report.ProseWords, report.Passed())
	}
	if f := report.Findings[0]; f.Line != 0 || f.Rule != RuleArticleDensity || !strings.Contains(f.Excerpt, "4.5 articles per 100 prose words (2/44)") {
		t.Fatalf("density finding = %+v", f)
	}
}

func TestCheckNegative(t *testing.T) {
	for name, text := range map[string]string{
		"empty":                "",
		"blank lines":          "\n\n  \n",
		"code only":            "```go\n// Note that this is probably just a comment in the code.\n```",
		"quoted filler":        `No filler preamble, no "Based on", no "just", no chatter.`,
		"inline code hedge":    "run `just test` then `make please`",
		"heading with article": "## The State of the Ledger and the Gate",
		"table terse":          "| key | status |\n| :--- | :--- |\n| gate | ready |",
		"link target article":  "see [ledger](docs/the/a/an/state.md)",
		"off region":           OffMarker + "\nI think the gate probably failed.\n" + OnMarker,
		"multi-line comment":   "<!--\nNote that the comment is probably prose.\n-->",
		"visible combining":    "cafe\u0301",
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, Options{}); !report.Passed() {
				t.Fatalf("want pass, got %v", report.Findings)
			}
		})
	}
	if got := Check("", Options{}).Density(); got != 0 {
		t.Errorf("density of empty text = %v, want 0", got)
	}
}

func TestCheckBoundary(t *testing.T) {
	// Density is judged from DefaultMinProseWords on: 39 words with 3 articles pass, 40 fail.
	below := Check("the a an "+sentences(36), Options{})
	at := Check("the a an "+sentences(37), Options{})
	if !below.Passed() || below.ProseWords != 39 {
		t.Errorf("39 words: %v, %d words", below.Findings, below.ProseWords)
	}
	if at.Passed() || at.ProseWords != 40 {
		t.Errorf("40 words must be judged: %v, %d words", at.Findings, at.ProseWords)
	}
	// Exactly the density limit passes; the rule fires only above it.
	if report := Check("the "+sentences(49), Options{}); !report.Passed() {
		t.Errorf("2.0 per 100 is at the limit, not above it: %v", report.Findings)
	}
	// A 30-word sentence passes; a clause break splits a longer one.
	for name, text := range map[string]string{
		"thirty words":   words(30) + ".",
		"semicolon":      words(20) + "; " + words(20),
		"arrow":          words(20) + " -> " + words(20),
		"colon":          words(20) + ": " + words(20),
		"list items":     "- " + words(20) + "\n- " + words(20),
		"crlf paragraph": strings.ReplaceAll(words(15)+".\n"+words(15)+".", "\n", "\r\n"),
	} {
		if report := Check(text, Options{}); !report.Passed() {
			t.Errorf("%s: want pass, got %v", name, report.Findings)
		}
	}
	// A wrapped sentence is one sentence: two 16-word lines without a break are 32 words.
	if report := Check(words(16)+"\n"+words(16), Options{}); len(report.Findings) != 1 || report.Findings[0].Rule != RuleLongSentence {
		t.Errorf("wrapped sentence: %v", report.Findings)
	}
}

func TestCheckOptions(t *testing.T) {
	text := "the " + words(9)
	if report := Check(text, Options{MinProseWords: 10, MaxArticleDensity: 5}); report.Passed() {
		t.Error("10% density must fail a 5-per-100 limit once 10 words are judged")
	}
	if report := Check(text, Options{MinProseWords: -1, MaxArticleDensity: -1, MaxSentenceWords: -1}); !report.Passed() {
		t.Errorf("negative options take the defaults; 10 words are below the default minimum: %v", report.Findings)
	}
	if report := Check(words(12), Options{MaxSentenceWords: 10}); report.Passed() {
		t.Error("a 12-word sentence must fail a 10-word limit")
	}
	// A limit below the excerpt length still quotes the sentence without running past it.
	report := Check("gate run", Options{MaxSentenceWords: 1})
	if len(report.Findings) != 1 || report.Findings[0].Excerpt != "2 words: gate run ..." {
		t.Errorf("short-limit excerpt: %v", report.Findings)
	}
}

// TestCheckWordCeiling covers C7 positive (over the ceiling fires), negative (a zero
// ceiling never fires, an unset one behaves the same as a caller that never set it) and
// boundary (exactly at the ceiling passes, one word over fails).
func TestCheckWordCeiling(t *testing.T) {
	report := Check(words(10), Options{MaxProseWords: 5})
	if report.Passed() {
		t.Fatal("10 words over a 5-word ceiling must fail")
	}
	if rules := rulesOf(report); len(rules) != 1 || rules[0] != RuleWordCeiling {
		t.Fatalf("rules = %v, want [%s]", rules, RuleWordCeiling)
	}
	for name, opts := range map[string]Options{
		"zero ceiling":     {MaxProseWords: 0},
		"negative ceiling": {MaxProseWords: -1},
		"unset ceiling":    {},
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(sentences(5000), opts); !report.Passed() {
				t.Errorf("no ceiling set must never fire C7: %v", report.Findings)
			}
		})
	}
	if report := Check(words(5), Options{MaxProseWords: 5}); !report.Passed() {
		t.Errorf("exactly at the ceiling must pass: %v", report.Findings)
	}
	if report := Check(words(6), Options{MaxProseWords: 5}); report.Passed() {
		t.Error("one word over the ceiling must fail")
	}
}

// TestCheckTokenCeiling covers C8 positive (over the ceiling fires), negative (a zero,
// negative or unset ceiling never fires) and boundary (exactly at the ceiling passes, one
// token over fails). EstimateTokens is words * 1.3, so token counts are derived from it
// rather than hand-picked, keeping the test honest against the one estimator in the
// package.
func TestCheckTokenCeiling(t *testing.T) {
	text := sentences(100)
	tokens := EstimateTokens(text)
	report := Check(text, Options{MaxTokens: tokens - 1})
	if report.Passed() {
		t.Fatalf("%d estimated tokens over a %d ceiling must fail", tokens, tokens-1)
	}
	if rules := rulesOf(report); len(rules) != 1 || rules[0] != RuleTokenCeiling {
		t.Fatalf("rules = %v, want [%s]", rules, RuleTokenCeiling)
	}
	for name, opts := range map[string]Options{
		"zero ceiling":     {MaxTokens: 0},
		"negative ceiling": {MaxTokens: -1},
		"unset ceiling":    {},
	} {
		t.Run(name, func(t *testing.T) {
			if report := Check(text, opts); !report.Passed() {
				t.Errorf("no ceiling set must never fire C8: %v", report.Findings)
			}
		})
	}
	if report := Check(text, Options{MaxTokens: tokens}); !report.Passed() {
		t.Errorf("exactly at the ceiling must pass: %v", report.Findings)
	}
	// EstimatedTokens is always reported, ceiling or not.
	if got := Check(text, Options{}).EstimatedTokens; got != tokens {
		t.Errorf("EstimatedTokens = %d, want %d", got, tokens)
	}
}

func TestCheckIsDeterministic(t *testing.T) {
	text := "Please note that it just works 🎉.\n\x1b[1mbold\x1b[m\nprobably fine, really."
	first := Check(text, Options{})
	for i := 0; i < 20; i++ {
		if again := Check(text, Options{}); !reflect.DeepEqual(first, again) {
			t.Fatalf("run %d differs:\n%v\n%v", i, first.Findings, again.Findings)
		}
	}
	lines := make([]int, 0, len(first.Findings))
	for _, f := range first.Findings {
		lines = append(lines, f.Line)
	}
	for i := 1; i < len(lines); i++ {
		if lines[i] < lines[i-1] {
			t.Fatalf("findings not sorted by line: %v", first.Findings)
		}
	}
	if got := first.Findings[0].String(); !strings.HasPrefix(got, "1: C2 filler: ") {
		t.Errorf("Finding.String() = %q", got)
	}
}
