// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"slices"
	"strings"
	"testing"
)

func TestScan_Positive_SpansAndShellCommands(t *testing.T) {
	doc := "# T\n\nUse `a b` and ``c ` d``.\n\n```bash\n# comment\n$ praetorctl audit \\\n  --strict\n\nnext\n```\n"
	candidates, problems := Scan("d.md", doc)
	if len(problems) != 0 {
		t.Fatalf("problems = %v", problems)
	}
	want := []Candidate{
		{Line: 3, Text: "a b"},
		{Line: 3, Text: "c ` d"},
		{Line: 7, Text: "praetorctl audit  --strict", Shell: true},
		{Line: 10, Text: "next", Shell: true},
	}
	if !slices.Equal(candidates, want) {
		t.Fatalf("candidates = %#v\nwant %#v", candidates, want)
	}
}

func TestScan_Negative_NonShellFencesAndSuppressedBlocksYieldNothing(t *testing.T) {
	doc := "```yaml\nrun: praetorctl nosuch\n```\n\n~~~\npraetorctl nosuch\n~~~\n\n" +
		"<!-- praetor:docs-references:off example -->\n\n`praetorctl nosuch`\n\n```sh\npraetorctl nosuch\n```\n\n" +
		"<!-- praetor:docs-references:on -->\n"
	candidates, problems := Scan("d.md", doc)
	if len(candidates) != 0 || len(problems) != 0 {
		t.Fatalf("candidates = %#v, problems = %v; want none", candidates, problems)
	}
}

func TestScan_Negative_MalformedDirectivesAreFindings(t *testing.T) {
	doc := "<!-- praetor:docs-references:off\n-->\n<!-- praetor:docs-references:on -->\n"
	_, problems := Scan("d.md", doc)
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "needs a reason") {
		t.Fatalf("problems = %v, want one missing-reason finding", problems)
	}
}

func TestScan_Boundary_LineEndingsUnclosedSpansAndBudget(t *testing.T) {
	candidates, _ := Scan("d.md", "x `one`\r\nan unclosed `span\r\n`` a `` b`` c\r\n")
	want := []Candidate{{Line: 1, Text: "one"}, {Line: 3, Text: "a"}}
	if !slices.Equal(candidates, want) {
		t.Fatalf("candidates = %#v\nwant %#v", candidates, want)
	}
	// An unterminated fence swallows the rest of the document, as the shared fence tracker
	// does for every scanner, and a shell command still pending at the end is emitted.
	candidates, _ = Scan("d.md", "```bash\npraetorctl audit \\\n")
	if len(candidates) != 1 || candidates[0].Text != "praetorctl audit" {
		t.Fatalf("pending command at end of document = %#v", candidates)
	}
	_, problems := Scan("d.md", strings.Repeat("\n", maxDocumentLines))
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "exceeds") {
		t.Fatalf("an over-budget document must be one finding, got %v", problems)
	}
	if _, problems = Scan("d.md", strings.Repeat("\n", maxDocumentLines-1)); len(problems) != 0 {
		t.Fatalf("a document at the budget must scan, got %v", problems)
	}
}
