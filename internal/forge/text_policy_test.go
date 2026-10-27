// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// Order: engine-fr-rule-checks-20261027. Planted cases use the public checklist path.
func TestForgeText_WrappedParagraph(t *testing.T) {
	result, err := ValidatePRChecklist("This paragraph has enough prose to wrap across a physical line\nsecond prose line.")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(result.Errors, "\n"), "body line 2: hard-wrapped prose") {
		t.Fatalf("missing hard-wrap refusal at body line 2: %v", result.Errors)
	}
}

func TestForgeText_BodyForeignReferences(t *testing.T) {
	result, err := ValidatePRChecklist("foreign-org/tool#12 and https://github.com/foreign-org/tool/pull/7")
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(result.Errors, "\n")
	if strings.Count(got, "foreign reference") != 2 {
		t.Fatalf("want two foreign reference findings: %s", got)
	}
}

func TestForgeText_BoundaryMarkup(t *testing.T) {
	cases := []struct{ name, body string }{
		{"unwrapped", "One paragraph on one line."},
		{"lists", "- item\n- another"},
		{"heading", "# Heading\nProse."},
		{"quote", "> first\n> second"},
		{"definition", "[a]: https://example.org\n[b]: https://example.org"},
		{"comment_fence", "<!--\n```\n-->\nOne prose line."},
		{"table", "| a | b |\n| c | d |"},
		{"table_without_outer_pipe", "a | b\n--- | ---\nc | d\ne | f"},
		{"fence", "```go\nfirst\nsecond\n```"},
		{"receipt", "```receipt\nfirst\nsecond\n```"},
		{"comment", "<!--\nfirst\nsecond\n-->"},
		{"explicit_spaces", "first  \nsecond"},
		{"explicit_slash", "first\\\nsecond"},
		{"inline", "`foreign-org/tool#12` and `https://github.com/foreign-org/tool/pull/7`"},
		{"bare", "See #12."},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := ValidatePRChecklist(tc.body)
			if err != nil {
				t.Fatal(err)
			}
			got := strings.Join(result.Errors, "\n")
			if strings.Contains(got, "hard-wrapped") || strings.Contains(got, "foreign reference") {
				t.Fatal(got)
			}
		})
	}
}

func TestForgeText_ShippedTemplate(t *testing.T) {
	data, err := os.ReadFile("../../.github/pull_request_template.md")
	if err != nil {
		t.Fatal(err)
	}
	result, err := ValidatePRChecklist(string(data))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(result.Errors, "\n"); strings.Contains(got, "hard-wrapped") {
		t.Fatal(got)
	}
}

func TestForgeText_ReferencePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		count      int
	}{
		{"own", "ACME/tool#12 and https://github.com/acme/tool/pull/7", 0},
		{"foreign_bang", "foreign-org/tool!12", 1},
		{"discussion", "https://github.com/foreign-org/tool/discussions/12", 1},
		{"multiple", "foreign-org/tool#1 foreign-org/tool!2", 2},
		{"inline_multiline", "``foreign-org/tool#1\nand foreign-org/tool!2``", 0},
		{"unclosed_inline", "`foreign-org/tool#1", 1},
		{"fenced", "~~~text\nforeign-org/tool#1\n~~~", 0},
		{"escaped_backtick", "\\`foreign-org/tool#1", 1},
		{"bare", "#12", 0},
		{"http_owner", "http-owner/tool#12", 1},
		{"separate_paragraphs", "`literal\n\nforeign-org/tool#12 `literal", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ForeignReferenceFindings(tc.text, "body", []string{"acme"})
			if len(got) != tc.count {
				t.Fatalf("want %d, got %v", tc.count, got)
			}
		})
	}
}

func TestForgeText_Bounds(t *testing.T) {
	if got := BodyHardWrapFindings(strings.Repeat("\n", MaxPRLinesLimit)); len(got) != 1 {
		t.Fatal(got)
	}
	result, err := ValidatePRChecklist(strings.Repeat("\n", MaxPRLinesLimit) + "foreign-org/tool#1")
	if err != nil || len(result.Errors) != 1 || !strings.Contains(result.Errors[0], "exceeds") {
		t.Fatalf("%+v, %v", result, err)
	}
	if got := ForeignReferenceFindings(strings.Repeat("x", maxReferenceTextBytes+1), "body", nil); len(got) != 1 {
		t.Fatal(got)
	}
}

