package reviewmerge

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// completeReview supplies an independent, fully populated draft and matching final.
func completeReview() ([]Draft, Final) {
	f := Finding{ID: "x", Claim: "defect", Pointer: "x.go:1", Evidence: "proof"}
	drafts := []Draft{{Model: "one", Kind: "code", Findings: []Finding{f}}}
	final := Final{Combiner: "combiner", Verdict: "approve", Kept: []KeptFinding{{
		ID: f.ID, Claim: f.Claim, Pointer: f.Pointer, Evidence: f.Evidence, Sources: []string{"one"},
	}}}
	return drafts, final
}

// requireReviewProblem requires a specific rule rather than accepting unrelated failures.
func requireReviewProblem(t *testing.T, drafts []Draft, final Final, want string) {
	t.Helper()
	got := fmt.Sprint(Validate(drafts, final))
	if !strings.Contains(got, want) {
		t.Fatalf("missing %q: %s", want, got)
	}
}

// TestReviewDraftFindingsRequired distinguishes omitted/null findings from an empty review.
func TestReviewDraftFindingsRequired(t *testing.T) {
	for _, value := range []string{"", `,"findings":null`, `,"findings":[]`} {
		t.Run(value, func(t *testing.T) {
			var d Draft
			if err := json.Unmarshal([]byte(`{"model":"one","kind":"code"`+value+`}`), &d); err != nil {
				t.Fatal(err)
			}
			final := Final{Combiner: "combiner", Verdict: "approve"}
			p := Validate([]Draft{d}, final)
			if (len(p) == 0) != (value == `,"findings":[]`) {
				t.Fatalf("findings %q: %v", value, p)
			}
		})
	}
}

// TestReviewFindingContent rejects every missing required field in each finding category.
func TestReviewFindingContent(t *testing.T) {
	for _, kind := range []string{"draft", "kept", "added"} {
		for _, field := range []string{"claim", "pointer", "evidence"} {
			t.Run(kind+"/"+field, func(t *testing.T) {
				d, f := completeReview()
				finding := &d[0].Findings[0]
				if kind == "added" {
					f.Added = []Finding{{ID: "added:y", Claim: "new", Pointer: "y.go:2", Evidence: "proof"}}
					finding = &f.Added[0]
				}
				values := map[string]*string{"claim": &finding.Claim, "pointer": &finding.Pointer, "evidence": &finding.Evidence}
				if kind == "kept" {
					values = map[string]*string{"claim": &f.Kept[0].Claim, "pointer": &f.Kept[0].Pointer, "evidence": &f.Kept[0].Evidence}
				}
				*values[field] = " \t "
				requireReviewProblem(t, d, f, field+" required")
			})
		}
	}
}

// TestReviewScopedIDs prevents one disposition from swallowing distinct draft-scoped claims.
func TestReviewScopedIDs(t *testing.T) {
	d, f := completeReview()
	d = append(d, Draft{Model: "two", Kind: "code", Findings: []Finding{{ID: "x", Claim: "other", Pointer: "y.go:2", Evidence: "other proof"}}})
	f.Kept[0].Sources = []string{"one", "two"}
	requireReviewProblem(t, d, f, "draft finding must occur exactly once")
	f.Kept[0].Sources = []string{"one"}
	f.Dropped = []DroppedClaim{{ID: "x", Claim: "other", Pointer: "y.go:2", Reason: "false positive"}}
	if p := Validate(d, f); len(p) != 0 {
		t.Fatalf("independent dispositions: %v", p)
	}
	f.Kept = nil
	f.Dropped = append(f.Dropped, DroppedClaim{ID: "x", Claim: "defect", Pointer: "x.go:1", Reason: "tested"})
	if p := Validate(d, f); len(p) != 0 {
		t.Fatalf("independent drops: %v", p)
	}
}

// TestReviewDuplicateSources refuses duplicate attribution but accepts the exact union.
func TestReviewDuplicateSources(t *testing.T) {
	d, f := completeReview()
	if p := Validate(d, f); len(p) != 0 {
		t.Fatal(p)
	}
	f.Kept[0].Sources = []string{"one", "one"}
	requireReviewProblem(t, d, f, "duplicate source")
}

// TestReviewKeptSubstance rejects changes to any originating finding field.
func TestReviewKeptSubstance(t *testing.T) {
	for _, field := range []string{"claim", "pointer", "evidence"} {
		t.Run(field, func(t *testing.T) {
			d, f := completeReview()
			values := map[string]*string{"claim": &f.Kept[0].Claim, "pointer": &f.Kept[0].Pointer, "evidence": &f.Kept[0].Evidence}
			*values[field] = "changed.go:9"
			requireReviewProblem(t, d, f, "kept finding differs from draft")
		})
	}
}

