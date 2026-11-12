package reviewmerge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReviewMergeRules(t *testing.T) {
	drafts := []Draft{{Model: "one", Kind: "code", Findings: []Finding{{ID: "a", Claim: "claim a", Pointer: "a.go:1", Evidence: "proof"}, {ID: "b", Claim: "claim b", Pointer: "b.go:2", Evidence: "proof"}}}}
	good := Final{Combiner: "merge", Verdict: "approve", Kept: []KeptFinding{{ID: "a", Sources: []string{"one"}}}, Dropped: []DroppedClaim{{ID: "b", Claim: "claim b", Reason: "false", Pointer: "b.go:2"}}, Added: []Finding{{ID: "added:c"}}}
	cases := []struct {
		name, want string
		edit       func(*Final)
	}{
		{"valid", "", func(*Final) {}},
		{"omission", "b", func(f *Final) { f.Dropped = nil }},
		{"unmarked", "c", func(f *Final) { f.Added[0].ID = "c" }},
		{"changed claim", "b", func(f *Final) { f.Dropped[0].Claim = "different" }},
		{"reason", "b", func(f *Final) { f.Dropped[0].Reason = "" }},
		{"pointer", "b", func(f *Final) { f.Dropped[0].Pointer = "" }},
		{"source", "a", func(f *Final) { f.Kept[0].Sources = []string{"other"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := good
			f.Kept = append([]KeptFinding(nil), good.Kept...)
			f.Dropped = append([]DroppedClaim(nil), good.Dropped...)
			f.Added = append([]Finding(nil), good.Added...)
			tc.edit(&f)
			problems := Validate(drafts, f)
			if tc.want == "" && len(problems) > 0 {
				t.Fatalf("%v", problems)
			}
			if tc.want != "" && !strings.Contains(fmt.Sprint(problems), tc.want) {
				t.Fatalf("missing %s: %v", tc.want, problems)
			}
		})
	}
}
func TestReviewDraftRejectsVerdict(t *testing.T) {
	var d Draft
	if err := json.Unmarshal([]byte(`{"model":"m","kind":"code","findings":[],"verdict":"approve"}`), &d); err == nil {
		t.Fatal("draft verdict accepted")
	}
}
func TestReviewMergeBounds(t *testing.T) {
	if p := Validate([]Draft{{Model: "m", Kind: "code"}}, Final{Combiner: "c", Verdict: "approve"}); len(p) > 0 {
		t.Fatal(p)
	}
	drafts := make([]Draft, MaxDrafts)
	for i := range drafts {
		drafts[i] = Draft{Model: fmt.Sprint(i), Kind: "code"}
	}
	if p := Validate(drafts, Final{Combiner: "c", Verdict: "approve"}); len(p) > 0 {
		t.Fatal(p)
	}
	if p := Validate(append(drafts, Draft{}), Final{Combiner: "c", Verdict: "approve"}); len(p) == 0 {
		t.Fatal("draft cap")
	}
	findings := make([]Finding, MaxFindings)
	kept := make([]KeptFinding, MaxFindings)
	for i := range findings {
		id := fmt.Sprint(i)
		findings[i] = Finding{ID: id}
		kept[i] = KeptFinding{ID: id, Sources: []string{"m"}}
	}
	d := []Draft{{Model: "m", Kind: "code", Findings: findings}}
	f := Final{Combiner: "c", Verdict: "approve", Kept: kept}
	if p := Validate(d, f); len(p) > 0 {
		t.Fatal(p)
	}
	d[0].Findings = append(findings, Finding{ID: "extra"})
	if p := Validate(d, f); len(p) == 0 {
		t.Fatal("finding cap")
	}
}
