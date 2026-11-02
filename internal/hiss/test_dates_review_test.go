// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFixtureDatesPytestPaths pins fixture dates pytest paths.
func TestFixtureDatesPytestPaths(t *testing.T) {
	for _, name := range []string{"conftest.py", "tests/shared.py", "test/shared.py", "nested/tests/shared.py", "test_date.py", "date_selftest.py", "selftest.py", "src/shared.py"} {
		t.Run(name, func(t *testing.T) {
			rep := newScanReport(MaxInfractionsCap)
			src := sourceFile{rel: name, data: []byte( /* testdate: fact: scanner counterexample; never compared with a clock */ `expiry = "2030-01-01"`)}
			scanPythonFixtureDates(src, rep)
			if got := rep.Breakdown["HISS-15"] > 0; got != (name != "src/shared.py") {
				t.Fatalf("date fixture scan %s: %+v", name, rep.Violations)
			}
		})
	}
}

// TestFixtureDatesAnnotationCRLF pins fixture dates annotation CRLF.
func TestFixtureDatesAnnotationCRLF(t *testing.T) {
	for _, ending := range []string{"\n", "\r\n"} {
		t.Run(ending, func(t *testing.T) {
			body := /* testdate: fact: annotation counterexample; scanner input only */ "# testdate: fact: release parser input\nrelease = \"2030-01-01\"\n"
			rep := newScanReport(MaxInfractionsCap)
			scanPythonFixtureDates(sourceFile{rel: "test_date.py", data: []byte(strings.ReplaceAll(body, "\n", ending))}, rep)
			if rep.Breakdown["HISS-15"] != 0 {
				t.Fatalf("standalone annotation rejected: %+v", rep.Violations)
			}
		})
	}
}

// TestFixtureDateCoverageRetainsDimensionMechanisms pins fixture date coverage retains dimension mechanisms.
func TestFixtureDateCoverageRetainsDimensionMechanisms(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", ".config", "hiss", "coverage.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	_, entry, ok := strings.Cut(string(data), "  - id: HISS-15\n")
	if !ok {
		t.Fatal("missing HISS-15 coverage")
	}
	entry, _, _ = strings.Cut(entry, "      - language: python")
	for _, term := range []string{"scanGoFixtureDates", "COVERAGE_FLOOR=65", "forge", "validate-pr"} {
		if !strings.Contains(entry, term) {
			t.Errorf("missing independent mechanism %s", term)
		}
	}
}

// TestFixtureDatesPythonTolerantEscapes pins fixture dates python tolerant escapes.
func TestFixtureDatesPythonTolerantEscapes(t *testing.T) {
	for _, tc := range []struct {
		name, body, wantSubstring string
	}{
		{"positive/named-digits-date", `expiry = "\N{DIGIT TWO}\N{DIGIT ZERO}\N{DIGIT THREE}\N{DIGIT ZERO}-01-01"`, "derive it from the test clock"},
		{"positive/fstring-named-digits", `expiry = f"\N{DIGIT TWO}\N{DIGIT ZERO}\N{DIGIT THREE}\N{DIGIT ZERO}\N{HYPHEN-MINUS}01-01"`, "derive it from the test clock"},
		{
			"positive/surrogate-with-date",
			/* testdate: fact: scanner counterexample; never compared with a clock */
			`expiry = "\ud83d 2030-01-01"`,
			"derive it from the test clock",
		},
		{"positive/unterminated-literal", `expiry = "unterminated`, "test fixture string cannot be decoded"},
		{"negative/named-degree-sign", `unit = "\N{DEGREE SIGN}"`, ""},
		{"negative/lone-surrogate", `raw = "\ud83d"`, ""},
		{"negative/high-octal", `raw = "\400"`, ""},
		{"negative/annotated-named-date", "# testdate: fact: recorded release input\nrelease = \"\\N{DIGIT TWO}\\N{DIGIT ZERO}\\N{DIGIT THREE}\\N{DIGIT ZERO}-01-01\"\n", ""},
		{
			"negative/boundary-preserved-after-named",
			/* testdate: fact: scanner counterexample; never compared with a clock */
			"unit = \"\\N{DEGREE SIGN} '\" # 2030-01-01\n",
			"",
		},
		{
			"boundary/hash-in-named-literal-not-comment",
			/* testdate: fact: scanner counterexample; never compared with a clock */
			"unit = \"\\N{DEGREE SIGN} #\"; expiry = \"2030-01-01\"\n",
			"derive it from the test clock",
		},
		{
			"boundary/annotation-covers-first-only",
			/* testdate: fact: scanner counterexample; never compared with a clock */
			"# testdate: fact: first literal only\nrelease = \"\\N{DEGREE SIGN} 2030-01-01\"; expiry = \"2030-01-02\"\n",
			"derive it from the test clock",
		},
		{"boundary/concat-across-named-escape", `expiry = "\N{DIGIT TWO}\N{DIGIT ZERO}30-" + "01-01"`, "derive it from the test clock"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rep := newScanReport(MaxInfractionsCap)
			scanPythonFixtureDates(sourceFile{rel: "test_date.py", data: []byte(tc.body)}, rep)
			if tc.wantSubstring == "" {
				if len(rep.Violations) != 0 {
					t.Fatalf("unexpected violations: %+v", rep.Violations)
				}
				return
			}
			if len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0].Message, tc.wantSubstring) {
				t.Fatalf("violations = %+v, want 1 containing %q", rep.Violations, tc.wantSubstring)
			}
		})
	}
}

