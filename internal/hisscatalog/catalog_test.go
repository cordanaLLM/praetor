// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
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
		if got, ok := LookupRule(rule.ID); !ok || !reflect.DeepEqual(got, rule) {
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
	rules[0].Directive[0].Text = "tampered"
	looked, _ := LookupRule("HISS-02")
	looked.Directive[0].Text = "tampered"
	if got, _ := LookupRule("HISS-01"); got.Title == "tampered" || got.Directive[0].Text == "tampered" {
		t.Error("editing Rules()'s result changed the registry")
	}
	if got, _ := LookupRule("HISS-02"); got.Directive[0].Text == "tampered" {
		t.Error("editing LookupRule's result changed the registry")
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

// specFuncLength matches the function-length line of a rule's formal specification.
var specFuncLength = regexp.MustCompile(`Function Length <= (\d+) LOC`)

// hiss04FuncLengths returns every function length HISS-04's explanation states.
func hiss04FuncLengths(t *testing.T) []string {
	t.Helper()
	rule, ok := LookupRule("HISS-04")
	if !ok {
		t.Fatal("HISS-04 is not in the catalog")
	}
	var lengths []string
	for _, match := range specFuncLength.FindAllStringSubmatch(rule.Explanation(), catalogSize) {
		lengths = append(lengths, match[1])
	}
	return lengths
}

// Positive: standards_explain_rule states the function length the audit enforces
// (config.AuditMaxFuncLOC), not a copy of it (#574).
func TestRuleExplanation_Positive_HISS04StatesTheAuditFuncLOC(t *testing.T) {
	want := strconv.Itoa(config.AuditMaxFuncLOC)
	if got := hiss04FuncLengths(t); len(got) != 1 || got[0] != want {
		t.Fatalf("HISS-04 states function lengths %v, want [%s]", got, want)
	}
}

// Negative: the 75-line copy the specification used to carry is gone, and the cyclomatic cap
// on the line above is not read as a function length.
func TestRuleExplanation_Negative_HISS04DropsTheStaleFuncLOC(t *testing.T) {
	rule, _ := LookupRule("HISS-04")
	if strings.Contains(rule.Explanation(), "<= 75 LOC") {
		t.Fatalf("HISS-04 still states the stale 75-line length: %q", rule.Explanation())
	}
	if got := specFuncLength.FindAllString("McCabe Cyclomatic Complexity <= 10", -1); len(got) != 0 {
		t.Fatalf("a cyclomatic cap read as a function length: %v", got)
	}
}

// Boundary: the function length sits between the cognitive and the statement caps, as the
// fourth line of the list, so deriving it did not reorder or drop the neighbouring caps.
func TestRuleExplanation_Boundary_HISS04ListKeepsItsShape(t *testing.T) {
	rule, _ := LookupRule("HISS-04")
	want := "\n  - McCabe Cyclomatic Complexity <= 10\n  - Cognitive Complexity <= 15\n  - Function Length <= " +
		strconv.Itoa(config.AuditMaxFuncLOC) + " LOC\n  - Executable Statements <= 50"
	if rule.Specification != want {
		t.Fatalf("HISS-04 specification = %q, want %q", rule.Specification, want)
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

// TestSplitInvariantRow_Positive_AnyBoldID: the splitter reads a catalog row and a
// repository's own row alike, with the line kept as written and the cells split.
func TestSplitInvariantRow_Positive_AnyBoldID(t *testing.T) {
	for line, want := range map[string]InvariantRow{
		"| **HISS-01** control flow | recursion prohibited | build | immediate build failure |": {
			ID: "HISS-01", Cells: []string{"control flow", "recursion prohibited", "build", "immediate build failure"},
		},
		"  | **ACME-01** secrets | never log tokens | review |  ": {
			ID: "ACME-01", Cells: []string{"secrets", "never log tokens", "review"},
		},
	} {
		got, ok := SplitInvariantRow(line)
		want.Line = strings.TrimSpace(line)
		if !ok || !reflect.DeepEqual(got, want) {
			t.Errorf("SplitInvariantRow(%q) = %+v, %v; want %+v", line, got, ok, want)
		}
	}
}

// TestSplitInvariantRow_Negative_NotARow: the header, the delimiter row, prose, a row without
// a bold ID and an unterminated row are no invariant rows.
func TestSplitInvariantRow_Negative_NotARow(t *testing.T) {
	for _, line := range []string{
		"| Invariant | Rule | Adopted check | On fail |",
		"| :--- | :--- | :--- | :--- |",
		"old table",
		"",
		"|",
		"| HISS-01 control flow | a | b | c |",
		"| **HISS-01** control flow | a | b | c",
		"| **HISS 01** control flow | a | b | c |",
	} {
		if row, ok := SplitInvariantRow(line); ok {
			t.Errorf("SplitInvariantRow(%q) = %+v, want no row", line, row)
		}
	}
}

// TestSplitInvariantRow_Boundary_EscapedPipes: an escaped pipe stays inside its cell and is
// restored, a closing escaped pipe leaves the row unterminated, and ParseGatedInvariants skips
// a repository's own row between catalog rows instead of rejecting it.
func TestSplitInvariantRow_Boundary_EscapedPipes(t *testing.T) {
	row, ok := SplitInvariantRow(`| **ACME-02** a \| b | c \| d |`)
	if !ok || row.ID != "ACME-02" || !reflect.DeepEqual(row.Cells, []string{"a | b", "c | d"}) {
		t.Fatalf("escaped pipes: %+v, %v", row, ok)
	}
	if row, ok := SplitInvariantRow(`| **ACME-02** a | b \|`); ok {
		t.Errorf("a closing escaped pipe must not close the row: %+v", row)
	}
	rows, err := ParseGatedInvariants(gatedFixture(
		"| **HISS-01** control flow | a | b | c |",
		`| **ACME-01** own \| rule | x | y | z |`,
		"| **HISS-02** loops | a | b | c |"))
	if err != nil || len(rows) != 2 || rows[0].ID != "HISS-01" || rows[1].ID != "HISS-02" {
		t.Fatalf("ParseGatedInvariants = %+v, %v; want HISS-01 and HISS-02 only", rows, err)
	}
	table, err := InvariantTableRows(gatedFixture("| **HISS-01** control flow | a | b | c |", "| **ACME-01** own | x | y | z |"))
	if err != nil || len(table) != 2 || table[1].ID != "ACME-01" {
		t.Fatalf("InvariantTableRows = %+v, %v; want both rows", table, err)
	}
	if table, err := InvariantTableRows("# Harness\n\n| **ACME-01** own | x | y | z |\n"); err != nil || len(table) != 0 {
		t.Errorf("a row outside the section: %+v, %v", table, err)
	}
}