// TestReviewPointerSyntax covers each finding category and path:positive-line boundaries.
func TestReviewPointerSyntax(t *testing.T) {
	for _, pointer := range []string{"x", ":1", "x:0", "x:-1", "x:+1", "x:1:2", "x:1\n", "x:999999999999999999999999", "x:1", `C:\src\x.go:12`} {
		for _, kind := range []string{"draft", "kept", "added", "dropped"} {
			t.Run(kind+"/"+pointer, func(t *testing.T) {
				d, f := completeReview()
				switch kind {
				case "draft":
					d[0].Findings[0].Pointer = pointer
					f.Kept[0].Pointer = pointer
				case "kept":
					f.Kept[0].Pointer = pointer
					d[0].Findings[0].Pointer = pointer
				case "added":
					f.Added = []Finding{{ID: "added:y", Claim: "new", Pointer: pointer, Evidence: "proof"}}
				case "dropped":
					f.Kept = nil
					d[0].Findings[0].Pointer = pointer
					f.Dropped = []DroppedClaim{{ID: "x", Claim: "defect", Reason: "tested", Pointer: pointer}}
				}
				p := Validate(d, f)
				valid := pointer == "x:1" || pointer == `C:\src\x.go:12`
				if (len(p) == 0) != valid {
					t.Fatalf("pointer %q: %v", pointer, p)
				}
			})
		}
	}
}

// TestReviewDraftDecodeBounds refuses an oversized array before retaining its decoded records.
func TestReviewDraftDecodeBounds(t *testing.T) {
	for _, count := range []int{0, MaxFindings, MaxFindings + 1} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			items := make([]string, count)
			for i := range items {
				items[i] = fmt.Sprintf(`{"id":"%d","claim":"c","pointer":"x:1","evidence":"e"}`, i)
			}
			var d Draft
			err := json.Unmarshal([]byte(`{"model":"m","kind":"code","findings":[`+strings.Join(items, ",")+`]}`), &d)
			if (err == nil) != (count <= MaxFindings) {
				t.Fatalf("count %d: %v", count, err)
			}
			if err != nil && d.Findings != nil {
				t.Fatal("failed decode retained findings")
			}
		})
	}
}

// TestReviewTerminalEscaping keeps diagnostics on one terminal-safe line.
func TestReviewTerminalEscaping(t *testing.T) {
	p := Problem{ID: "x\x1b[2J\nforged\r", Rule: "source \x1b[31m\u0085\u2028"}
	got := p.String()
	if strings.ContainsAny(got, "\x1b\n\r\u0085\u2028") || !strings.Contains(got, `\x1b`) {
		t.Fatalf("unsafe terminal output: %q", got)
	}
}

// TestReviewMissingRuleFixtures exercises existing kind, verdict and duplicate-ID refusals.
func TestReviewMissingRuleFixtures(t *testing.T) {
	cases := []struct {
		name, rule string
		edit       func([]Draft, *Final)
	}{
		{"kind", "invalid draft kind", func(d []Draft, _ *Final) { d[0].Kind = "other" }},
		{"verdict", "invalid verdict", func(_ []Draft, f *Final) { f.Verdict = "other" }},
		{"draft id", "duplicate id in draft", func(d []Draft, _ *Final) { d[0].Findings = append(d[0].Findings, d[0].Findings[0]) }},
		{"added id", "added id must be unique", func(_ []Draft, f *Final) {
			a := Finding{ID: "added:y", Claim: "new", Pointer: "y.go:2", Evidence: "proof"}
			f.Added = []Finding{a, a}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, f := completeReview()
			tc.edit(d, &f)
			requireReviewProblem(t, d, f, tc.rule)
		})
	}
}

// TestReviewMergeDocumentationReferences keeps contract claims traceable to code and tests.
func TestReviewMergeDocumentationReferences(t *testing.T) {
	data, err := os.ReadFile("../../docs/standards/model-routing-and-fanout.md")
	if err != nil {
		t.Fatal(err)
	}
	_, section, ok := strings.Cut(string(data), "## Merging reviews")
	if !ok {
		t.Fatal("missing review merge guide")
	}
	for _, reference := range []string{
		"../../internal/reviewmerge/reviewmerge.go",
		"../../internal/reviewmerge/final.go",
		"../../internal/reviewmerge/decode.go",
		"../../internal/reviewmerge/reviewmerge_regression_test.go",
		"../../cmd/standardsctl/models_merge.go",
		"../../cmd/standardsctl/models_merge_regression_test.go",
	} {
		if !strings.Contains(section, reference) {
			t.Errorf("missing contract reference %s", reference)
		}
	}
}
