package hiss

import (
	"slices"
	"strings"
	"testing"
)

// scannedKeys scans one file and returns the baseline fingerprint and the line of each finding,
// in scan order.
func scannedKeys(t *testing.T, name, body string) (fingerprints []string, lines []int, anchors []string) {
	t.Helper()
	rep := scanFixtureFile(t, name, body)
	for _, inf := range ConvertToBaseline(rep.Violations) {
		fingerprints = append(fingerprints, inf.Fingerprint)
		lines = append(lines, inf.LineNumber)
		anchors = append(anchors, inf.Anchor)
	}
	return fingerprints, lines, anchors
}

// shiftCases holds, per language scanner that produces findings, a file whose findings sit in
// the named places. head stays the first line (an interpreter line, a YAML document marker) and
// the lines a shift inserts go below it.
var shiftCases = []struct {
	name, file, head, body string
	// anchors are the anchors of the findings, in scan order.
	anchors []string
}{
	{name: "go method and function", file: "a.go", head: "package p\n",
		body:    "type T struct{}\n\nfunc (t *T) Run() {\n\tfor {\n\t}\n}\n\nfunc free() {\n\tfor {\n\t}\n}\n",
		anchors: []string{"fn:T.Run", "fn:free"}},
	{name: "python method and module level", file: "a.py", head: "",
		body:    "class Job:\n    def run(self):\n        while True:\n            pass\n\nwhile True:\n    pass\n",
		anchors: []string{"fn:Job.run", "text:"}},
	{name: "typescript function", file: "a.ts", head: "",
		body:    "export function spin(): void {\n  while (true) { tick(); }\n}\n",
		anchors: []string{"fn:spin"}},
	{name: "shell function", file: "a.sh", head: "#!/usr/bin/env bash\n",
		body:    "set -euo pipefail\nspin() {\n  while true; do\n    :\n  done\n}\n",
		anchors: []string{"fn:spin"}},
	{name: "c function", file: "a.c", head: "",
		body:    "int f(void)\n{\n    while (1) {\n    }\n    return 0;\n}\n",
		anchors: []string{"fn:f"}},
	{name: "rust function", file: "a.rs", head: "",
		body:    "fn spin() {\n    loop {\n    }\n}\n",
		anchors: []string{"fn:spin"}},
	{name: "systemd directive", file: "job.service", head: "",
		body:    "[Service]\nType=oneshot\nExecStart=/usr/bin/job\n",
		anchors: []string{"text:"}},
	{name: "ansible task key", file: "play.yml", head: "---\n",
		body: "- name: Configure hosts\n  hosts: all\n  tasks:\n    - name: Probe\n      ansible.builtin.uri:\n" +
			"        url: https://example.com\n      ignore_errors: true\n",
		anchors: []string{"text:"}},
}

// Positive (#29): in every language that produces findings, a finding keeps its baseline key
// when lines are inserted above it, and its recorded line follows the shift. Before, the key
// held the line, so each of these findings was a new infraction after the insert.
func TestAnchor_KeySurvivesInsertedLines(t *testing.T) {
	const inserted = 3
	for _, tc := range shiftCases {
		t.Run(tc.name, func(t *testing.T) {
			before, linesBefore, anchors := scannedKeys(t, tc.file, tc.head+tc.body)
			after, linesAfter, _ := scannedKeys(t, tc.file, tc.head+strings.Repeat("\n", inserted)+tc.body)
			if len(before) != len(tc.anchors) {
				t.Fatalf("findings %v, want %d anchored %v", before, len(tc.anchors), tc.anchors)
			}
			for i, want := range tc.anchors {
				if !strings.HasPrefix(anchors[i], want) || (!strings.HasSuffix(want, ":") && anchors[i] != want) {
					t.Errorf("finding %d anchor %q, want %q", i, anchors[i], want)
				}
				if linesAfter[i] != linesBefore[i]+inserted {
					t.Errorf("finding %d line %d after the insert, want %d", i, linesAfter[i], linesBefore[i]+inserted)
				}
			}
			if !slices.Equal(before, after) {
				t.Errorf("keys changed with the inserted lines:\n before %v\n after  %v", before, after)
			}
		})
	}
}

// Negative: a renamed function is another function, and a second function with the same
// violation is a second entry; neither is hidden behind the recorded key.
func TestAnchor_RenamedAndNewFunctionsAreNewKeys(t *testing.T) {
	loop := func(name string) string { return "func " + name + "() {\n\tfor {\n\t}\n}\n" }
	recorded, _, _ := scannedKeys(t, "a.go", "package p\n\n"+loop("spin"))

	renamed, _, _ := scannedKeys(t, "a.go", "package p\n\n"+loop("turn"))
	if len(recorded) != 1 || len(renamed) != 1 || renamed[0] == recorded[0] {
		t.Fatalf("a renamed function must carry a new key: recorded %v, renamed %v", recorded, renamed)
	}

	added, _, _ := scannedKeys(t, "a.go", "package p\n\n"+loop("fresh")+"\n"+loop("spin"))
	if len(added) != 2 || added[1] != recorded[0] || added[0] == recorded[0] {
		t.Fatalf("a new function above must add a key and leave the recorded one: recorded %v, now %v", recorded, added)
	}
}

