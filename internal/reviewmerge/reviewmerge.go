// Package reviewmerge validates the trace from ungraded reviews to a combined review.
package reviewmerge

import (
	"fmt"
	"sort"
	"strings"

	"github.com/cordanaLLM/praetor/internal/strictjson"
)

const (
	MaxDrafts    = 64
	MaxFindings  = 256
	AddedPrefix  = "added:"
	maxJSONBytes = 4 << 20
)

type Finding struct {
	ID       string `json:"id"`
	Claim    string `json:"claim"`
	Pointer  string `json:"pointer"`
	Evidence string `json:"evidence"`
}
type Draft struct {
	Model    string    `json:"model"`
	Kind     string    `json:"kind"`
	Findings []Finding `json:"findings"`
}
type KeptFinding struct {
	ID       string   `json:"id"`
	Claim    string   `json:"claim"`
	Pointer  string   `json:"pointer"`
	Evidence string   `json:"evidence"`
	Sources  []string `json:"sources"`
}
type DroppedClaim struct {
	ID      string `json:"id"`
	Claim   string `json:"claim"`
	Reason  string `json:"reason"`
	Pointer string `json:"pointer"`
}
type Final struct {
	Combiner string         `json:"combiner"`
	Verdict  string         `json:"verdict"`
	Kept     []KeptFinding  `json:"kept"`
	Added    []Finding      `json:"added"`
	Dropped  []DroppedClaim `json:"dropped"`
}

// UnmarshalJSON refuses unknown fields, including classifications, in a draft.
func (d *Draft) UnmarshalJSON(raw []byte) error {
	type plain Draft
	var v plain
	if err := strictjson.Decode(raw, &v, strictjson.Options{MaxBytes: maxJSONBytes, MaxDepth: 16}); err != nil {
		return fmt.Errorf("decode draft: %w", err)
	}
	*d = Draft(v)
	return nil
}

type Problem struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}

func (p Problem) String() string { return fmt.Sprintf("%s: %s", p.ID, p.Rule) }

// Validate reports every violation within the declared input bounds.
func Validate(drafts []Draft, final Final) []Problem {
	v := validator{raised: map[string][]string{}, claims: map[string][]string{}, counts: map[string]int{}}
	if !v.checkBounds(drafts, final) {
		return v.problems
	}
	v.checkDrafts(drafts)
	v.checkFinal(final)
	sort.SliceStable(v.problems, func(i, j int) bool {
		a, b := v.problems[i], v.problems[j]
		if a.ID == b.ID {
			return a.Rule < b.Rule
		}
		return a.ID < b.ID
	})
	return v.problems
}

type validator struct {
	problems       []Problem
	raised, claims map[string][]string
	counts         map[string]int
}

func (v *validator) add(id, rule string) { v.problems = append(v.problems, Problem{id, rule}) }
func (v *validator) checkBounds(drafts []Draft, f Final) bool {
	if len(drafts) > MaxDrafts {
		v.add("drafts", "draft count exceeds limit")
		return false
	}
	if len(f.Kept) > MaxFindings || len(f.Dropped) > MaxFindings || len(f.Added) > MaxFindings {
		v.add("final", "finding count exceeds limit")
		return false
	}
	for _, d := range drafts {
		if len(d.Findings) > MaxFindings {
			v.add(d.Model, "finding count exceeds limit")
			return false
		}
	}
	return true
}
func (v *validator) checkDrafts(drafts []Draft) {
	for _, d := range drafts {
		if strings.TrimSpace(d.Model) == "" {
			v.add("draft", "model required")
		}
		if d.Kind != "code" && d.Kind != "design" {
			v.add(d.Model, "invalid draft kind")
		}
		seen := map[string]bool{}
		for _, f := range d.Findings {
			if strings.TrimSpace(f.ID) == "" {
				v.add(d.Model, "finding id required")
			}
			if seen[f.ID] {
				v.add(f.ID, "duplicate id in draft")
			}
			seen[f.ID] = true
			v.raised[f.ID] = append(v.raised[f.ID], d.Model)
			v.claims[f.ID] = append(v.claims[f.ID], f.Claim)
		}
	}
}
func (v *validator) checkFinal(f Final) {
	if strings.TrimSpace(f.Combiner) == "" {
		v.add("final", "combiner required")
	}
	switch f.Verdict {
	case "approve", "request_changes", "comment":
	default:
		v.add("final", "invalid verdict")
	}
	for _, k := range f.Kept {
		v.checkKept(k)
	}
	for _, d := range f.Dropped {
		v.checkDropped(d)
	}
	for id := range v.raised {
		if v.counts[id] != 1 {
			v.add(id, "draft finding must occur exactly once in kept or dropped")
		}
	}
	for _, a := range f.Added {
		v.checkAdded(a)
	}
}
func (v *validator) checkKept(k KeptFinding) {
	v.counts[k.ID]++
	if len(k.Sources) > MaxDrafts {
		v.add(k.ID, "source count exceeds limit")
		return
	}
	models, ok := v.raised[k.ID]
	if !ok {
		v.add(k.ID, "kept id not raised in draft")
	}
	for _, m := range models {
		if !contains(k.Sources, m) {
			v.add(k.ID, "missing source "+m)
		}
	}
	for _, s := range k.Sources {
		if !contains(models, s) {
			v.add(k.ID, "source did not raise finding: "+s)
		}
	}
}
func contains(items []string, value string) bool {
	for _, s := range items {
		if s == value {
			return true
		}
	}
	return false
}
func (v *validator) checkDropped(d DroppedClaim) {
	v.counts[d.ID]++
	if _, ok := v.raised[d.ID]; !ok {
		v.add(d.ID, "dropped id not raised in draft")
	}
	if strings.TrimSpace(d.Claim) == "" {
		v.add(d.ID, "dropped claim required")
	}
	for _, claim := range v.claims[d.ID] {
		if d.Claim != claim {
			v.add(d.ID, "dropped claim differs from draft")
		}
	}
	if strings.TrimSpace(d.Reason) == "" {
		v.add(d.ID, "drop reason required")
	}
	if strings.TrimSpace(d.Pointer) == "" {
		v.add(d.ID, "drop pointer required")
	}
}
func (v *validator) checkAdded(f Finding) {
	if !strings.HasPrefix(f.ID, AddedPrefix) || len(f.ID) == len(AddedPrefix) {
		v.add(f.ID, "added id requires added: prefix")
	}
	if _, ok := v.raised[f.ID]; ok {
		v.add(f.ID, "added id already raised")
	}
	v.counts[f.ID]++
	if v.counts[f.ID] > 1 {
		v.add(f.ID, "added id must be unique")
	}
}