func TestForgeText_OwnBodyAndWrappedCommit(t *testing.T) {
	body := "See ACME/tool#12 and https://github.com/partners/tool/pull/7."
	policy := ClosingReferencePolicy{OwnOrganizations: []string{"acme", "partners"}}
	result, err := ValidatePRChecklistWithPolicyContext(t.Context(), body, ReceiptPolicy{ClosingReferences: policy})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.Join(result.Errors, "\n"), "foreign reference") {
		t.Fatal(result.Errors)
	}
	commit, err := AnalyzeCommitWithPolicy(t.Context(), "fix: own reference\n\nA wrapped commit body\nmentions ACME/tool#12.", policy)
	if err != nil || !commit.Valid {
		t.Fatalf("wrapped own commit: %+v, %v", commit, err)
	}
}

func TestForgeText_TableContextEndsAtBlank(t *testing.T) {
	body := "a | b\n--- | ---\nc | d\n\nThis paragraph has enough prose with a | pipe to wrap mid-sentence\nand a continuation."
	got := BodyHardWrapFindings(body)
	if len(got) != 1 || !strings.Contains(got[0], "line 6:") {
		t.Fatal(got)
	}
}

func TestForgeText_HardWrapIntentionalBreaks(t *testing.T) {
	long := strings.Repeat("prose ", 12)
	cases := []struct{ name, body string }{
		{"orders", "Summary.\n\nOrder: engine-a-20261027\nOrder: engine-b-20261027\n"},
		{"closing_order", "Fixes #12\nOrder: engine-a-20261027\n"},
		{"html_details", "<details>\n<summary>notes</summary>\n\ntext\n</details>\n"},
		{"Order_with_short_summary", "Summary\nFollow-up summary.\n\nOrder: engine-a-20261027\nOrder: engine-b-20261027\n"},
		{"long_sentences", long + "ends.\n" + long + "ends."},
		{"short_previous", strings.Repeat("x", 49) + "\nContinuation."},
		{"short_runes", strings.Repeat("界", 49) + "\nContinuation."},
		{"trimmed_previous", strings.Repeat("x", 49) + " \t\nContinuation."},
		{"period", long + ".\t\nContinuation."},
		{"exclamation", long + "!\nContinuation."},
		{"question", long + "?\nContinuation."},
		{"colon", long + ":\nContinuation."},
		{"semicolon", long + ";\nContinuation."},
		{"html_after_prose", long + "\n  <details>\n<summary>notes</summary>\n</details>"},
		{"trailers_after_prose", long + "\nOrder: engine-a\nRefs: task\nSigned-off-by: Person"},
		{"closing_keywords", long + "\nFixes #12\n" + long + "\nCloses #13\n" + long + "\nResolves #14\n" + long + "\nRefs #15"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := BodyHardWrapFindings(tc.body); len(got) != 0 {
				t.Fatalf("intentional breaks flagged: %v", got)
			}
		})
	}
}

func TestForgeText_HardWrapProse(t *testing.T) {
	line72 := strings.Repeat("word ", 14) + "ab"
	cases := []struct {
		name, body string
		lines      []int
	}{
		{"wrapped_72_columns", line72 + "\n" + line72 + "\nEnd.", []int{2, 3}},
		{"at_50_runes", strings.Repeat("x", 50) + "\nContinuation.", []int{2}},
		{"at_50_multibyte_runes", strings.Repeat("界", 50) + "\nContinuation.", []int{2}},
		{"lowercase_trailer_is_prose", line72 + "\norder: task", []int{2}},
		{"incomplete_trailer_is_prose", line72 + "\nOrder:", []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := BodyHardWrapFindings(tc.body)
			if len(got) != len(tc.lines) {
				t.Fatalf("want lines %v, got %v", tc.lines, got)
			}
			for i := 0; i < len(tc.lines); i++ {
				if want := fmt.Sprintf("body line %d:", tc.lines[i]); !strings.Contains(got[i], want) {
					t.Fatalf("want %s, got %s", want, got[i])
				}
			}
		})
	}
}
