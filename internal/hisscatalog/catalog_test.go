// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// catalogSize is the number of invariants the standard defines, HISS-01 through HISS-21.
const catalogSize = 21

// TestCatalog_Positive_ContiguousAndComplete pins the registry the MCP tool and the wiki
// matrix share: every ID from HISS-01 to HISS-21 exactly once, in ascending order, with
// every field filled.
func TestCatalog_Positive_ContiguousAndComplete(t *testing.T) {
	rules := Rules()
	if len(rules) != catalogSize {
		t.Fatalf("catalog has %d rules, want %d", len(rules), catalogSize)
	}
	for i, rule := range rules {
		if want := fmt.Sprintf("HISS-%02d", i+1); rule.ID != want {
			t.Errorf("rule %d is %s, want %s", i, rule.ID, want)
		}
		if rule.Title == "" || rule.Specification == "" || rule.Enforcement == "" || rule.FailureAction == "" {
			t.Errorf("%s has an empty field: %+v", rule.ID, rule)
		}
		if got, ok := LookupRule(rule.ID); !ok || got != rule {
			t.Errorf("LookupRule(%s) = %+v, %v", rule.ID, got, ok)
		}
	}
	if ids := RuleIDs(); len(ids) != catalogSize || ids[0] != "HISS-01" || ids[catalogSize-1] != "HISS-21" {
		t.Errorf("RuleIDs() = %v", ids)
	}
}

// TestCatalog_Negative_CopyAndUnknownLookup proves a caller cannot edit the registry through
// Rules's result, and that lookup is exact.
func TestCatalog_Negative_CopyAndUnknownLookup(t *testing.T) {
	rules := Rules()
	rules[0].Title = "tampered"
	if got, _ := LookupRule("HISS-01"); got.Title == "tampered" {
		t.Error("editing Rules()'s result changed the registry")
	}
	for _, id := range []string{"", "HISS-00", "HISS-22", "hiss-01", " HISS-01", "HISS-1"} {
		if _, ok := LookupRule(id); ok {
			t.Errorf("LookupRule(%q) found a rule", id)
		}
	}
}

// TestRuleExplanation_Boundary_ListSpecification pins both specification shapes: prose
// follows the label on its line, a list opens on the next line with no trailing space.
func TestRuleExplanation_Boundary_ListSpecification(t *testing.T) {
	prose, _ := LookupRule("HISS-12")
	if want := "Rule: HISS-12 (Secret Leak Prevention)\nFormal Specification: Zero credentials in Git history.\n" +
		"Enforcement: 'make secrets' runs gitleaks over repository history inside verify-all.\n" +
		"Failure Action: Verification gate rejection."; prose.Explanation() != want {
		t.Errorf("HISS-12 explanation = %q, want %q", prose.Explanation(), want)
	}
	list, _ := LookupRule("HISS-04")
	if !strings.Contains(list.Explanation(), "Formal Specification:\n  - McCabe Cyclomatic Complexity <= 10\n") {
		t.Errorf("HISS-04 explanation does not open its list on a new line: %q", list.Explanation())
	}
}

// TestParseGatedInvariants_Positive_CanonicalAgents reads the repository's own AGENTS.md:
// its table must parse, and every row must be a catalog invariant.
func TestParseGatedInvariants_Positive_CanonicalAgents(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseGatedInvariants(string(data))
	if err != nil {
		t.Fatalf("ParseGatedInvariants(AGENTS.md): %v", err)
	}
	if len(rows) == 0 || rows[0].ID != "HISS-01" || rows[0].Scope != "control flow" {
		t.Fatalf("first gated row = %+v", rows)
	}
	first := rows[0]
	if first.Rule != "recursion prohibited; call graph = DAG" || first.Enforcement != "build" || first.OnFail != "immediate build failure" {
		t.Errorf("HISS-01 cells = %+v", first)
	}
}

// gatedFixture wraps table rows in the section AGENTS.md carries them in.
func gatedFixture(rows ...string) string {
	return "# Harness\n\n" + GatedInvariantsHeading + "\n\n" +
		"| Invariant | Rule | Enforcement | On fail |\n| :--- | :--- | :--- | :--- |\n" +
		strings.Join(rows, "\n") + "\n\n## Operational Rules\n\n1. text\n"
}

func TestParseGatedInvariants_Negative_RejectsUnreadableTables(t *testing.T) {
	cases := map[string]struct {
		doc  string
		want error
	}{
		"no section":    {"# Harness\n\n| **HISS-01** x | a | b | c |\n", ErrNoGatedInvariants},
		"empty section": {gatedFixture(), ErrNoGatedInvariants},
		"unknown invariant": {
			gatedFixture("| **HISS-01** control flow | a | b | c |", "| **HISS-99** invented | a | b | c |"),
			ErrUnknownInvariant,
		},
		"duplicate invariant": {
			gatedFixture("| **HISS-01** control flow | a | b | c |", "| **HISS-01** again | a | b | c |"),
			ErrDuplicateInvariant,
		},
		"row only after the section": {
			"# Harness\n\n" + GatedInvariantsHeading + "\n\ntext\n\n## Other\n\n| **HISS-01** x | a | b | c |\n",
			ErrNoGatedInvariants,
		},
	}
	for name, tc := range cases {
		if _, err := ParseGatedInvariants(tc.doc); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// TestParseGatedInvariants_Boundary_SectionEdgesAndCells covers the edges of the grammar: a
// suffixed heading, CRLF line endings, an escaped pipe, a fenced shell comment inside the
// section, and a row after the next heading that must not be read.
func TestParseGatedInvariants_Boundary_SectionEdgesAndCells(t *testing.T) {
	doc := "# Harness\r\n\r\n" + GatedInvariantsHeading + " (Modernized)\r\n\r\n" +
		"| **HISS-02** loops, I/O | a \\| b | Semgrep / AST | error |\r\n" +
		"```bash\r\n# not a heading\r\n```\r\n" +
		"| **HISS-16** context integrity | one source | pre-commit | blocker |\r\n" +
		"\r\n## Operational Rules\r\n| **HISS-17** state ledger | x | y | z |\r\n"
	rows, err := ParseGatedInvariants(doc)
	if err != nil {
		t.Fatalf("ParseGatedInvariants: %v", err)
	}
	if len(rows) != 2 || rows[0].ID != "HISS-02" || rows[1].ID != "HISS-16" {
		t.Fatalf("rows = %+v, want HISS-02 and HISS-16 only", rows)
	}
	if rows[0].Scope != "loops, I/O" || rows[0].Rule != "a | b" || rows[1].OnFail != "blocker" {
		t.Errorf("cells = %+v", rows)
	}

	tooLong := strings.Repeat("\n", maxGatedTableLines)
	if _, err := ParseGatedInvariants(tooLong); !errors.Is(err, ErrGatedTableTooLong) {
		t.Errorf("a document past the line bound: err = %v, want ErrGatedTableTooLong", err)
	}
}
