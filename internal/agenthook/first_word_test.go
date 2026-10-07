package agenthook

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// anchoredRule is one built-in rule that starts at the first of its command words on a line.
type anchoredRule struct {
	name  string
	words []string
	tail  string
}

var anchoredRules = []anchoredRule{
	{"in-place edit", inPlaceEditorWords, inPlaceEditTail},
	{"find", findWords, findTail},
}

// anchored is the rule as builtinEvasion holds it.
func (r anchoredRule) anchored() string { return lineThroughFirstWord(r.words...) + r.tail }

// unanchored is the rule as it stood before #829, `\b(words)\b` at any word: the oracle.
func (r anchoredRule) unanchored() string {
	return `\b(` + strings.Join(r.words, "|") + `)\b` + r.tail
}

// lineStartOnly is a planted wrong anchor that loses every word not at a line start.
func (r anchoredRule) lineStartOnly() string {
	return `(?m)^(` + strings.Join(r.words, "|") + `)\b` + r.tail
}

// firstWordTokens build the replayed commands: the command words, alone and with the hooks
// directory or the in-place flag after them, their prefixes, word and non-word characters (a
// Unicode letter, which only Python's \w counts), line breaks, the hooks directory and the
// actions and flags the two rules look for.
var firstWordTokens = []string{
	"find", "find .git/hooks", "fin", "sed", "perl -i ", "x", "é", "_", " ", "-", "\n", "\t",
	".git/hooks", " -delete", " -i", " -ok", "d",
}

// firstWordTexts returns every sequence of up to four firstWordTokens, in a fixed order.
func firstWordTexts() []string {
	texts := []string{""}
	level := []string{""}
	for range 4 {
		next := make([]string, 0, len(level)*len(firstWordTokens))
		for _, prefix := range level {
			for _, token := range firstWordTokens {
				next = append(next, prefix+token)
			}
		}
		texts = append(texts, next...)
		level = next
	}
	return texts
}

// compileLikeTheEngine compiles a source the way NewPolicy compiles a built-in rule.
func compileLikeTheEngine(t *testing.T, source string) *regexp.Regexp {
	t.Helper()
	return builtinRule(BuiltinRule{Source: source, Invariant: "HISS"}, evasionMessage).pattern
}

// disagreements counts the texts on which two patterns disagree, and the matches of the first.
func disagreements(oracle, candidate *regexp.Regexp, texts []string) (int, int) {
	differ, matches := 0, 0
	for _, text := range texts {
		want := oracle.MatchString(text)
		if want {
			matches++
		}
		if want != candidate.MatchString(text) {
			differ++
		}
	}
	return differ, matches
}

// TestLineThroughFirstWord_Positive_EndsAtTheFirstWord: the anchor ends at the first whole
// command word of the first line that holds one, after other words, separators and lines.
func TestLineThroughFirstWord_Positive_EndsAtTheFirstWord(t *testing.T) {
	for _, tc := range []struct {
		words []string
		text  string
		end   int
	}{
		{findWords, "find .git", 4},
		{findWords, "ls; find x; find y", 8},
		{findWords, "xfind find", 10},
		{findWords, "a b\nc find", 10},
		{inPlaceEditorWords, "perl -pi; sed", 4},
		{inPlaceEditorWords, "sedx per perl sed", 13},
		{inPlaceEditorWords, "s se sed", 8},
	} {
		got := regexp.MustCompile(lineThroughFirstWord(tc.words...)).FindStringIndex(tc.text)
		if got == nil || got[1] != tc.end {
			t.Errorf("%q in %q: match %v, want end %d", tc.words, tc.text, got, tc.end)
		}
	}
}

// TestLineThroughFirstWord_Negative_NoWholeWord: a word inside a longer word, split by a
// character, or with a word character on either side is no command word.
func TestLineThroughFirstWord_Negative_NoWholeWord(t *testing.T) {
	for _, tc := range []struct {
		words []string
		text  string
	}{
		{findWords, "xfind"}, {findWords, "findx"}, {findWords, "fin d"}, {findWords, "find_x"},
		{findWords, "_find"}, {findWords, "pathfinder"}, {findWords, ""},
		{inPlaceEditorWords, "sedan"}, {inPlaceEditorWords, "perlite"}, {inPlaceEditorWords, "se d"},
	} {
		if loc := regexp.MustCompile(lineThroughFirstWord(tc.words...)).FindStringIndex(tc.text); loc != nil {
			t.Errorf("%q matched in %q at %v", tc.words, tc.text, loc)
		}
	}
}

