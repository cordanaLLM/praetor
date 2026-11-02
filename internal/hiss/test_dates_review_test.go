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
