// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package radar

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// neutralizeCases pairs untrusted text with the inert Markdown Neutralize must render.
var neutralizeCases = []struct{ name, in, want string }{
	{"mention", "thanks @someone and @example-org/team", "thanks someone and example-org/team"},
	{"e-mail address", "write to a@example.org", "write to aexample.org"},
	{"issue reference", "fixes #123 and owner/repo#45", "fixes # 123 and owner/repo# 45"},
	{"GH reference", "see GH-12 and gh-7", "see GH 12 and gh 7"},
	{"hashtag", "#release notes", "#release notes"},
	{"table break", "a | b", "a ¦ b"},
	{"html tags", "<script>alert(1)</script>x<b>y</b>", "alert\\(1\\)xy"},
	{"encoded html", "&lt;img src=x onerror=y&gt;caption", "caption"},
	{"entity", "Fish &amp; chips", "Fish \\& chips"},
	{"line breaks", "line1\nline2\r\n\tline3", "line1 line2 line3"},
	{"markdown", "**b** _i_ `c` [l](x) ![i](y) ~s~", "\\*\\*b\\*\\* \\_i\\_ \\`c\\` \\[l\\]\\(x\\) \\!\\[i\\]\\(y\\) \\~s\\~"},
	{"url", "see https://example.org/a or www.example.org", "see https\\[:\\]//example.org/a or www\\[.\\]example.org"},
	{"invisible characters", "a\u200bb\u202ec\u0007d", "abcd"},
	{"invalid UTF-8", "a\xffb", "ab"},
	{"backslash", `a\b`, `a\\b`},
	{"whitespace only", " \n\t ", ""},
}

// Positive: every untrusted construct renders inert, and plain text passes unchanged.
func TestNeutralize_Positive_Table(t *testing.T) {
	for _, tc := range neutralizeCases {
		if got := Neutralize(tc.in, MaxTitleRunes); got != tc.want {
			t.Errorf("%s: Neutralize(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
	if got := Neutralize("A faster tokenizer, version 2", MaxTitleRunes); got != "A faster tokenizer, version 2" {
		t.Errorf("plain text changed: %q", got)
	}
}

// Negative: no rendered title may still carry a mention, an issue reference, a table break, an
// HTML tag or a line break, whatever the input.
func TestNeutralize_Negative_NoActiveConstructSurvives(t *testing.T) {
	hostile := "@a #1 GH-2 | <i>x</i> \n &lt;b&gt; @@b ##3 #\u200b4"
	got := Neutralize(hostile, MaxTitleRunes)
	for _, banned := range []string{"@", "#1", "#3", "#4", "GH-2", "|", "<", ">", "\n"} {
		if strings.Contains(got, banned) {
			t.Errorf("Neutralize(%q) = %q still carries %q", hostile, got, banned)
		}
	}
}

// Boundary: text of exactly the rune cap is kept whole and one rune more is cut with an
// ellipsis; the cut happens before escaping, so it never leaves a dangling backslash; a cap below
// one keeps the text.
func TestNeutralize_Boundary_Truncation(t *testing.T) {
	exact := strings.Repeat("é", MaxTitleRunes)
	if got := Neutralize(exact, MaxTitleRunes); got != exact {
		t.Errorf("text at the cap changed: %d runes", len([]rune(got)))
	}
	if got := Neutralize(exact+"x", MaxTitleRunes); got != exact+"…" {
		t.Errorf("text over the cap = %q", got)
	}
	if got := Neutralize(strings.Repeat("*", 5), 3); got != `\*\*\*…` {
		t.Errorf("escaped cut = %q", got)
	}
	if got := Neutralize("abc", 0); got != "abc" {
		t.Errorf("cap 0 = %q", got)
	}
}

// digestWindow is the window the digest tests render.
func digestWindow(t *testing.T) Window {
	t.Helper()
	w, err := NewWindow(time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC), 7)
	if err != nil {
		t.Fatal(err)
	}
	return w
}

// Positive: a digest renders its title, window, a section per source with neutralised titles,
// safe links as links, issue links as code, unsafe links not at all, and the failed sources.
func TestRender_Positive_SectionsAndFailures(t *testing.T) {
	when := time.Date(2026, 10, 2, 9, 0, 0, 0, time.UTC)
	feed := Source{ID: "example-blog", Kind: KindFeed, URL: "https://example.org/feed.xml"}
	d := Digest{Window: digestWindow(t), Read: 1, Sections: []Section{{Source: feed, Undated: 2, Items: []Entry{
		{Title: "Hello @someone #1", Link: "https://example.org/hello", Published: when},
		{Title: "Bug report", Link: "https://github.com/example-org/x/issues/5", Published: when},
		{Title: "Sneaky", Link: "javascript:alert(1)", Published: when},
		{Title: "", Link: "https://example.org/a)b", Published: when},
		{Title: "Encoded", Link: "https://example.org/v1%2B2", Published: when},
		{Title: "Spaced", Link: "https://example.org/a b", Published: when},
	}}}, Failed: []Failure{{Source: Source{ID: "broken"}, Reason: "read fixture broken.xml: missing | @x"}}}
	out := string(Render(d))
	for _, want := range []string{
		"# Radar digest 2026-09-30 to 2026-10-07\n",
		"Window: 2026-09-30T00:00:00Z <= t < 2026-10-07T00:00:00Z (UTC). Sources read: 1, failed: 1.",
		"## example-blog (feed)\n\nSource: <https://example.org/feed.xml>",
		"- 2026-10-02 [Hello someone # 1](https://example.org/hello)\n",
		"- 2026-10-02 Bug report (`https://github.com/example-org/x/issues/5`)\n",
		"- 2026-10-02 Sneaky\n",
		"- 2026-10-02 (untitled)\n",
		"- 2026-10-02 [Encoded](https://example.org/v1%2B2)\n",
		"- 2026-10-02 Spaced\n",
		"2 entries carry no readable date and are in no window.",
		"## Failed sources\n\n- broken: read fixture broken.xml: missing ¦ x\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("digest lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "javascript:") {
		t.Errorf("digest renders an unsafe link:\n%s", out)
	}
}

// Negative: a digest with nothing new and no failed source renders as no bytes at all.
func TestRender_Negative_EmptyDigest(t *testing.T) {
	if out := Render(Digest{Window: digestWindow(t), Read: 3}); len(out) != 0 {
		t.Fatalf("empty digest rendered %q", out)
	}
}

// Boundary: a section of exactly MaxSectionItems items lists all of them; one more lists the cap
// and counts the rest.
func TestRender_Boundary_SectionCap(t *testing.T) {
	source := Source{ID: "busy", Kind: KindFeed, URL: "https://example.org/busy.xml"}
	render := func(count int) string {
		items := make([]Entry, count)
		for i := range items {
			items[i] = Entry{Title: fmt.Sprintf("item %d", i), Published: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
		}
		return string(Render(Digest{Window: digestWindow(t), Read: 1, Sections: []Section{{Source: source, Items: items}}}))
	}
	full := render(MaxSectionItems)
	if strings.Count(full, "- 2026-10-01 item") != MaxSectionItems || strings.Contains(full, "more not shown") {
		t.Errorf("%d items:\n%s", MaxSectionItems, full)
	}
	over := render(MaxSectionItems + 1)
	if strings.Count(over, "- 2026-10-01 item") != MaxSectionItems || !strings.Contains(over, "- 1 more not shown (section cap 50).") {
		t.Errorf("%d items:\n%s", MaxSectionItems+1, over)
	}
}
