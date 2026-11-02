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
