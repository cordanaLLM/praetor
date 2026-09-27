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
	scan := Scan("d.md", doc)
	candidates, problems := scan.Candidates, scan.Problems
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
		"<!-- praetor:docs-references:off an illustrative example block -->\n\n`praetorctl nosuch`\n\n```sh\npraetorctl nosuch\n```\n\n" +
		"<!-- praetor:docs-references:on -->\n"
	scan := Scan("d.md", doc)
	if len(scan.Candidates) != 0 || len(scan.Problems) != 0 {
		t.Fatalf("candidates = %#v, problems = %v; want none", scan.Candidates, scan.Problems)
	}
	want := []Suppression{{Line: 9, Reason: "an illustrative example block"}}
	if !slices.Equal(scan.Suppressions, want) {
		t.Fatalf("suppressions = %#v, want %#v", scan.Suppressions, want)
	}
}

func TestScan_Negative_MalformedDirectivesAreFindings(t *testing.T) {
	for _, directive := range []string{
		"<!-- praetor:docs-references:off\n-->",
		"<!-- praetor:docs-references:off x -->",
		"<!-- praetor:docs-references:off two words -->",
	} {
		scan := Scan("d.md", directive+"\n<!-- praetor:docs-references:on -->\n")
		if len(scan.Problems) != 1 || !strings.Contains(scan.Problems[0].Message, "needs a reason of at least 3 words") {
			t.Errorf("%q: problems = %v, want one short-reason finding", directive, scan.Problems)
		}
		if len(scan.Suppressions) != 0 {
			t.Errorf("%q: a rejected directive must not count as a suppression, got %v", directive, scan.Suppressions)
		}
	}
}

func TestScan_Positive_TerminalTranscriptReadsOnlyPromptedLines(t *testing.T) {
	doc := "```console\n$ praetorctl version\npraetorctl version dev\ninternal/removedx/file.go:3: output\n" +
		"$ praetorctl audit \\\n    --strict\n```\n"
	scan := Scan("d.md", doc)
	want := []Candidate{
		{Line: 2, Text: "praetorctl version", Shell: true},
		{Line: 5, Text: "praetorctl audit  --strict", Shell: true},
	}
	if !slices.Equal(scan.Candidates, want) {
		t.Fatalf("candidates = %#v\nwant %#v", scan.Candidates, want)
	}
}

func TestScan_Boundary_LineEndingsUnclosedSpansAndBudget(t *testing.T) {
	candidates := Scan("d.md", "x `one`\r\nan unclosed `span\r\n`` a `` b`` c\r\n").Candidates
	want := []Candidate{{Line: 1, Text: "one"}, {Line: 3, Text: "a"}}
	if !slices.Equal(candidates, want) {
		t.Fatalf("candidates = %#v\nwant %#v", candidates, want)
	}
	// An unterminated fence swallows the rest of the document, as the shared fence tracker
	// does for every scanner, and a shell command still pending at the end is emitted.
	candidates = Scan("d.md", "```bash\npraetorctl audit \\\n").Candidates
	if len(candidates) != 1 || candidates[0].Text != "praetorctl audit" {
		t.Fatalf("pending command at end of document = %#v", candidates)
	}
	problems := Scan("d.md", strings.Repeat("\n", maxDocumentLines)).Problems
	if len(problems) != 1 || !strings.Contains(problems[0].Message, "exceeds") {
		t.Fatalf("an over-budget document must be one finding, got %v", problems)
	}
	if problems = Scan("d.md", strings.Repeat("\n", maxDocumentLines-1)).Problems; len(problems) != 0 {
		t.Fatalf("a document at the budget must scan, got %v", problems)
	}
}
