// Package reviewmerge validates the trace from ungraded reviews to a combined review.
package reviewmerge

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

const (
	// MaxDrafts limits the number of review drafts.
	MaxDrafts = 64
	// MaxFindings limits findings and merged IDs per entry.
	MaxFindings = 256
	// AddedPrefix distinguishes findings introduced by the combiner.
	AddedPrefix = "added:"
	// maxJSONBytes limits the size of a draft document.
	maxJSONBytes = 4 << 20
)

// Finding records a review claim and its supporting evidence.
type Finding struct {
	ID       string `json:"id"`
	Claim    string `json:"claim"`
	Pointer  string `json:"pointer"`
	Evidence string `json:"evidence"`
}

// Draft holds one model’s ungraded review.
type Draft struct {
	Model    string    `json:"model"`
	Kind     string    `json:"kind"`
	Findings []Finding `json:"findings"`
}

// KeptFinding records a retained finding and its attribution.
type KeptFinding struct {
	ID        string   `json:"id"`
	Claim     string   `json:"claim"`
	Pointer   string   `json:"pointer"`
	Evidence  string   `json:"evidence"`
	Sources   []string `json:"sources"`
	MergedIDs []string `json:"merged_ids,omitempty"`
}

// DroppedClaim records a rejected draft claim and its rationale.
type DroppedClaim struct {
	ID      string `json:"id"`
	Claim   string `json:"claim"`
	Reason  string `json:"reason"`
	Pointer string `json:"pointer"`
}

// Final holds the combiner’s review and disposition of draft findings.
type Final struct {
	Combiner string         `json:"combiner"`
	Verdict  string         `json:"verdict"`
	Kept     []KeptFinding  `json:"kept"`
	Added    []Finding      `json:"added"`
	Dropped  []DroppedClaim `json:"dropped"`
}

// Problem describes a contract violation.
type Problem struct {
	ID   string `json:"id"`
	Rule string `json:"rule"`
}

// String escapes untrusted fields so each diagnostic stays on one terminal-safe line.
func (p Problem) String() string {
	id, rule := strconv.Quote(p.ID), strconv.Quote(p.Rule)
	return fmt.Sprintf("%s: %s", id[1:len(id)-1], rule[1:len(rule)-1])
}

// Validate reports every violation within the declared input bounds.
func Validate(drafts []Draft, final Final) []Problem {
	v := newValidator()
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

// originKey identifies a finding within its draft, even when models or IDs repeat.
type originKey struct {
	draft int
	id    string
}

// origin retains the original substance and model for a draft-scoped finding.
type origin struct {
	key     originKey
	model   string
	finding Finding
}

// validator tracks bounded draft occurrences separately from combiner-added IDs.
type validator struct {
	problems []Problem
	raised   map[string][]origin
	counts   map[originKey]int
	added    map[string]bool
}

// newValidator creates independent indexes for one bounded validation pass.
func newValidator() validator {
	return validator{raised: map[string][]origin{}, counts: map[originKey]int{}, added: map[string]bool{}}
}

// add records a contract violation.
func (v *validator) add(id, rule string) { v.problems = append(v.problems, Problem{id, rule}) }

// checkBounds rejects collections before any indexing or nested validation.
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
	return v.checkKeptBounds(f.Kept)
}

// checkKeptBounds bounds the secondary lists before walking them.
func (v *validator) checkKeptBounds(kept []KeptFinding) bool {
	for _, k := range kept {
		if len(k.MergedIDs) > MaxFindings {
			v.add(k.ID, "merged id count exceeds limit")
			return false
		}
		if len(k.Sources) > MaxDrafts {
			v.add(k.ID, "source count exceeds limit")
			return false
		}
	}
	return true
}

// checkDrafts validates each draft before indexing its findings by draft occurrence.
func (v *validator) checkDrafts(drafts []Draft) {
	for i, d := range drafts {
		if strings.TrimSpace(d.Model) == "" {
			v.add("draft", "model required")
		}
		if d.Kind != "code" && d.Kind != "design" {
			v.add(d.Model, "invalid draft kind")
		}
		if d.Findings == nil {
			v.add(d.Model, "findings array required")
		}
		v.indexDraft(i, d)
	}
}

// indexDraft retains each bounded finding without collapsing equal IDs across drafts.
func (v *validator) indexDraft(index int, d Draft) {
	seen := map[string]bool{}
	for _, f := range d.Findings {
		v.checkFinding(f)
		if seen[f.ID] {
			v.add(f.ID, "duplicate id in draft")
			continue
		}
		seen[f.ID] = true
		o := origin{key: originKey{draft: index, id: f.ID}, model: d.Model, finding: f}
		v.raised[f.ID] = append(v.raised[f.ID], o)
	}
}

// checkFinding requires complete, actionable content for every finding category.
func (v *validator) checkFinding(f Finding) {
	if strings.TrimSpace(f.ID) == "" {
		v.add(f.ID, "finding id required")
	}
	if strings.TrimSpace(f.Claim) == "" {
		v.add(f.ID, "claim required")
	}
	if strings.TrimSpace(f.Evidence) == "" {
		v.add(f.ID, "evidence required")
	}
	v.checkPointer(f.ID, f.Pointer)
}

// checkPointer requires a path and positive decimal line number without terminal controls.
func (v *validator) checkPointer(id, pointer string) {
	if strings.TrimSpace(pointer) == "" {
		v.add(id, "pointer required")
		return
	}
	if !validPointer(pointer) {
		v.add(id, "pointer must be path:line")
	}
}

// validPointer accepts portable paths, including Windows drive paths, and bounded line numbers.
func validPointer(pointer string) bool {
	colon := strings.LastIndexByte(pointer, ':')
	if colon <= 0 || strings.IndexFunc(pointer, unicode.IsControl) >= 0 {
		return false
	}
	path, line := pointer[:colon], pointer[colon+1:]
	if strings.Contains(path, ":") && !windowsPointerPath(path) {
		return false
	}
	n, err := strconv.Atoi(line)
	return err == nil && n > 0 && strconv.Itoa(n) == line && strings.TrimSpace(path) != ""
}

// windowsPointerPath recognizes a single drive prefix rather than arbitrary extra colons.
func windowsPointerPath(path string) bool {
	return len(path) > 3 && path[1] == ':' && strings.ContainsAny(path[2:3], `/\`) &&
		strings.Count(path, ":") == 1 && strings.ContainsRune("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ", rune(path[0]))
}
