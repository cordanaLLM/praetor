package hiss

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scanFixtureFile writes one source file under a fresh root and scans it.
func scanFixtureFile(t *testing.T, name, body string) *ScanReport {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return rep
}

// TestNonGoRulesSurviveSpacing is the positive dimension and the regression this exists
// for. The non-Go rules matched exact strings, so a single space silenced them and a
// reformatter could erase a finding without changing behaviour. Every construct below was
// reported zero times before; each must now be reported.
func TestNonGoRulesSurviveSpacing(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
		rule string
	}{
		{"native while spaced", "a.c", "void f(void) {\n\twhile ( 1 ) { }\n}\n", "HISS-02"},
		{"native for spaced", "a.c", "void f(void) {\n\tfor ( ;; ) { }\n}\n", "HISS-02"},
		{"native while true", "a.c", "void f(void) {\n\twhile( true ) { }\n}\n", "HISS-02"},
		{"native banned call spaced", "a.c", "void f(char *b) {\n\tgets (b);\n}\n", "HISS-08"},
		{"rust loop no space", "a.rs", "fn f() {\n\tloop{\n\t}\n}\n", "HISS-02"},
		{"rust labelled loop", "a.rs", "fn f() {\n\t'outer: loop {\n\t}\n}\n", "HISS-02"},
		{"rust while true", "a.rs", "fn f() {\n\twhile true {\n\t}\n}\n", "HISS-02"},
		{"rust unwrap spaced", "a.rs", "fn f(x: Option<i32>) -> i32 {\n\tx. unwrap()\n}\n", "HISS-07"},
		{"python while true spaced", "a.py", "def f():\n    while True :\n        pass\n", "HISS-02"},
		{"python bare except spaced", "a.py", "def f():\n    try:\n        pass\n    except :\n        pass\n", "HISS-07"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := scanFixtureFile(t, tc.file, tc.body)
			if rep.Breakdown[tc.rule] == 0 {
				t.Errorf("%s must be reported despite spacing, got %+v", tc.rule, rep.Violations)
			}
		})
	}
}

// TestNonGoRulesDoNotOvermatch is the negative dimension. Tolerating whitespace must not
// turn the patterns into substring matchers that fire on ordinary code, or the rules become
// noise and get suppressed wholesale.
func TestNonGoRulesDoNotOvermatch(t *testing.T) {
	cases := []struct {
		name string
		file string
		body string
	}{
		{"bounded native while", "a.c", "void f(int n) {\n\twhile (n > 0) { n--; }\n}\n"},
		{"bounded native for", "a.c", "void f(void) {\n\tfor (int i = 0; i < 10; i++) { }\n}\n"},
		{"identifier ending in loop", "a.rs", "fn f() {\n\tlet myloop = 1;\n\tlet _ = myloop;\n}\n"},
		{"identifier ending in unsafe", "a.rs", "fn f() {\n\tlet notunsafe = 1;\n\tlet _ = notunsafe;\n}\n"},
		{"fgets is not gets", "a.c", "void f(char *b) {\n\tfgets(b, 10, 0);\n}\n"},
		{"bounded rust while", "a.rs", "fn f(mut n: i32) {\n\twhile n > 0 {\n\t\tn -= 1;\n\t}\n}\n"},
		{"qualified except", "a.py", "def f():\n    try:\n        pass\n    except ValueError:\n        pass\n"},
		{"while true in a string", "a.py", "def f():\n    s = \"while True :\"\n    return s\n"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rep := scanFixtureFile(t, tc.file, tc.body)
			if rep.TotalInfractions != 0 {
				t.Errorf("legitimate code must not be reported, got %+v", rep.Violations)
			}
		})
	}
}

// TestRustUnsafeBraceSpacing is the boundary dimension: HISS-09 requires a SAFETY proof for
// an unsafe block, and the block opener is where the rule attaches. A space before the brace
// used to detach it, so the proof requirement silently disappeared.
func TestRustUnsafeBraceSpacing(t *testing.T) {
	unproven := scanFixtureFile(t, "a.rs", "fn f() {\n\tunsafe  {\n\t\tlet _ = 1;\n\t}\n}\n")
	if unproven.Breakdown["HISS-09"] == 0 {
		t.Errorf("an unsafe block without a SAFETY proof must be reported: %+v", unproven.Violations)
	}

	proven := scanFixtureFile(t, "a.rs", "fn f() {\n\t// SAFETY: bounded by construction.\n\tunsafe  {\n\t\tlet _ = 1;\n\t}\n}\n")
	if proven.Breakdown["HISS-09"] != 0 {
		t.Errorf("a proven unsafe block must not be reported: %+v", proven.Violations)
	}
}
