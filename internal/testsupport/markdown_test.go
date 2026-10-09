// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"strings"
	"testing"
)

// Positive: a document shaped the way markdownlint's defaults want passes, including
// HTML comment lines beside blocks, which markdownlint reads as blank.
func TestMarkdownFindingsAcceptsCleanDocument(t *testing.T) {
	doc := "<!-- generated -->\n# Title\n\nText.\n\n## Section\n\n- one\n- two\n  continued\n\n" +
		"1. first\n\n   ```bash\n   make verify-all\n   ```\n\n" +
		"<!-- markdownlint-disable MD013 -->\n\n| wide | table |\n| :--- | :--- |\n| " + strings.Repeat("cell ", 20) + "| x |\n\n" +
		"<!-- markdownlint-enable MD013 -->\n"
	if findings := MarkdownFindings(doc); len(findings) != 0 {
		t.Fatalf("clean document reported %v", findings)
	}
}

// Negative: each rule the generators must hold is reported on its own.
func TestMarkdownFindingsReportsEachRule(t *testing.T) {
	cases := map[string]struct{ doc, rule string }{
		"heading without blank below": {"# T\n\n## S\n- a\n", "MD022"},
		"list without blank above":    {"# T\n\ntext\n- a\n", "MD032"},
		"list without blank below":    {"# T\n\n- a\ntext\n", "MD032"},
		"heading directly after list": {"# T\n\n- a\n## H\n\ntext\n", "MD032"},
		"fence without blank above":   {"# T\n\ntext\n```bash\nx\n```\n", "MD031"},
		"fence without blank below":   {"# T\n\n```bash\nx\n```\ntext\n", "MD031"},
		"double blank line":           {"# T\n\n\ntext\n", "MD012"},
		"second top-level heading":    {"# T\n\n# U\n", "MD025"},
		"long prose line":             {"# T\n\n" + strings.Repeat("word ", 20) + "\n", "MD013"},
		"missing final newline":       {"# T", "MD047"},
		"file-wide disable ended":     {"<!-- markdownlint-disable MD013 -->\n# T\n\n<!-- markdownlint-enable MD013 -->\n\n" + strings.Repeat("word ", 20) + "\n", "MD013"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			findings := MarkdownFindings(tc.doc)
			if !strings.Contains(strings.Join(findings, "\n"), tc.rule) {
				t.Fatalf("findings %v do not report %s", findings, tc.rule)
			}
		})
	}
}

// Boundary: a line of exactly the limit passes, one character more with whitespace past
// the limit fails, an unbreakable token past the limit passes, and a disabled rule stays
// silent until it is enabled again; a closed front matter block is skipped and an unclosed
// one is not.
func TestMarkdownFindingsBoundaries(t *testing.T) {
	atLimit := "# T\n\n" + strings.Repeat("a", MarkdownLineLimit) + "\n"
	if findings := MarkdownFindings(atLimit); len(findings) != 0 {
		t.Errorf("line of exactly %d characters reported %v", MarkdownLineLimit, findings)
	}
	overLimit := "# T\n\n" + strings.Repeat("a", MarkdownLineLimit-1) + " bb\n"
	if findings := MarkdownFindings(overLimit); len(findings) != 1 {
		t.Errorf("line over the limit with a space past it: %v", findings)
	}
	token := "# T\n\nsee " + strings.Repeat("u", 2*MarkdownLineLimit) + "\n"
	if findings := MarkdownFindings(token); len(findings) != 0 {
		t.Errorf("unbreakable token past the limit reported %v", findings)
	}
	disabled := "# T\n\n<!-- markdownlint-disable MD025 -->\n\n# U\n"
	if findings := MarkdownFindings(disabled); len(findings) != 0 {
		t.Errorf("scoped MD025 disable not honoured: %v", findings)
	}
	frontMatter := "---\ndescription: \"" + strings.Repeat("word ", 20) + "\"\n---\n# T\n\ntext\n"
	if findings := MarkdownFindings(frontMatter); len(findings) != 0 {
		t.Errorf("front matter not skipped: %v", findings)
	}
	unclosed := "---\n# T\n" + strings.Repeat("word ", 20) + "\n"
	if findings := MarkdownFindings(unclosed); len(findings) == 0 {
		t.Error("an unclosed front matter block must be linted as the body")
	}
	if findings := MarkdownFindings(strings.Repeat("\n", maxMarkdownLines+1)); len(findings) != 1 {
		t.Errorf("oversized document: %v", findings)
	}
}

// Positive: a four-backtick fence holds a three-backtick one as content, so only the outer
// fence needs blank lines around it. Negative: the shorter run does not close the outer fence,
// so the line after the outer fence's real closer is still checked (MD031). Boundary: a longer
// closing run closes, and a different character does not.
func TestMarkdownFindingsNestedFences(t *testing.T) {
	nested := "# T\n\n````markdown\n```figure\nname\n```\n````\n\ntext\n"
	if findings := MarkdownFindings(nested); len(findings) != 0 {
		t.Errorf("nested fences reported %v", findings)
	}
	unclosed := "# T\n\n````markdown\n```figure\nname\n```\n````\ntext\n"
	if findings := MarkdownFindings(unclosed); !strings.Contains(strings.Join(findings, "\n"), "MD031") {
		t.Errorf("a text line right after the outer fence was not reported: %v", findings)
	}
	longer := "# T\n\n```bash\nx\n`````\n\ntext\n"
	if findings := MarkdownFindings(longer); len(findings) != 0 {
		t.Errorf("a longer closing run did not close the fence: %v", findings)
	}
	tilde := "# T\n\n```bash\nx\n~~~\ny\n```\n\ntext\n"
	if findings := MarkdownFindings(tilde); len(findings) != 0 {
		t.Errorf("a tilde run closed or broke a backtick fence: %v", findings)
	}
}

// Positive, Negative, Boundary (HISS-15, HISS-21): MarkdownTableRowsUnderHeading parses
// backtick-started rows under a heading, ignores other lines and sections, handles CRLF,
// and returns nil when the heading is missing.
func TestMarkdownTableRowsUnderHeading_3D(t *testing.T) {
	md := "## Target\n\n| Col 1 | Col 2 |\n| :--- | :--- |\n| `step1` | desc 1 |\n| plain | ignored |\n| `step2` | desc 2 |\n\n## Next\n| `step3` | after |\n"
	got := MarkdownTableRowsUnderHeading(md, "## Target")
	if len(got) != 2 || got[0][0] != "`step1`" || got[0][1] != "desc 1" || got[1][0] != "`step2`" || got[1][1] != "desc 2" {
		t.Fatalf("positive parse unexpected rows: %+v", got)
	}

	// Negative: missing heading returns nil.
	if got := MarkdownTableRowsUnderHeading(md, "## Missing"); got != nil {
		t.Fatalf("missing heading returned %v, want nil", got)
	}

	// Boundary: CRLF behaves identically to LF.
	crlf := strings.ReplaceAll(md, "\n", "\r\n")
	if crlfGot := MarkdownTableRowsUnderHeading(crlf, "## Target"); len(crlfGot) != len(got) {
		t.Fatalf("CRLF rows length %d differs from LF length %d", len(crlfGot), len(got))
	}
}