// TestFixtureDatePythonCoverageAndGuideReferences pins fixture date python coverage and guide references.
func TestFixtureDatePythonCoverageAndGuideReferences(t *testing.T) {
	for _, tc := range []struct {
		name, coverage, guide string
		want                  bool
	}{
		{
			"positive/live-references",
			"mechanism: internal/hiss/test_dates_python.go, scanPythonFixtureDates; standardsctl audit in local hooks.",
			"runs HISS-15's clock fixture check in\n`internal/hiss/test_dates.go` (Go) and `internal/hiss/test_dates_python.go`\n(Python).",
			true,
		},
		{
			"negative/coverage-points-to-go-file",
			"mechanism: internal/hiss/test_dates.go, scanPythonFixtureDates; standardsctl audit in local hooks.",
			"runs HISS-15's clock fixture check in\n`internal/hiss/test_dates.go` (Go) and `internal/hiss/test_dates_python.go`\n(Python).",
			false,
		},
		{
			"negative/guide-omits-python-file",
			"mechanism: internal/hiss/test_dates_python.go, scanPythonFixtureDates; standardsctl audit in local hooks.",
			"runs HISS-15's clock fixture check in\n`internal/hiss/test_dates.go`. Python-only source changes also select that audit,",
			false,
		},
		{
			"boundary/go-mechanism-referenced",
			"mechanism: internal/hiss/test_dates.go, scanGoFixtureDates; standardsctl audit in local hooks.",
			"runs HISS-15's clock fixture check in\n`internal/hiss/test_dates.go` (Go) and `internal/hiss/test_dates_python.go`\n(Python).",
			false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := validPythonDateReferences(tc.coverage, tc.guide); got != tc.want {
				t.Fatalf("validPythonDateReferences = %v, want %v", got, tc.want)
			}
		})
	}

	covData, err := os.ReadFile(filepath.Join("..", "..", ".config", "hiss", "coverage.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	guideData, err := os.ReadFile(filepath.Join("..", "..", "docs", "guides", "git-hooks.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !validPythonDateReferences(string(covData), string(guideData)) {
		t.Fatal("coverage.yaml or git-hooks.md does not locate Python fixture date scanner in internal/hiss/test_dates_python.go")
	}
}

// validPythonDateReferences returns fixture date reference strings permitted in tests.
func validPythonDateReferences(coverage, guide string) bool {
	hasCoverage := strings.Contains(coverage, "internal/hiss/test_dates_python.go, scanPythonFixtureDates")
	hasGuide := strings.Contains(guide, "internal/hiss/test_dates_python.go")
	return hasCoverage && hasGuide
}
