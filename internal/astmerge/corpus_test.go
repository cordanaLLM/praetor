package astmerge

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// probeHeader is what the "@header" line of testdata/probe_corpus.txt stands for.
const probeHeader = "package p\n\nvar log []string\n\nfunc rec(s string) int { log = append(log, s); return len(log) }\n"

// maxCorpusLines bounds the corpus parse (HISS-02).
const maxCorpusLines = 200000

// corpusCase is one recorded probe case: three inputs and, when the two sides' edits
// commute, the semantics (see factsSummary) the merged file must have.
type corpusCase struct {
	name               string
	single             bool
	model              string
	from               string
	base, ours, theirs string
}

// loadCorpus reads testdata/probe_corpus.txt.
func loadCorpus(t testing.TB) []corpusCase {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "probe_corpus.txt"))
	if err != nil {
		t.Fatalf("reading the probe corpus: %v", err)
	}
	cases, err := parseCorpus(string(data))
	if err != nil {
		t.Fatalf("parsing the probe corpus: %v", err)
	}
	return cases
}

// parseCorpus splits the corpus into cases: a "=== <fields>" line opens a case, a
// "--- <section>" line opens one of its sections, and "#" lines outside a case are notes.
func parseCorpus(data string) ([]corpusCase, error) {
	lines := strings.Split(data, "\n")
	if len(lines) > maxCorpusLines {
		return nil, fmt.Errorf("corpus has %d lines, over the %d bound", len(lines), maxCorpusLines)
	}
	var cases []corpusCase
	var current *corpusCase
	var section *string
	for i, line := range lines {
		switch {
		case strings.HasPrefix(line, "=== "):
			cases = append(cases, newCorpusCase(line[4:]))
			current, section = &cases[len(cases)-1], nil
		case current != nil && strings.HasPrefix(line, "--- "):
			section = current.section(line[4:])
			if section == nil {
				return nil, fmt.Errorf("line %d: unknown section %q", i+1, line)
			}
		case section != nil:
			*section += expandProbeLine(line)
		case line != "" && !strings.HasPrefix(line, "#"):
			return nil, fmt.Errorf("line %d: text outside a section: %q", i+1, line)
		}
	}
	return cases, nil
}

func newCorpusCase(fields string) corpusCase {
	c := corpusCase{name: strings.ReplaceAll(fields, " ", "/")}
	for _, field := range strings.Fields(fields) {
		key, value, _ := strings.Cut(field, "=")
		switch key {
		case "sides":
			c.single = value == "single"
		case "from":
			c.from = value
		}
	}
	return c
}

// section returns the text a section header fills, nil for an unknown one.
func (c *corpusCase) section(name string) *string {
	switch name {
	case "base":
		return &c.base
	case "ours":
		return &c.ours
	case "theirs":
		return &c.theirs
	case "model":
		return &c.model
	}
	return nil
}

func expandProbeLine(line string) string {
	if line == "@header" {
		return probeHeader
	}
	return line + "\n"
}

// factsSummary renders what the probe compares: every constant's value and type, sorted by
// name, then the variable initialization order.
func factsSummary(facts semanticFacts) string {
	names := make([]string, 0, len(facts.consts))
	for name := range facts.consts {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, 0, len(names))
	for _, name := range names {
		parts = append(parts, name+"="+facts.consts[name])
	}
	return strings.Join(parts, " ") + " | init " + strings.Join(facts.init, ",")
}

// oracleViolations is the adversarial probe's oracle, ported as it was written against the
// first per-spec attempt: a constant base, ours and theirs all hold at one value, or that
// both sides changed to one value, keeps it; a pair of initializers base and both sides
// run in one order keeps it. It is deliberately weaker than the guard and written apart
// from it, so a clean merge that satisfies the guard is checked by a second reading.
func oracleViolations(base, ours, theirs, merged semanticFacts) []string {
	out := constsKeptOracle(base, ours, theirs, merged)
	out = append(out, constsAgreedOracle(base, ours, theirs, merged)...)
	return append(out, initOracle(base, ours, theirs, merged)...)
}

// constsKeptOracle reports each constant base and both sides hold at one value whose merged
// value differs.
func constsKeptOracle(base, ours, theirs, merged semanticFacts) []string {
	var out []string
	for name, vb := range base.consts {
		vo, vt, vm := ours.consts[name], theirs.consts[name], merged.consts[name]
		if vo == vb && vt == vb && vm != vb {
			out = append(out, fmt.Sprintf("const %s: base=ours=theirs=%s merged=%s", name, vb, vm))
		}
	}
	return out
}

// constsAgreedOracle reports each constant both sides changed to one value that the merged
// file lacks or holds at another.
func constsAgreedOracle(base, ours, theirs, merged semanticFacts) []string {
	var out []string
	for name, vo := range ours.consts {
		vt, inT := theirs.consts[name]
		vm, inM := merged.consts[name]
		if inT && vt == vo && vo != base.consts[name] && (!inM || vm != vo) {
			out = append(out, fmt.Sprintf("const %s: ours=theirs=%s merged=%s", name, vo, vm))
		}
	}
	return out
}