// Boundary: two findings of one rule in one function are told apart by their rank, not their
// lines; a renamed file is another file; a finding no function holds is keyed by its line's
// text, which re-indenting keeps and editing changes.
func TestAnchor_Boundaries(t *testing.T) {
	two := "package p\n\nfunc spin() {\n\tfor {\n\t}\n\tfor {\n\t}\n}\n"
	keys, _, _ := scannedKeys(t, "a.go", two)
	want := []string{"a.go:HISS-02:fn:spin#1", "a.go:HISS-02:fn:spin#2"}
	if !slices.Equal(keys, want) {
		t.Fatalf("two findings in one function: keys %v, want %v", keys, want)
	}
	spread, _, _ := scannedKeys(t, "a.go", strings.Replace(two, "\tfor {\n\t}\n\tfor", "\tfor {\n\t}\n\n\n\tfor", 1))
	if !slices.Equal(spread, want) {
		t.Fatalf("lines inserted between the two findings changed a key: %v", spread)
	}

	moved, _, _ := scannedKeys(t, "b.go", two)
	if moved[0] == keys[0] || !strings.HasPrefix(moved[0], "b.go:") {
		t.Fatalf("a renamed file must carry new keys naming it: %v", moved)
	}

	level, _, _ := scannedKeys(t, "a.py", "while True:\n    pass\n")
	indented, _, _ := scannedKeys(t, "a.py", "if ready:\n    while   True:\n        pass\n")
	edited, _, _ := scannedKeys(t, "a.py", "while True:  # forever\n    pass\n")
	if len(level) != 1 || !slices.Equal(level, indented) {
		t.Fatalf("a re-indented line must keep its text anchor: %v, %v", level, indented)
	}
	if slices.Equal(level, edited) {
		t.Fatalf("an edited line must change its text anchor: %v", edited)
	}
}

// The anchor helpers, apart from any scanner. Positive: the innermost function holds the line.
// Negative: a finding that names its function is not taken by a function nested on that line.
// Boundary: no function, a line the file does not hold, an unnamed function, the note bound.
func TestAnchor_Helpers_3D(t *testing.T) {
	functions := []funcSpan{{name: "outer", start: 1, end: 20}, {name: "T.inner", start: 1, end: 5}, {name: "late", start: 30, end: 40}}
	lines := []string{"  while True:  ", "\twhile True:\r"}
	for _, tc := range []struct {
		line   int
		symbol string
		want   string
	}{
		{3, "", "fn:T.inner"},
		{10, "", "fn:outer"},
		{1, "outer", "fn:outer"},
		{2, "inner", "fn:T.inner"},
		{35, "gone", "fn:gone"},
		{50, "", "file"},
		{0, "", "file"},
	} {
		if got := findingAnchor(functions, lines, tc.line, tc.symbol); got != tc.want {
			t.Errorf("line %d symbol %q: anchor %q, want %q", tc.line, tc.symbol, got, tc.want)
		}
	}
	first, second := findingAnchor(nil, lines, 1, ""), findingAnchor(nil, lines, 2, "")
	if !strings.HasPrefix(first, "text:") || first != second {
		t.Errorf("lines differing in whitespace and line ending must share a text anchor: %q, %q", first, second)
	}

	if got := appendFunction(nil, "", 1, 2); len(got) != 0 {
		t.Errorf("an unnamed function must not be noted: %v", got)
	}
	full := make([]funcSpan, maxNotedFunctions)
	if got := appendFunction(full, "f", 1, 2); len(got) != maxNotedFunctions {
		t.Errorf("the note bound must hold: %d functions", len(got))
	}
	if qualifiedName("", "f") != "f" || qualifiedName("T", "f") != "T.f" || qualifiedName("T", "") != "" {
		t.Error("qualifiedName must join an owner and a name, and leave either alone without the other")
	}
}

// A finding a package pass records after every file is read names its function, and is
// anchored to it; one that names none keeps no anchor and so its line key.
func TestAnchor_PackagePassFindingsAreAnchoredBySymbol(t *testing.T) {
	found := scanSources(t, map[string]string{
		"a.go": "package p\n\nfunc a() { b() }\n",
		"b.go": "package p\n\nfunc b() { a() }\n",
	})
	if len(found) != 1 || found[0].Anchor != "fn:"+found[0].Symbol || found[0].Symbol == "" {
		t.Fatalf("a call cycle must be anchored to the function it names: %+v", found)
	}

	unnamed := []InvariantViolation{{RuleID: "HISS-02", FilePath: "x.go", LineNumber: 7}}
	anchorBySymbol(unnamed)
	if key := ConvertToBaseline(unnamed)[0].Fingerprint; unnamed[0].Anchor != "" || key != "x.go:7:HISS-02" {
		t.Fatalf("a finding without anchor or symbol must keep the line key, got anchor %q key %q", unnamed[0].Anchor, key)
	}
}
