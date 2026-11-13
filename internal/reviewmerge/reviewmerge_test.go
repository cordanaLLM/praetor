package reviewmerge

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestReviewMergeRules checks the review merge contract.
func TestReviewMergeRules(t *testing.T) {
	drafts := []Draft{{Model: "one", Kind: "code", Findings: []Finding{{ID: "a", Claim: "claim a", Pointer: "a.go:1", Evidence: "proof"}, {ID: "b", Claim: "claim b", Pointer: "b.go:2", Evidence: "proof"}}}}
	good := Final{Combiner: "merge", Verdict: "approve", Kept: []KeptFinding{{ID: "a", Claim: "claim a", Pointer: "a.go:1", Evidence: "proof", Sources: []string{"one"}}}, Dropped: []DroppedClaim{{ID: "b", Claim: "claim b", Reason: "false", Pointer: "b.go:2"}}, Added: []Finding{{ID: "added:c", Claim: "new", Pointer: "c.go:3", Evidence: "proof"}}}
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

// TestReviewDraftRejectsVerdict checks the review merge contract.
func TestReviewDraftRejectsVerdict(t *testing.T) {
	var d Draft
	if err := json.Unmarshal([]byte(`{"model":"m","kind":"code","findings":[],"verdict":"approve"}`), &d); err == nil {
		t.Fatal("draft verdict accepted")
	}
}

// TestReviewMergeBounds checks the review merge contract.
func TestReviewMergeBounds(t *testing.T) {
	if p := Validate([]Draft{{Model: "m", Kind: "code", Findings: []Finding{}}}, Final{Combiner: "c", Verdict: "approve"}); len(p) > 0 {
		t.Fatal(p)
	}
	drafts := make([]Draft, MaxDrafts)
	for i := range drafts {
		drafts[i] = Draft{Model: fmt.Sprint(i), Kind: "code", Findings: []Finding{}}
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
		findings[i] = Finding{ID: id, Claim: "defect", Pointer: "x:1", Evidence: "proof"}
		kept[i] = KeptFinding{ID: id, Claim: "defect", Pointer: "x:1", Evidence: "proof", Sources: []string{"m"}}
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

// TestReviewMergeDifferentIDs verifies coverage and source attribution across draft IDs.
func TestReviewMergeDifferentIDs(t *testing.T) {
	drafts := []Draft{
		{Model: "a", Kind: "code", Findings: []Finding{{ID: "a-1", Claim: "defect", Pointer: "x:1", Evidence: "proof"}}},
		{Model: "b", Kind: "code", Findings: []Finding{{ID: "b-3", Claim: "defect", Pointer: "x:1", Evidence: "proof"}}},
	}
	cases := []struct{ name, input, want string }{
		{"union", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a","b"]}]}`, ""},
		{"missing source", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a"]}]}`, "missing source b"},
		{"unknown merged", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3","unknown"],"sources":["a","b"]}]}`, "unknown: merged id not raised in draft"},
		{"extra source", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a","b","c"]}]}`, "source did not raise finding: c"},
		{"merged twice", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3","b-3"],"sources":["a","b"]}]}`, "b-3: draft finding must occur exactly once"},
		{"merged and kept", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a","b"]},{"id":"b-3","claim":"defect","pointer":"x:1","evidence":"proof","sources":["b"]}]}`, "b-3: draft finding must occur exactly once"},
		{"merged and added", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a","b"]}],"added":[{"id":"b-3"}]}`, "b-3: added id already raised"},
		{"merged and dropped", `{"combiner":"c","verdict":"approve","kept":[{"id":"a-1","claim":"defect","pointer":"x:1","evidence":"proof","merged_ids":["b-3"],"sources":["a","b"]}],"dropped":[{"id":"b-3","claim":"defect","reason":"duplicate","pointer":"x:1"}]}`, "b-3: draft finding must occur exactly once"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var f Final
			if err := json.Unmarshal([]byte(tc.input), &f); err != nil {
				t.Fatal(err)
			}
			problems := Validate(drafts, f)
			if tc.want == "" && len(problems) != 0 {
				t.Fatalf("unexpected: %v", problems)
			}
			if tc.want != "" && !strings.Contains(fmt.Sprint(problems), tc.want) {
				t.Fatalf("missing %q: %v", tc.want, problems)
			}
		})
	}
}

// TestReviewMergeMergedIDBounds checks the limit on merged IDs.
func TestReviewMergeMergedIDBounds(t *testing.T) {
	f := Final{Combiner: "c", Verdict: "approve", Kept: []KeptFinding{{ID: "a", MergedIDs: make([]string, MaxFindings+1)}}}
	if p := Validate(nil, f); !strings.Contains(fmt.Sprint(p), "merged id count exceeds limit") {
		t.Fatal(p)
	}
}