// TestLineThroughFirstWord_Boundary_WordListsAndEdges pins the edges: a word at the very end of
// the text, one right after a line break, a line of separators only, a one-letter word, and
// the shipped word lists, whose letters and distinct first letters the run alternatives rely on.
func TestLineThroughFirstWord_Boundary_WordListsAndEdges(t *testing.T) {
	find := regexp.MustCompile(lineThroughFirstWord(findWords...))
	if !find.MatchString("find") || !find.MatchString("-;\nfind") || find.MatchString(" -;\t") {
		t.Error("find anchor: end of text, line start or separator-only line misread")
	}
	if got := regexp.MustCompile(lineThroughFirstWord("x")).FindStringIndex("xx x"); got == nil || got[1] != 4 {
		t.Errorf("one-letter word: %v", got)
	}
	letters := regexp.MustCompile(`^[a-z]+$`)
	for _, words := range [][]string{findWords, inPlaceEditorWords} {
		firsts := map[byte]bool{}
		for _, word := range words {
			if !letters.MatchString(word) || firsts[word[0]] {
				t.Errorf("word list %q: %q is not lowercase ASCII letters or repeats a first letter", words, word)
			}
			firsts[word[0]] = true
		}
	}
}

// TestAnchoredRulesKeepTheirLanguage replays every sequence of up to four firstWordTokens
// against both anchored rules and their unanchored form in RE2: they must agree on every one.
// The texts must discriminate: each rule matches some of them, and a planted anchor that only
// reads the word at a line start disagrees with the oracle on some.
func TestAnchoredRulesKeepTheirLanguage(t *testing.T) {
	texts := firstWordTexts()
	for _, rule := range anchoredRules {
		if !slices.Contains(builtinEvasion, rule.anchored()) {
			t.Fatalf("%s: builtinEvasion does not hold the anchored rule %q", rule.name, rule.anchored())
		}
		oracle := compileLikeTheEngine(t, rule.unanchored())
		differ, matches := disagreements(oracle, compileLikeTheEngine(t, rule.anchored()), texts)
		planted, _ := disagreements(oracle, compileLikeTheEngine(t, rule.lineStartOnly()), texts)
		t.Logf("%s: %d texts, %d match, anchored disagrees on %d, line-start anchor on %d", rule.name, len(texts), matches, differ, planted)
		if differ != 0 || matches < 1000 || matches == len(texts) {
			t.Errorf("%s: %d of %d texts disagree, %d match", rule.name, differ, len(texts), matches)
		}
		if planted < 100 {
			t.Errorf("%s: the texts do not tell a line-start anchor from the rule", rule.name)
		}
	}
}

// pythonDisagreements is the program that replays the texts in Python's re: it prints, per
// pattern pair, how many texts the two disagree on and how many the first matches, and the
// characters it read, so a decoding other than UTF-8 shows as a different count.
const pythonDisagreements = `import json, re, sys
data = json.loads(sys.stdin.buffer.read().decode("utf-8"))
result = []
for oracle, candidate in data["pairs"]:
    first, second = re.compile(oracle), re.compile(candidate)
    differ = matches = 0
    for text in data["texts"]:
        want = first.search(text) is not None
        matches += want
        differ += want != (second.search(text) is not None)
    result.append([differ, matches])
print(json.dumps({"counts": result, "characters": sum(len(text) for text in data["texts"])}))
`

// TestAnchoredRulesKeepTheirLanguageInPython replays the same texts in Python's re, whose \w
// and \b read Unicode: the anchored rules agree with their unanchored form there too, and the
// planted line-start anchor does not.
func TestAnchoredRulesKeepTheirLanguageInPython(t *testing.T) {
	interpreter := testsupport.PythonInterpreter(t)
	pairs := make([][2]string, 0, 2*len(anchoredRules))
	for _, rule := range anchoredRules {
		pairs = append(pairs, [2]string{rule.unanchored(), rule.anchored()}, [2]string{rule.unanchored(), rule.lineStartOnly()})
	}
	texts := firstWordTexts()
	characters := 0
	for _, text := range texts {
		characters += utf8.RuneCountInString(text)
	}
	input, err := json.Marshal(map[string]any{"pairs": pairs, "texts": texts})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if ctx, err = util.WithCommandStdin(ctx, input); err != nil {
		t.Fatal(err)
	}
	result, err := util.RunCommandBytes(ctx, "", interpreter, 1<<16, "-B", "-c", pythonDisagreements)
	if err != nil {
		t.Fatalf("python replay: %v: %s", err, result.Stderr)
	}
	var replay struct {
		Counts     [][2]int `json:"counts"`
		Characters int      `json:"characters"`
	}
	if err := json.Unmarshal(result.Stdout, &replay); err != nil || len(replay.Counts) != len(pairs) || replay.Characters != characters {
		t.Fatalf("python replay output %q (sent %d characters): %v", result.Stdout, characters, err)
	}
	for index, rule := range anchoredRules {
		anchored, planted := replay.Counts[2*index], replay.Counts[2*index+1]
		t.Logf("%s: %d match, anchored disagrees on %d, line-start anchor on %d", rule.name, anchored[1], anchored[0], planted[0])
		if anchored[0] != 0 || anchored[1] < 1000 || planted[0] < 100 {
			t.Errorf("%s: anchored disagrees on %d texts (%d match), line-start anchor on %d", rule.name, anchored[0], anchored[1], planted[0])
		}
	}
}
