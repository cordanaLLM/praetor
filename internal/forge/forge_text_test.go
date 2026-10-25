// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestValidateForgeParagraphs_Positive verifies that paragraphs on a single line pass.
func TestValidateForgeParagraphs_Positive(t *testing.T) {
	singleLine := "This is a single-line paragraph that is not hard-wrapped across lines."
	findings := ValidateForgeParagraphs(singleLine, "test.md")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for single-line paragraph, got: %v", findings)
	}
}

// TestValidateForgeParagraphs_Negative verifies that a planted hard-wrapped paragraph fails.
func TestValidateForgeParagraphs_Negative(t *testing.T) {
	planted := "This is a paragraph that has been\nhard-wrapped into two lines."
	findings := ValidateForgeParagraphs(planted, "test.md")
	if len(findings) == 0 {
		t.Fatal("expected failure on planted hard-wrapped paragraph, but got 0 findings")
	}
	expectedSub := "test.md line 1: hard-wrapped forge paragraph"
	if !strings.Contains(findings[0], expectedSub) {
		t.Fatalf("finding %q does not contain expected %q", findings[0], expectedSub)
	}
}

// TestValidateForgeParagraphs_UntouchedStructures verifies fences, tables, lists, and receipt fence pass.
func TestValidateForgeParagraphs_UntouchedStructures(t *testing.T) {
	sample := "# Heading 1\n\n" +
		"> Blockquote line 1\n" +
		"> Blockquote line 2\n\n" +
		"- Item 1\n" +
		"- Item 2\n\n" +
		"| Header 1 | Header 2 |\n" +
		"| --- | --- |\n" +
		"| Val 1 | Val 2 |\n\n" +
		"```go\n" +
		"func wrappedCode() {\n" +
		"    // mid-sentence\n" +
		"    codeLine := true\n" +
		"}\n" +
		"```\n\n" +
		"```receipt\n" +
		"{\n" +
		"  \"command\": \"praetorctl gate run\"\n" +
		"}\n" +
		"```\n\n" +
		"<!-- HTML comment\n" +
		"multi-line\n" +
		"-->\n"
	findings := ValidateForgeParagraphs(sample, "structures.md")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for structured markdown, got: %v", findings)
	}
}

// TestValidateForgeParagraphs_FixedPRTemplatePasses verifies that the fixed .github/pull_request_template.md passes.
func TestValidateForgeParagraphs_FixedPRTemplatePasses(t *testing.T) {
	path := filepath.Join("..", "..", ".github", "pull_request_template.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed reading template: %v", err)
	}
	findings := ValidateForgeParagraphs(string(data), ".github/pull_request_template.md")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings on fixed PR template, got: %v", findings)
	}
}

// TestValidateForgeParagraphs_UnfixedPRTemplateFails verifies detection on the original hard-wrapped receipt section.
func TestValidateForgeParagraphs_UnfixedPRTemplateFails(t *testing.T) {
	unfixedReceiptSection := "## 4. Ed25519 Exit-0 Verification Receipt\n\n" +
		"Paste the **signed receipt JSON** below, inside the fence labelled `receipt`. The label is what\n" +
		"the validator looks for: an unlabelled or `text` fence is treated as a code snippet in a\n" +
		"description and never read.\n\n" +
		"`praetorctl gate run` writes it to `.standards-receipt.json`, and the pre-push hook keeps a copy\n" +
		"at `.git/praetor-receipts/<sha>.json`. It must certify the pull request's head commit; a rebase\n" +
		"invalidates it, so re-mint after one.\n"

	findings := ValidateForgeParagraphs(unfixedReceiptSection, ".github/pull_request_template.md")
	if len(findings) != 4 {
		t.Fatalf("expected 4 findings on unfixed receipt text, got %d: %v", len(findings), findings)
	}
}

