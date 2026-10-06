package main

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/agentcontext"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	cavemanProse = "Search for an existing implementation before adding one. Grep the repository for the " +
		"capability and extend the code that is already there. Two implementations of one behavior are a " +
		"defect: they drift, and the second one stops matching the first.\n"
	cavemanTerse = "verdict: pass. changed: internal/caveman. ran: go test ./internal/caveman/. open: none.\n"
)

// runCavemanCLI runs the command against an in-memory stdin and returns what it printed.
func runCavemanCLI(t *testing.T, stdin string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := cavemanCommand(context.Background(), args, strings.NewReader(stdin), &out)
	return out.String(), err
}

func TestCavemanCheckPositive(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	out, err := runCavemanCLI(t, "", "check", terse)
	if err != nil || !strings.Contains(out, ": PASS prose_words=") {
		t.Fatalf("terse file: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, cavemanTerse, "check", "-"); err != nil || !strings.HasPrefix(out, "-: PASS") {
		t.Fatalf("stdin: err=%v\n%s", err, out)
	}
	// A directory expands to the Markdown files below it, and nothing else.
	writeFixtureFile(t, dir, "nested/deeper.md", cavemanTerse)
	writeFixtureFile(t, dir, "nested/notes.txt", cavemanProse)
	if out, err = runCavemanCLI(t, "", "check", dir); err != nil || strings.Count(out, ": PASS") != 2 || strings.Contains(out, "notes.txt") {
		t.Fatalf("directory: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckNegative(t *testing.T) {
	dir := t.TempDir()
	prose := writeFixtureFile(t, dir, "prose.md", cavemanProse)
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	out, err := runCavemanCLI(t, "", "check", terse, prose)
	if err == nil || !strings.Contains(err.Error(), "1 of 2 input(s) failed") {
		t.Fatalf("prose must fail the check: err=%v", err)
	}
	if !strings.Contains(out, "prose.md: FAIL") || !strings.Contains(out, "prose.md:0 C1 article-density:") {
		t.Fatalf("failure output:\n%s", out)
	}
	for name, args := range map[string][]string{
		"no subcommand":      nil,
		"unknown subcommand": {"lint", terse},
		"no inputs":          {"check"},
		"missing file":       {"check", filepath.Join(dir, "absent.md")},
		"unknown flag":       {"check", "--strict", terse},
		"unknown surface":    {"check", "--surface=slack", "--root=" + dir, terse},
	} {
		if _, err := runCavemanCLI(t, "", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCavemanCheckMessageKinds(t *testing.T) {
	fullProse := "I think we have reviewed every file and it looks like the gate is ready. We are probably finished, and it seems we will only need to update the report. You can see that it is clear, but I might have missed something. Please note that we did the checks as requested, and thanks for waiting.\n"
	out, err := runCavemanCLI(t, fullProse, "check", "-")
	if err == nil || !strings.Contains(out, "C9 grammar") || !strings.Contains(out, "contract=message") {
		t.Fatalf("default message contract must reject demonstrated prose: err=%v\n%s", err, out)
	}
	for _, want := range []string{"mechanical_rules=1", "advisory_rules=2,3,4,5,6,7,8"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary missing %q:\n%s", want, out)
		}
	}

	returnText := "verdict: pass\nchanged: none\nran: go test ./...\nevidence: none\nopen: none\n"
	if out, err = runCavemanCLI(t, returnText, "check", "--kind=return", "-"); err != nil || !strings.Contains(out, "contract=return") {
		t.Fatalf("return kind: err=%v\n%s", err, out)
	}
	if _, err = runCavemanCLI(t, "changed: none\n", "check", "--kind=return", "-"); err == nil {
		t.Fatal("malformed return must fail")
	}

	grammarOnly := "We are ready; it is complete.\n"
	if _, err = runCavemanCLI(t, grammarOnly, "check", "-"); err == nil {
		t.Fatal("message grammar must reject pronouns and copulas")
	}
	if out, err = runCavemanCLI(t, grammarOnly, "check", "--kind=context", "-"); err != nil || !strings.Contains(out, "contract=context") {
		t.Fatalf("context profile must report its advisory boundary: err=%v\n%s", err, out)
	}
	if _, err = runCavemanCLI(t, returnText, "check", "--kind=unknown", "-"); err == nil {
		t.Fatal("unknown kind must fail flag validation")
	}
}

func TestCavemanCheckRejectsDefaultIgnorableCombiningText(t *testing.T) {
	hidden := "verdict: pass\nchanged: none\nran: w\u034fe a\u034fre ready\nevidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, hidden, "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, "U+034F") {
		t.Fatalf("default-ignorable grammar must fail: err=%v\n%s", err, out)
	}
	visible := "verdict: pass\nchanged: none\nran: cafe\u0301 check\nevidence: none\nopen: none\n"
	if out, err = runCavemanCLI(t, visible, "check", "--kind=return", "-"); err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("visible combining text must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckRejectsASCIIPunctuationGrammar(t *testing.T) {
	hidden := "verdict: pass\nchanged: none\nran: w.e w:e w_e w-e w+e w=e w#e w|e w~e w,e\n" +
		"evidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, hidden, "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, `C9 grammar: pronoun "we"`) {
		t.Fatalf("ASCII punctuation grammar must fail: err=%v\n%s", err, out)
	}
	literal := "verdict: pass\nchanged: none\nran: HTTPS://example.test/we/is\nevidence: none\nopen: none\n"
	if out, err = runCavemanCLI(t, literal, "check", "--kind=return", "-"); err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("mixed-case URL literal must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckRejectsUnicodePunctuationGrammar(t *testing.T) {
	hidden := "verdict: pass\nchanged: none\nran: w\uFF0Ee w\u00B7e w\u2014e w\uFF0Fe w\u2215e w\u2024e\n" +
		"evidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, hidden, "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, `C9 grammar: pronoun "we"`) {
		t.Fatalf("Unicode punctuation grammar must fail: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckRejectsUnsafeControls(t *testing.T) {
	hidden := "verdict: pass\nchanged: none\nran: w\x00e w\x08e w\x7fe\nevidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, hidden, "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, "C4 terminal-noise: U+0000 control") {
		t.Fatalf("unsafe controls must fail: err=%v\n%s", err, out)
	}
	safeWhitespace := "verdict:\tpass\r\nchanged: none\r\nran: w\te check\r\nevidence: none\r\nopen: none\r\n"
	if out, err = runCavemanCLI(t, safeWhitespace, "check", "--kind=return", "-"); err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("tab and CRLF boundaries must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckPreservesCompleteURLAndPathLiterals(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: C:/work/we/is/value.go internal/we/is/value.go:42 /work/we/is/value.go:42:7 " +
		"C:\\work\\we\\is\\value.go:42 C:/work/we/is/value.go:42\n" +
		"ran: https://example.test/log_(we)/is (HTTPS://example.test/log_(we)/is)\n" +
		"evidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
	if err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("complete URL and path literals must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckRejectsAdversarialGrammarAndPhrases(t *testing.T) {
	cases := map[string]struct {
		value string
		want  string
	}{
		"visible combining grammar": {"w\u0301e ready", `C9 grammar: pronoun "we"`},
		"segmented flags":           {"--we. --w.e --w-e --w_e", `C9 grammar: pronoun "we"`},
		"segmented hedge":           {"prob.ably", "C3 hedge: probably"},
		"segmented fillers":         {"note.that in.order.to as.requested", "C2 filler:"},
		"wrapped filler":            {"note\nthat", "C2 filler: note that"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + tc.value + "\nevidence: none\nopen: none\n"
			out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("adversarial CLI input must fail with %q: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

func TestCavemanCheckPreservesAdversarialBoundaries(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: internal/(we)/is/value.go\n" +
		"ran: notify we@example.com. cafe\u0301 --max-words=3 --write-output --run=TestValue\n" +
		"evidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
	if err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("literal and technical boundaries must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckRejectsCompatibilityAndUnicodeSeparators(t *testing.T) {
	cases := map[string]struct {
		value string
		want  string
	}{
		"line separator":      {"value\u2028check", "C4 terminal-noise: U+2028 line separator"},
		"paragraph separator": {"value\u2029check", "C4 terminal-noise: U+2029 line separator"},
		"modifier apostrophe": {"we\u02bcre ready", `C9 grammar: pronoun "we're"`},
		"modifier hedge":      {"prob\u02bcably", "C3 hedge: probably"},
		"fullwidth grammar":   {"\uff57\uff45 ready", `C9 grammar: pronoun "we"`},
		"circled grammar":     {"ⓦⓔ ready", `C9 grammar: pronoun "we"`},
		"bold grammar":        {"𝐰𝐞 ready", `C9 grammar: pronoun "we"`},
		"double grammar":      {"𝕨𝕖 ready", `C9 grammar: pronoun "we"`},
		"modifier grammar":    {"ʷᵉ ready", `C9 grammar: pronoun "we"`},
		"pseudo path":         {"we／are／value．go", `C9 grammar: pronoun "we"`},
		"pseudo mail":         {"we＠example．com", `C9 grammar: pronoun "we"`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + tc.value + "\nevidence: none\nopen: none\n"
			out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("CLI escape must fail with %q: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

func TestCavemanCheckRejectsResidualUnicodeConfusables(t *testing.T) {
	cases := map[string]string{
		"turned comma":          "(we\u02BBre),",
		"prime":                 "(we\u02B9re),",
		"acute accent":          "(we\u02CAre),",
		"grave accent":          "(we\u02CBre),",
		"small letter saltillo": "(we\uA78Cre),",
		"legacy script":         "(𝓌ℯ),",
		"parenthesized Latin":   "(⒲⒠),",
		"small capitals":        "(ᴡᴇ),",
		"modifier subscript":    "(ʷₑ),",
	}
	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			text := "verdict: pass\nchanged: none\nran: " + value + " ready\nevidence: none\nopen: none\n"
			out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
			if err == nil || !strings.Contains(out, "-:3 C9 grammar: pronoun") {
				t.Fatalf("CLI confusable must fail at exact source line: err=%v\n%s", err, out)
			}
		})
	}
}

func TestCavemanCheckPreservesMSVCDefineFlags(t *testing.T) {
	valid := "verdict: pass\nchanged: none\nran: clang-cl /DDEBUG /DDEBUG=1 /Dwe /Dwe=are (/Dwe=are), source.c\nevidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, valid, "check", "--kind=return", "-")
	if err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("valid /Dmacro[=value] flags must pass CLI: err=%v\n%s", err, out)
	}
	invalid := "verdict: pass\nchanged: none\nran: /D-we /D=we /D.we\nevidence: none\nopen: none\n"
	out, err = runCavemanCLI(t, invalid, "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, `-:3 C9 grammar: pronoun "we"`) {
		t.Fatalf("invalid /D lookalikes must remain lintable: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckPreservesWrappedPlatformLiteralsAndShortFlags(t *testing.T) {
	text := "verdict: pass\n" +
		"changed: (internal/(we)/is/value.go). internal/(we)/is/ internal\\(we)\\is\\ " +
		"internal/(we)/is/value.go:42). (C:\\café\\(we)\\is\\value.go:42). " +
		"C:\\café\\(we)\\is\\value.go:42). (\\\\sérver\\share\\(we)\\is\\value.go). " +
		"\\\\sérver\\share\\(we)\\is\\value.go:42).\n" +
		"ran: notify (we@example.com). -I -i -a /I clang -I/usr/include source.c " +
		"clang -I=/usr/include source.c rock\u02bcn\n" +
		"evidence: none\nopen: none\n"
	out, err := runCavemanCLI(t, text, "check", "--kind=return", "-")
	if err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("platform literals and short flags must pass: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckLiteralWrapperBoundary(t *testing.T) {
	base := "internal/(we)/is/value.go:42"
	makeReturn := func(value string) string {
		return "verdict: pass\nchanged: " + value + "\nran: none\nevidence: none\nopen: none\n"
	}
	at := base + strings.Repeat(")", 4)
	out, err := runCavemanCLI(t, makeReturn(at), "check", "--kind=return", "-")
	if err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("four wrapper layers must pass: err=%v\n%s", err, out)
	}
	over := base + strings.Repeat(")", 5)
	out, err = runCavemanCLI(t, makeReturn(over), "check", "--kind=return", "-")
	if err == nil || !strings.Contains(out, `C9 grammar: pronoun "we"`) {
		t.Fatalf("fifth wrapper layer must remain visible: err=%v\n%s", err, out)
	}
}

func TestCavemanCheckPhraseFindingLineIsExactAndUnique(t *testing.T) {
	cases := map[string]struct {
		text string
		line string
	}{
		"same line": {"verdict: pass\nchanged: none\nran: alpha\nnote that\nevidence: none\nopen: none", "-:4 C2 filler: note that"},
		"wrapped":   {"verdict: pass\nchanged: none\nran: note\nthat\nevidence: none\nopen: none", "-:3 C2 filler: note that"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, tc.text, "check", "--kind=return", "-")
			if err == nil || strings.Count(out, "C2 filler: note that") != 1 || !strings.Contains(out, tc.line) {
				t.Fatalf("CLI phrase attribution mismatch: err=%v\n%s", err, out)
			}
		})
	}
}

func TestCavemanCheckBoundary(t *testing.T) {
	dir := t.TempDir()
	prose := writeFixtureFile(t, dir, "prose.md", cavemanProse)
	empty := writeFixtureFile(t, dir, "empty.md", "")
	if out, err := runCavemanCLI(t, "", "check", empty); err != nil || !strings.Contains(out, "PASS prose_words=0") {
		t.Fatalf("empty file: err=%v\n%s", err, out)
	}
	// Without a manifest the mcp surface is internal by default: the lint applies.
	if _, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose); err == nil {
		t.Fatal("mcp defaults to internal; prose must fail")
	}
	// surfaces.agent does not reach an emission surface, so agent = docs keeps the lint on.
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    agent: docs\n")
	if _, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose); err == nil {
		t.Fatal("agent = docs must not switch the mcp lint off; prose must fail")
	}
	// A manifest that opts the surface out returns no-verdict failure, never a green skip.
	writeFixtureFile(t, dir, ".standards.yaml", "version: 1\nregister:\n  surfaces:\n    mcp: docs\n")
	out, err := runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, prose)
	if err == nil || !strings.Contains(err.Error(), "surfaces.mcp = docs has no Caveman verdict") || out != "" {
		t.Fatalf("opted-out surface must not look green: err=%v\n%s", err, out)
	}
	// Printed findings are bounded; the rest are counted.
	many := strings.Repeat("please.\n", maxPrintedFindings+5)
	if out, err = runCavemanCLI(t, many, "check", "-"); err == nil || !strings.Contains(out, "(+5 more findings)") {
		t.Fatalf("finding bound: err=%v\n%s", err, out)
	}
	if _, err = runCavemanCLI(t, strings.Repeat("a", 1<<20+1), "check", "-"); err == nil {
		t.Fatal("stdin above 1 MiB must be refused")
	}
	// The rendered register block is masked exactly as the context gate masks it.
	block := config.RegisterBlockStart + "\n" + strings.TrimSuffix(cavemanProse, "\n") + "\n" + config.RegisterBlockEnd + "\n"
	if out, err = runCavemanCLI(t, cavemanTerse+block, "check", "-"); err != nil || !strings.Contains(out, "PASS") || !strings.Contains(out, "register_block_lines=3") {
		t.Fatalf("register block: err=%v\n%s", err, out)
	}
	// YAML front matter is left out of the prose rules, and the summary counts its lines.
	front := "---\nname: example\ndescription: Probably the skill a user asks for.\n---\n"
	if out, err = runCavemanCLI(t, front+cavemanTerse, "check", "--kind=context", "-"); err != nil || !strings.Contains(out, "front_matter_lines=4") {
		t.Fatalf("front matter: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, cavemanTerse, "check", "--kind=context", "-"); err != nil || !strings.Contains(out, "front_matter_lines=0") {
		t.Fatalf("no front matter: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckSurfaceWithoutLint pins #367: a surface the lint does not apply to is never a
// pass. Positive: on the docs, forge and operator surfaces an existing file and standard input
// both end in an error that names the deciding row and says the input was NOT checked, with no
// report line. Negative: a missing path is the read error on every known surface, whether or
// not the lint applies there, so the input is read before the surface is judged. Boundary:
// input above the reader's bound is refused before the surface is judged too, and a surface
// with a lint still prints its verdict.
func TestCavemanCheckSurfaceWithoutLint(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	for surface, row := range map[string]string{
		"docs":     "surfaces.docs = docs",
		"forge":    "surfaces.forge = social",
		"operator": "surfaces.operator = docs",
	} {
		for _, input := range []string{terse, "-"} {
			out, err := runCavemanCLI(t, cavemanTerse, "check", "--surface="+surface, "--root="+dir, input)
			if err == nil || out != "" || !strings.Contains(err.Error(), row+" has no Caveman verdict; input NOT checked") {
				t.Errorf("--surface=%s %s: want the row and NOT checked, no report: err=%v\n%s", surface, input, err, out)
			}
		}
	}
	absent := filepath.Join(dir, "absent.md")
	for _, surface := range []string{"forge", "docs", "agent", "operator", "context", "mcp", "hooks", "prompts", "ledger"} {
		out, err := runCavemanCLI(t, "", "check", "--surface="+surface, "--root="+dir, absent)
		if err == nil || out != "" || !strings.Contains(err.Error(), "absent.md") || strings.Contains(err.Error(), "Caveman verdict") {
			t.Errorf("--surface=%s: a missing path must be the read error: err=%v\n%s", surface, err, out)
		}
	}
	out, err := runCavemanCLI(t, strings.Repeat("a", 1<<20+1), "check", "--surface=docs", "--root="+dir, "-")
	if err == nil || out != "" || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized input on a surface without a lint must be the read error: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, "", "check", "--surface=mcp", "--root="+dir, terse); err != nil || !strings.Contains(out, ": PASS") || strings.Contains(out, "NOT checked") {
		t.Fatalf("a surface with a lint must print its verdict: err=%v\n%s", err, out)
	}
}

// TestCavemanFloorNumbers runs F9 through the command: a dropped number fails with its line in
// <before>, and a kept one passes.
func TestCavemanFloorNumbers(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", "p99 15 s over 1819 requests.\n")
	kept := writeFixtureFile(t, dir, "kept.md", "1819 requests, p99 15s.\n")
	lossy := writeFixtureFile(t, dir, "lossy.md", "p99 15 s over many requests.\n")
	if out, err := runCavemanCLI(t, "", "floor", before, kept); err != nil {
		t.Fatalf("kept numbers must pass: err=%v\n%s", err, out)
	}
	out, err := runCavemanCLI(t, "", "floor", before, lossy)
	if err == nil || !strings.Contains(out, "before.md:1 F9 number-lost: 1819") {
		t.Fatalf("a dropped number must fail: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckCeilingFlags covers --max-words/--max-tokens: positive (terse text still
// passing prose rules fails once it crosses either ceiling), negative (the default, no
// flags, never fires C7/C8) and boundary (0 means no ceiling; exactly at a ceiling passes).
func TestCavemanCheckCeilingFlags(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)

	out, err := runCavemanCLI(t, "", "check", "--max-words=1", terse)
	if err == nil || !strings.Contains(out, "C7 word-ceiling") {
		t.Fatalf("--max-words=1 must fail terse text on the word ceiling alone: err=%v\n%s", err, out)
	}
	out, err = runCavemanCLI(t, "", "check", "--max-tokens=1", terse)
	if err == nil || !strings.Contains(out, "C8 token-ceiling") {
		t.Fatalf("--max-tokens=1 must fail terse text on the token ceiling alone: err=%v\n%s", err, out)
	}

	if out, err = runCavemanCLI(t, "", "check", terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("no flags set (the default) must never fire a ceiling rule: err=%v\n%s", err, out)
	}
	if out, err = runCavemanCLI(t, "", "check", "--max-words=0", "--max-tokens=0", terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("--max-words=0 --max-tokens=0 must behave like unset: err=%v\n%s", err, out)
	}

	words := strings.Fields(cavemanTerse)
	if out, err = runCavemanCLI(t, "", "check", fmt.Sprintf("--max-words=%d", len(words)), terse); err != nil || strings.Contains(out, "ceiling") {
		t.Fatalf("exactly at the word ceiling must pass: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckRefusesNegativeCeilings pins #382: a negative ceiling is a usage error that
// names its flag, never a ceiling silently switched off. Negative: -1 on either flag, on the
// configured-sources form too, and the refusal comes before any input is read. Boundary: the
// most negative int is refused like -1, and 0 beside it stays the opt-out. Positive: a
// ceiling of 1 still judges the text.
func TestCavemanCheckRefusesNegativeCeilings(t *testing.T) {
	dir := t.TempDir()
	terse := writeFixtureFile(t, dir, "terse.md", cavemanTerse)
	absent := filepath.Join(dir, "absent.md")
	for name, tc := range map[string]struct {
		args []string
		want string
	}{
		"words":                 {[]string{"check", "--max-words=-1", terse}, "--max-words=-1 is negative"},
		"tokens":                {[]string{"check", "--max-tokens=-1", terse}, "--max-tokens=-1 is negative"},
		"tokens after the path": {[]string{"check", terse, "--max-tokens", "-7"}, "--max-tokens=-7 is negative"},
		"words beside zero":     {[]string{"check", "--max-tokens=0", "--max-words=-1", terse}, "--max-words=-1 is negative"},
		"most negative":         {[]string{"check", fmt.Sprintf("--max-words=%d", math.MinInt), terse}, "--max-words=-"},
		"before the read":       {[]string{"check", "--max-words=-1", absent}, "--max-words=-1 is negative"},
		"configured sources":    {[]string{"check", "--root=" + dir, "--configured-sources", "--max-tokens=-1"}, "--max-tokens=-1 is negative"},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", tc.args...)
			if err == nil || !strings.Contains(err.Error(), tc.want) || out != "" {
				t.Fatalf("want a refusal naming the flag (%q) and no report: err=%v\n%s", tc.want, err, out)
			}
		})
	}
	if out, err := runCavemanCLI(t, "", "check", "--max-words=0", "--max-tokens=0", terse); err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("0 must stay the documented opt-out: err=%v\n%s", err, out)
	}
	if out, err := runCavemanCLI(t, "", "check", "--max-tokens=1", terse); err == nil || !strings.Contains(out, "C8 token-ceiling") {
		t.Fatalf("the smallest positive ceiling must still judge the text: err=%v\n%s", err, out)
	}
}

func TestCavemanEstimate(t *testing.T) {
	dir := t.TempDir()
	path := writeFixtureFile(t, dir, "ten.md", "one two three four five six seven eight nine ten\n")
	out, err := runCavemanCLI(t, "", "estimate", path, "-")
	if err != nil {
		t.Fatalf("estimate: %v", err)
	}
	if !strings.Contains(out, "ten.md: bytes=49 lines=1 tokens_est=13") || !strings.Contains(out, "-: bytes=0 lines=0 tokens_est=0") {
		t.Fatalf("per-input lines:\n%s", out)
	}
	if !strings.Contains(out, "total: inputs=2 bytes=49 tokens_est=13") {
		t.Fatalf("total line:\n%s", out)
	}
	if _, err := runCavemanCLI(t, "", "estimate"); err == nil {
		t.Fatal("estimate without inputs must print usage")
	}
	if _, err := runCavemanCLI(t, "", "estimate", filepath.Join(dir, "absent")); err == nil {
		t.Fatal("a missing input must fail")
	}
}

const (
	floorBefore = "1. **Never** push to `main`; run `make verify-all` first (HISS-16).\n" +
		"You MUST read [the guide](docs/guides/text-register.md).\n"
	floorAfter = "1. **Never** push `main`. First: `make verify-all` (HISS-16).\n" +
		"MUST read [guide](docs/guides/text-register.md).\n"
)

func TestCavemanFloorPositive(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	after := writeFixtureFile(t, dir, "after.md", floorAfter)
	out, err := runCavemanCLI(t, "", "floor", before, after)
	if err != nil || !strings.Contains(out, "after.md: PASS findings=0") {
		t.Fatalf("a rewrite that keeps every fact must pass: err=%v\n%s", err, out)
	}
	// The rewrite may arrive on standard input.
	if out, err = runCavemanCLI(t, floorAfter, "floor", before, "-"); err != nil || !strings.Contains(out, "-> -: PASS") {
		t.Fatalf("stdin rewrite: err=%v\n%s", err, out)
	}
}

// TestCavemanFloorANSIEscapes pins #713 through the command: a rewrite that removes colour
// codes holds the floor, and a number, id or directive lost beside an escape fails its rule,
// on a line below an OSC left unterminated on its own line too.
func TestCavemanFloorANSIEscapes(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", "a \033[31mMUST\033[0m b\n")
	after := writeFixtureFile(t, dir, "after.md", "a MUST b\n")
	out, err := runCavemanCLI(t, "", "floor", before, after)
	if err != nil || !strings.Contains(out, "after.md: PASS findings=0") {
		t.Fatalf("issue #713 reproduction files must pass: err=%v\n%s", err, out)
	}
	cases := map[string]struct{ before, after, want string }{
		"number": {"a \033[31mred 7\033[0m b\n", "a red b\n", "number_before.md:1 F9 number-lost: 7"},
		"id":     {"rule \033[1mHISS-17\033[0m applies\n", "rule applies\n", "id_before.md:1 F3 id-lost: HISS-17"},
		"must":   {"x \033[31mMUST\033[0m y\n", "x y\n", "must_before.md:0 F6 must-dropped: 1 -> 0"},
		"osc":    {"log \033]0;title\nrule HISS-17\ndone\a end\n", "log 0;title\ndone end\n", "osc_before.md:2 F3 id-lost: HISS-17"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lossyBefore := writeFixtureFile(t, dir, name+"_before.md", tc.before)
			lossyAfter := writeFixtureFile(t, dir, name+"_after.md", tc.after)
			out, err := runCavemanCLI(t, "", "floor", lossyBefore, lossyAfter)
			if err == nil || !strings.Contains(out, tc.want) {
				t.Fatalf("fact lost beside an escape must fail %q: err=%v\n%s", tc.want, err, out)
			}
		})
	}
}

func TestCavemanFloorNegative(t *testing.T) {
	dir := t.TempDir()
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	lossy := writeFixtureFile(t, dir, "lossy.md", "1. **Never** push main.\nRead the guide.\n")
	out, err := runCavemanCLI(t, "", "floor", before, lossy)
	if err == nil || !strings.Contains(err.Error(), "lossy.md lost") {
		t.Fatalf("a lossy rewrite must fail: err=%v\n%s", err, out)
	}
	for _, want := range []string{"F1 code-span-lost", "F3 id-lost: HISS-16", "F4 link-lost", "F6 must-dropped: 1 -> 0"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing finding %q in:\n%s", want, out)
		}
	}
	for name, args := range map[string][]string{
		"one input":    {"floor", before},
		"three inputs": {"floor", before, before, before},
		"both stdin":   {"floor", "-", "-"},
		"missing file": {"floor", before, filepath.Join(dir, "absent.md")},
	} {
		if _, err := runCavemanCLI(t, "", args...); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestCavemanFloorBoundary(t *testing.T) {
	dir := t.TempDir()
	empty := writeFixtureFile(t, dir, "empty.md", "")
	before := writeFixtureFile(t, dir, "before.md", floorBefore)
	// Nothing to lose: an empty original passes against anything, including empty.
	if out, err := runCavemanCLI(t, "", "floor", empty, empty); err != nil || !strings.Contains(out, "PASS findings=0") {
		t.Fatalf("empty -> empty: err=%v\n%s", err, out)
	}
	// Moving a fact is fine; the floor checks presence, not position.
	moved := writeFixtureFile(t, dir, "moved.md", "MUST read [guide](docs/guides/text-register.md).\n"+
		"1. **Never** push `main`. First: `make verify-all` (HISS-16).\n")
	if out, err := runCavemanCLI(t, "", "floor", before, moved); err != nil {
		t.Fatalf("reordered facts must pass: err=%v\n%s", err, out)
	}
	// Findings are bounded like check's; the rest are counted.
	var many strings.Builder
	for i := 0; i < maxPrintedFindings+5; i++ {
		fmt.Fprintf(&many, "see ID-%d\n", i+1)
	}
	ids := writeFixtureFile(t, dir, "ids.md", many.String())
	if out, err := runCavemanCLI(t, "", "floor", ids, empty); err == nil || !strings.Contains(out, "(+5 more findings)") {
		t.Fatalf("finding bound: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckJudgesHarnessTailLikeTheGate: the command reproduces the context gate on an
// adopted AGENTS.md. Positive: a terse tail passes. Negative: one prose paragraph below the
// harness fails although the long harness keeps whole-file density under the limit. Boundary:
// without the end marker the same text passes, as the gate would judge it.
func TestCavemanCheckJudgesHarnessTailLikeTheGate(t *testing.T) {
	dir := t.TempDir()
	harness := "# Fixture Agent Operating Harness\n\n" +
		strings.Repeat("1. **Verify.** Run `make verify-all` before turn end; read source first, then edit; report exit status.\n", 30) +
		compiler.HarnessEndMarker + "\n\n---\n\n"
	terse := writeFixtureFile(t, dir, "terse/AGENTS.md", harness+cavemanTerse)
	if out, err := runCavemanCLI(t, "", "check", "--kind=context", terse); err != nil {
		t.Fatalf("terse tail: err=%v\n%s", err, out)
	}
	prose := writeFixtureFile(t, dir, "prose/AGENTS.md", harness+cavemanProse)
	out, err := runCavemanCLI(t, "", "check", "--kind=context", prose)
	if err == nil || !strings.Contains(out, "repository text below the harness:") {
		t.Fatalf("prose below the harness passed: err=%v\n%s", err, out)
	}
	unmarked := writeFixtureFile(t, dir, "unmarked/AGENTS.md", strings.Replace(harness, compiler.HarnessEndMarker, "", 1)+cavemanProse)
	if out, err := runCavemanCLI(t, "", "check", "--kind=context", unmarked); err != nil {
		t.Fatalf("unmarked file judged by section: err=%v\n%s", err, out)
	}
}

// TestCavemanCheckFreeStandingQuote pins #695: a file containing a free-standing quote
// reports normally (no panic) under --kind=context and --kind=message, including boundary
// two-rune wrapper pairs.
func TestCavemanCheckFreeStandingQuote(t *testing.T) {
	dir := t.TempDir()
	quoteFile := writeFixtureFile(t, dir, "quote.md", "# Title\n\nQuote ' alone.\n")
	for _, kind := range []string{"context", "message"} {
		t.Run("quote_"+kind, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", "check", "--kind="+kind, quoteFile)
			if err != nil || !strings.Contains(out, ": PASS") {
				t.Fatalf("free-standing quote under --kind=%s must pass: err=%v\n%s", kind, err, out)
			}
		})
	}

	failFile := writeFixtureFile(t, dir, "fail.md", "# Title\n\nQuote ' we are alone.\n")
	out, err := runCavemanCLI(t, "", "check", "--kind=message", failFile)
	if err == nil || !strings.Contains(out, ": FAIL") || !strings.Contains(out, `C9 grammar: pronoun "we"`) {
		t.Fatalf("failing file with quote under --kind=message must report findings normally: err=%v\n%s", err, out)
	}

	emptyPairFile := writeFixtureFile(t, dir, "emptypair.md", "# Title\n\nQuote '' alone.\n")
	for _, kind := range []string{"context", "message"} {
		t.Run("empty_pair_"+kind, func(t *testing.T) {
			out, err := runCavemanCLI(t, "", "check", "--kind="+kind, emptyPairFile)
			if err != nil || !strings.Contains(out, ": PASS") {
				t.Fatalf("empty pair under --kind=%s must pass: err=%v\n%s", kind, err, out)
			}
		})
	}
}

// cavemanContextOnly passes the context profile and fails runtime message grammar (C9).
const cavemanContextOnly = "# Operating Harness\n\nIt is verified. All pass -> Ed25519 receipt.\n"

// wantCavemanVerdict fails the test unless the check of file ended with verdict ("PASS" or
// "FAIL") under contract; a FAIL must also carry a C9 grammar finding.
func wantCavemanVerdict(t *testing.T, file, verdict, contract string, args ...string) {
	t.Helper()
	out, err := runCavemanCLI(t, "", append(append([]string{"check"}, args...), file)...)
	ok := strings.Contains(out, filepath.ToSlash(file)+": "+verdict) && strings.Contains(out, "contract="+contract)
	if verdict == "PASS" {
		ok = ok && err == nil
	} else {
		ok = ok && err != nil && strings.Contains(out, "C9 grammar")
	}
	if !ok {
		t.Errorf("check %v %s: want %s under contract=%s, got err=%v\n%s", args, file, verdict, contract, err, out)
	}
}

// TestCavemanCheckContextKindInference_Positive pins #777: without --kind, AGENTS.md and every
// compiled vendor file at its place below --root take the context profile the gate lints them
// under, so text the gate accepts passes, the live AGENTS.md and CLAUDE.md included. Run from
// the repository root with the default --root, the command the harness prescribes passes too.
func TestCavemanCheckContextKindInference_Positive(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range agentcontext.ContextFiles() {
		file := writeFixtureFile(t, dir, rel, cavemanContextOnly)
		wantCavemanVerdict(t, file, "PASS", "context", "--root", dir)
	}
	repoRoot := filepath.Join("..", "..")
	for _, live := range []string{"AGENTS.md", "CLAUDE.md"} {
		wantCavemanVerdict(t, filepath.Join(repoRoot, live), "PASS", "context", "--root", repoRoot)
	}
	t.Chdir(dir)
	for _, rel := range []string{"AGENTS.md", ".windsurfrules"} {
		wantCavemanVerdict(t, rel, "PASS", "context")
	}
}

// TestCavemanCheckContextKindInference_Negative: without --kind, a file that only shares a
// context file's name (nested, a persona directory entry, another directory, another case)
// and every other note keep the message contract; nested AGENTS.md files get their own gate
// (#311). An explicit --kind=message on the live AGENTS.md reports message findings, and a
// Git commit message file is still not read as prose: it needs --surface as before.
func TestCavemanCheckContextKindInference_Negative(t *testing.T) {
	dir := t.TempDir()
	for _, rel := range []string{
		"nested/AGENTS.md",
		"nested/CLAUDE.md",
		".agents/agents/reviewer/AGENTS.md",
		"docs/claude.md",
		"agents.md",
		"NOT_AGENTS.md",
		"candidate-note.md",
	} {
		file := writeFixtureFile(t, dir, rel, cavemanContextOnly)
		wantCavemanVerdict(t, file, "FAIL", "message", "--root", dir)
	}
	repoRoot := filepath.Join("..", "..")
	wantCavemanVerdict(t, filepath.Join(repoRoot, "AGENTS.md"), "FAIL", "message", "--root", repoRoot, "--kind=message")

	commit := writeFixtureFile(t, dir, "COMMIT_EDITMSG", cavemanContextOnly)
	if out, err := runCavemanCLI(t, "", "check", "--root", dir, commit); err == nil || !strings.Contains(err.Error(), "requires --surface") {
		t.Errorf("COMMIT_EDITMSG without --surface: want the non-Markdown source error, got err=%v\n%s", err, out)
	}
}

// TestCavemanCheckContextKindInference_Boundary: an explicit --kind wins in both directions,
// and the inference resolves each path against --root. A non-clean path to the canonical file
// is that file; the same file outside --root is no context file of that repository, so it
// keeps message, and a compiled file without a Markdown extension there is not read as prose.
func TestCavemanCheckContextKindInference_Boundary(t *testing.T) {
	dir := t.TempDir()
	note := writeFixtureFile(t, dir, "candidate-note.md", cavemanContextOnly)
	wantCavemanVerdict(t, note, "PASS", "context", "--root", dir, "--kind=context")
	repoRoot := filepath.Join("..", "..")
	wantCavemanVerdict(t, filepath.Join(repoRoot, "CLAUDE.md"), "FAIL", "message", "--root", repoRoot, "-kind", "message")

	agents := writeFixtureFile(t, dir, "AGENTS.md", cavemanContextOnly)
	writeFixtureFile(t, dir, "nested/candidate-note.md", cavemanContextOnly)
	// filepath.Join would clean the path, so the separators are written out.
	sep := string(filepath.Separator)
	wantCavemanVerdict(t, dir+sep+"nested"+sep+".."+sep+"AGENTS.md", "PASS", "context", "--root", dir)
	outside := filepath.Join(dir, "sub")
	wantCavemanVerdict(t, agents, "FAIL", "message", "--root", outside)
	windsurf := writeFixtureFile(t, dir, ".windsurfrules", cavemanContextOnly)
	if out, err := runCavemanCLI(t, "", "check", "--root", outside, windsurf); err == nil || !strings.Contains(err.Error(), "requires --surface") {
		t.Errorf(".windsurfrules outside --root: want the non-Markdown source error, got err=%v\n%s", err, out)
	}
}