// initOracle reports each pair of initializers base, ours and theirs run in one order that
// the merged file runs in the other.
func initOracle(base, ours, theirs, merged semanticFacts) []string {
	o, t, m := positions(ours.init), positions(theirs.init), positions(merged.init)
	var out []string
	for i, x := range base.init {
		for _, y := range base.init[i+1:] {
			if !heldByEvery(x, y, o, t, m) {
				continue
			}
			if o[x] < o[y] && t[x] < t[y] && m[x] > m[y] {
				out = append(out, fmt.Sprintf("init %s before %s: base, ours and theirs agree, merged flips", x, y))
			}
		}
	}
	return out
}

func positions(keys []string) map[string]int {
	out := make(map[string]int, len(keys))
	for i, key := range keys {
		out[key] = i
	}
	return out
}

func heldByEvery(x, y string, inputs ...map[string]int) bool {
	for _, pos := range inputs {
		_, hasX := pos[x]
		_, hasY := pos[y]
		if !hasX || !hasY {
			return false
		}
	}
	return true
}

// requireSoundCleanMerge fails when a clean merged file does not type-check although both
// sides do, or breaks the probe's oracle.
func requireSoundCleanMerge(t *testing.T, base, ours, theirs, merged string) semanticFacts {
	t.Helper()
	oursFacts, theirsFacts, got := collectFacts(ours), collectFacts(theirs), collectFacts(merged)
	if len(oursFacts.errs) == 0 && len(theirsFacts.errs) == 0 && len(got.errs) > 0 {
		t.Fatalf("both sides type-check but the clean merge does not: %v\n%s", got.errs, merged)
	}
	if v := oracleViolations(collectFacts(base), oursFacts, theirsFacts, got); len(v) > 0 {
		t.Fatalf("the clean merge breaks the probe oracle: %v\n%s", v, merged)
	}
	return got
}

// TestMerge_Corpus_RecordedProbeCases replays every case the adversarial probe recorded
// against origin/main and the first per-spec attempt: compile errors, wrong values, flipped
// initialization order and conflicts. Each must now be a conflict, or a clean merge that
// type-checks, keeps the probe's oracle and, where the sides' edits commute, has the one
// result either order of the edits produces. A case where only ours edits (sides=single)
// must merge clean.
func TestMerge_Corpus_RecordedProbeCases(t *testing.T) {
	cases := loadCorpus(t)
	if len(cases) < 400 {
		t.Fatalf("expected the recorded corpus of over 400 cases, parsed %d", len(cases))
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { checkCorpusCase(t, c) })
	}
}

// checkCorpusCase merges one recorded case and checks the outcome.
func checkCorpusCase(t *testing.T, c corpusCase) {
	t.Helper()
	res, err := Merge(c.base, c.ours, c.theirs)
	if err != nil {
		t.Fatalf("Merge failed on valid inputs: %v", err)
	}
	if !res.Clean {
		if c.single {
			t.Fatalf("a one-sided edit must merge clean, got %+v", res.Conflicts)
		}
		return
	}
	got := requireSoundCleanMerge(t, c.base, c.ours, c.theirs, res.MergedCode)
	if want := strings.TrimSpace(c.model); want != "" && strings.TrimSpace(factsSummary(got)) != want {
		t.Fatalf("the sides' edits commute to\n  %s\nbut the clean merge has\n  %s\n%s", want, factsSummary(got), res.MergedCode)
	}
}

// TestParseCorpus_Negative_MalformedInput covers the parser's rejections.
func TestParseCorpus_Negative_MalformedInput(t *testing.T) {
	for name, data := range map[string]string{
		"unknown section":      "=== seed=1\n--- merged\npackage p\n",
		"text outside a case":  "package p\n",
		"too many lines":       strings.Repeat("\n", maxCorpusLines),
		"text before sections": "=== seed=1\npackage p\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseCorpus(data); err == nil {
				t.Fatalf("expected a parse error")
			}
		})
	}
}

// TestParseCorpus_Boundary_HeaderAndEmptyCorpus covers the header expansion, a case with
// no model section, and an empty corpus.
func TestParseCorpus_Boundary_HeaderAndEmptyCorpus(t *testing.T) {
	cases, err := parseCorpus("# note\n=== seed=7 sides=single model=ambiguous from=x\n--- base\n@header\n--- ours\n@header\nfunc F() {}\n--- theirs\n@header\n")
	if err != nil || len(cases) != 1 {
		t.Fatalf("expected one case, got %d, %v", len(cases), err)
	}
	c := cases[0]
	if c.base != probeHeader || c.ours != probeHeader+"func F() {}\n" || !c.single || c.model != "" || c.from != "x" {
		t.Errorf("unexpected case: %+v", c)
	}
	if empty, err := parseCorpus(""); err != nil || len(empty) != 0 {
		t.Errorf("an empty corpus must parse to no cases, got %d, %v", len(empty), err)
	}
}