// TestUnwrapForgeText_Positive verifies unwrapping 72-col text into single lines.
func TestUnwrapForgeText_Positive(t *testing.T) {
	input := "This is a commit message body that is wrapped at seventy-two columns\n" +
		"according to standard git commit conventions. It continues here.\n\n" +
		"- A list item that also happens\n" +
		"  to be wrapped on two lines\n" +
		"- Another list item\n\n" +
		"```go\n" +
		"func code() {\n" +
		"    return\n" +
		"}\n" +
		"```\n\n" +
		"| col1 | col2 |\n" +
		"| --- | --- |\n" +
		"| val1 | val2 |\n"

	unwrapped := UnwrapForgeText(input)
	findings := ValidateForgeParagraphs(unwrapped, "unwrapped.md")
	if len(findings) != 0 {
		t.Fatalf("unwrapped forge text failed validation: %v", findings)
	}

	expectedParagraph := "This is a commit message body that is wrapped at seventy-two columns according to standard git commit conventions. It continues here."
	if !strings.Contains(unwrapped, expectedParagraph) {
		t.Fatalf("unwrapped text did not contain joined paragraph: %q", unwrapped)
	}
	expectedListItem := "- A list item that also happens to be wrapped on two lines"
	if !strings.Contains(unwrapped, expectedListItem) {
		t.Fatalf("unwrapped text did not contain joined list item: %q", unwrapped)
	}
}

// TestUnwrapForgeText_ExactAndIdempotent pins the whole output, and unwrapping it again changes
// nothing: a trailing newline stays one newline, and an issue reference starting a continuation
// line is paragraph text, not a heading.
func TestUnwrapForgeText_ExactAndIdempotent(t *testing.T) {
	for _, tc := range []struct{ name, in, want string }{
		{"trailing newline", "wrapped\nline.\n", "wrapped line.\n"},
		{"crlf", "wrapped\r\nline.\r\n", "wrapped line.\r\n"},
		{"no trailing newline", "wrapped\nline.", "wrapped line."},
		{"issue reference", "Fixes the bug reported in\n#1379 by the reviewer.\n", "Fixes the bug reported in #1379 by the reviewer.\n"},
		{"heading", "## Title\nbody\n", "## Title\nbody\n"},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := UnwrapForgeText(tc.in)
			if got != tc.want {
				t.Fatalf("UnwrapForgeText(%q) = %q, want %q", tc.in, got, tc.want)
			}
			if again := UnwrapForgeText(got); again != got {
				t.Fatalf("second unwrap changed %q to %q", got, again)
			}
		})
	}
}

// TestValidateForgeParagraphs_IssueReferenceContinues reports a paragraph whose continuation
// line starts with an issue reference; a real heading after a line still ends the paragraph.
func TestValidateForgeParagraphs_IssueReferenceContinues(t *testing.T) {
	if got := ValidateForgeParagraphs("Fixes the bug reported in\n#1379 by the reviewer.", "ref.md"); len(got) != 1 {
		t.Fatalf("want one finding for the wrapped issue reference, got %v", got)
	}
	if got := ValidateForgeParagraphs("Intro without a stop\n## Heading", "heading.md"); len(got) != 0 {
		t.Fatalf("a heading is no continuation line, got %v", got)
	}
}

// mutantValidate skips paragraph check and demonstrates mutant survival without check.
func mutantValidate(text, where string) []string {
	_ = text
	_ = where
	return nil
}

// TestValidateForgeParagraphs_Mutant verifies that mutant validator fails to catch planted hard-wrap.
func TestValidateForgeParagraphs_Mutant(t *testing.T) {
	planted := "This is a paragraph that has been\nhard-wrapped into two lines."
	realFindings := ValidateForgeParagraphs(planted, "test.md")
	if len(realFindings) == 0 {
		t.Fatal("real validator should have caught hard-wrapped paragraph")
	}
	mutantFindings := mutantValidate(planted, "test.md")
	if len(mutantFindings) != 0 {
		t.Fatal("mutant validator should have skipped check and returned 0 findings")
	}
}
