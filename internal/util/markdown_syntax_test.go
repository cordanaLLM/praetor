package util_test

import (
	"slices"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// spanTexts returns each span of text with its delimiters, and its rendered content.
func spanTexts(text string, limit int) (raw, content []string) {
	for _, span := range util.MarkdownCodeSpans(text, limit) {
		raw = append(raw, text[span.Start:span.End])
		content = append(content, span.Content(text))
	}
	return raw, content
}

func TestMarkdownCodeSpansPositive(t *testing.T) {
	cases := map[string]struct {
		text        string
		raw, render []string
	}{
		"single":            {"Use `a b` here.", []string{"`a b`"}, []string{"a b"}},
		"double holds one":  {"Use ``c ` d`` here.", []string{"``c ` d``"}, []string{"c ` d"}},
		"padding stripped":  {"`` `x` ``", []string{"`` `x` ``"}, []string{"`x`"}},
		"wraps a line":      {"Run `alpha\nbeta` now.", []string{"`alpha\nbeta`"}, []string{"alpha beta"}},
		"two on one line":   {"`a` and `b`", []string{"`a`", "`b`"}, []string{"a", "b"}},
		"run order matters": {"``a`b`` `c`", []string{"``a`b``", "`c`"}, []string{"a`b", "c"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			raw, render := spanTexts(tc.text, len(tc.text))
			if !slices.Equal(raw, tc.raw) || !slices.Equal(render, tc.render) {
				t.Fatalf("spans %q / %q, want %q / %q", raw, render, tc.raw, tc.render)
			}
		})
	}
}

// TestMarkdownCodeSpansNegative pins the CommonMark rule the caveman floor relies on: an
// unclosed run is literal backticks, so it never pairs with the next run and never yields
// an empty or punctuation-only span between two real ones.
func TestMarkdownCodeSpansNegative(t *testing.T) {
	for _, text := range []string{"", "no code", "``", "```mermaid", "> ```mermaid", "a ` b", "`` a `"} {
		if raw, _ := spanTexts(text, 16); len(raw) != 0 {
			t.Errorf("%q yielded spans %q", text, raw)
		}
	}
	raw, _ := spanTexts("`alpha/module\nv1.0.0`, and `beta v2`.", 16)
	if !slices.Equal(raw, []string{"`alpha/module\nv1.0.0`", "`beta v2`"}) {
		t.Fatalf("a wrapped span paired its neighbours: %q", raw)
	}
}

func TestMarkdownCodeSpansBoundary(t *testing.T) {
	if raw, _ := spanTexts("`a` `b` `c`", 2); len(raw) != 2 {
		t.Fatalf("limit 2 returned %q", raw)
	}
	if raw, _ := spanTexts("`a`", 0); len(raw) != 0 {
		t.Fatalf("limit 0 returned %q", raw)
	}
	// Spaces only are kept whole: CommonMark strips the padding only around other content.
	if _, render := spanTexts("`  `", 4); !slices.Equal(render, []string{"  "}) {
		t.Fatalf("space-only span rendered %q", render)
	}
	// A span at the very end of the text, and a closing run at the very end.
	if raw, _ := spanTexts("x `y`", 8); !slices.Equal(raw, []string{"`y`"}) {
		t.Fatalf("trailing span: %q", raw)
	}
}

func TestMarkdownShellFence(t *testing.T) {
	cases := map[string]util.MarkdownShell{
		"```bash":           util.ShellScript,
		"```{.sh}":          util.ShellScript,
		"~~~PowerShell":     util.ShellScript,
		"```console":        util.ShellSession,
		"```go":             util.ShellNone,
		"```json":           util.ShellNone,
		"```mermaid":        util.ShellNone,
		"```text":           util.ShellNone,
		"```":               util.ShellNone,
		"```bash title=x y": util.ShellScript,
	}
	for opening, want := range cases {
		marker := opening[:3]
		if got := util.MarkdownShellFence(util.MarkdownFenceLanguage(opening, marker)); got != want {
			t.Errorf("%q read as %v, want %v", opening, got, want)
		}
	}
}

func TestMarkdownShellCommand(t *testing.T) {
	cases := []struct {
		shell   util.MarkdownShell
		line    string
		command string
		ok      bool
	}{
		{util.ShellScript, "make test", "make test", true},
		{util.ShellScript, "$ make test", "make test", true},
		{util.ShellSession, "$ make test", "make test", true},
		{util.ShellSession, "ok  example/pkg", "", false},
		{util.ShellScript, "# a comment", "", false},
		{util.ShellScript, "", "", false},
		{util.ShellNone, "make test", "", false},
		{util.ShellSession, "$", "", false},
	}
	for _, tc := range cases {
		command, ok := util.MarkdownShellCommand(tc.shell, tc.line)
		if command != tc.command || ok != tc.ok {
			t.Errorf("%v %q = %q, %v; want %q, %v", tc.shell, tc.line, command, ok, tc.command, tc.ok)
		}
	}
}

func TestMarkdownFrontMatterEnd(t *testing.T) {
	cases := map[string]struct {
		lines []string
		want  int
	}{
		"closed":            {[]string{"---", "name: x", "---", "# Body"}, 3},
		"trailing blanks":   {[]string{"--- ", "name: x", "---\t", "body"}, 3},
		"empty block":       {[]string{"---", "---"}, 2},
		"never closed":      {[]string{"---", "name: x", "body"}, 0},
		"not on line one":   {[]string{"", "---", "name: x", "---"}, 0},
		"thematic break":    {[]string{"----", "text", "---"}, 0},
		"empty document":    {nil, 0},
		"single delimiter":  {[]string{"---"}, 0},
		"indented opener":   {[]string{" ---", "name: x", "---"}, 0},
		"later block stays": {[]string{"---", "a: 1", "---", "---", "b: 2", "---"}, 3},
	}
	for name, tc := range cases {
		if got := util.MarkdownFrontMatterEnd(tc.lines); got != tc.want {
			t.Errorf("%s: end %d, want %d", name, got, tc.want)
		}
	}
}
