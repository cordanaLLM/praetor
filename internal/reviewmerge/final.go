package reviewmerge

import "strings"

// checkFinal validates disposition lists and requires exactly one disposition per draft occurrence.
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
	v.checkCoverage()
	for _, a := range f.Added {
		v.checkAdded(a)
	}
}

// checkCoverage checks every indexed occurrence, bounded by MaxDrafts times MaxFindings.
func (v *validator) checkCoverage() {
	for id, origins := range v.raised {
		for _, o := range origins {
			if v.counts[o.key] != 1 {
				v.add(id, "draft finding must occur exactly once in kept or dropped")
			}
		}
	}
}

// checkKept preserves substance and gathers the exact union of contributing models.
func (v *validator) checkKept(k KeptFinding) {
	f := Finding{ID: k.ID, Claim: k.Claim, Pointer: k.Pointer, Evidence: k.Evidence}
	v.checkFinding(f)
	models := map[string]bool{}
	v.coverKept(k.ID, f, "kept", models)
	for _, id := range k.MergedIDs {
		v.coverKept(id, f, "merged", models)
	}
	v.checkSources(k.ID, k.Sources, models)
}

// coverKept accounts only for occurrences whose substance agrees with the retained finding.
func (v *validator) coverKept(id string, kept Finding, kind string, models map[string]bool) {
	origins, ok := v.raised[id]
	if !ok {
		v.add(id, kind+" id not raised in draft")
		return
	}
	matched := false
	for _, o := range origins {
		f := o.finding
		if kept.Claim == f.Claim && kept.Pointer == f.Pointer && kept.Evidence == f.Evidence {
			v.counts[o.key]++
			models[o.model] = true
			matched = true
		}
	}
	if !matched {
		v.add(id, "kept finding differs from draft")
	}
}

// checkSources rejects missing, extraneous and repeated model names in a source union.
func (v *validator) checkSources(id string, sources []string, models map[string]bool) {
	seen := map[string]bool{}
	for _, source := range sources {
		if seen[source] {
			v.add(id, "duplicate source "+source)
		}
		seen[source] = true
		if !models[source] {
			v.add(id, "source did not raise finding: "+source)
		}
	}
	for model := range models {
		if !seen[model] {
			v.add(id, "missing source "+model)
		}
	}
}

// checkDropped requires a rationale and accounts only for matching draft-scoped claims.
func (v *validator) checkDropped(d DroppedClaim) {
	if strings.TrimSpace(d.Claim) == "" {
		v.add(d.ID, "dropped claim required")
	}
	if strings.TrimSpace(d.Reason) == "" {
		v.add(d.ID, "drop reason required")
	}
	v.checkPointer(d.ID, d.Pointer)
	origins, ok := v.raised[d.ID]
	if !ok {
		v.add(d.ID, "dropped id not raised in draft")
		return
	}
	matched := false
	for _, o := range origins {
		if d.Claim == o.finding.Claim && d.Pointer == o.finding.Pointer {
			v.counts[o.key]++
			matched = true
		}
	}
	if !matched {
		v.add(d.ID, "dropped claim differs from draft")
	}
}

// checkAdded checks complete content and an identity reserved for combiner-added findings.
func (v *validator) checkAdded(f Finding) {
	v.checkFinding(f)
	if !strings.HasPrefix(f.ID, AddedPrefix) || len(f.ID) == len(AddedPrefix) {
		v.add(f.ID, "added id requires added: prefix")
	}
	if _, ok := v.raised[f.ID]; ok {
		v.add(f.ID, "added id already raised")
	}
	if v.added[f.ID] {
		v.add(f.ID, "added id must be unique")
	}
	v.added[f.ID] = true
}
