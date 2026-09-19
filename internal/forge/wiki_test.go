package forge

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// canonicalTableRowPattern matches one row of AGENTS.md's "Core Directives & Invariants"
// table: "| **HISS-01** control flow | recursion prohibited; call graph = DAG | build |
// immediate build failure |". The fifth ("on fail") column has no analogue in the
// generated wiki's table and is intentionally not captured.
var canonicalTableRowPattern = regexp.MustCompile(
	`^\|\s*\*\*(HISS-\d+)\*\*\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|$`)

// wikiTableRowPattern matches one row of the generated wiki's table: "| **HISS-01** |
// control flow | recursion prohibited; call graph = DAG | build |".
var wikiTableRowPattern = regexp.MustCompile(
	`^\|\s*\*\*(HISS-\d+)\*\*\s*\|\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|\s*([^|]*?)\s*\|$`)

func parseCanonicalTable(t *testing.T, agentsMD string) map[string]hissInvariantRow {
	t.Helper()
	rows := make(map[string]hissInvariantRow)
	for _, line := range strings.Split(agentsMD, "\n") {
		m := canonicalTableRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows[m[1]] = hissInvariantRow{id: m[1], scope: m[2], rule: m[3], verification: m[4]}
	}
	return rows
}

func parseWikiTable(t *testing.T, wikiMD string) map[string]hissInvariantRow {
	t.Helper()
	rows := make(map[string]hissInvariantRow)
	for _, line := range strings.Split(wikiMD, "\n") {
		m := wikiTableRowPattern.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		rows[m[1]] = hissInvariantRow{id: m[1], scope: m[2], rule: m[3], verification: m[4]}
	}
	return rows
}

// compareInvariantTables reports every canonical id the wiki table omits and every id the
// wiki table invents beyond the canonical set. Both the positive guard test below (against
// the real files) and the negative test (against synthetic fixtures) call this, so the
// comparison logic itself is under test, not just its verdict against today's content.
func compareInvariantTables(canonical, wiki map[string]hissInvariantRow) (missing, invented []string) {
	for id := range canonical {
		if _, ok := wiki[id]; !ok {
			missing = append(missing, id)
		}
	}
	for id := range wiki {
		if _, ok := canonical[id]; !ok {
			invented = append(invented, id)
		}
	}
	sort.Strings(missing)
	sort.Strings(invented)
	return missing, invented
}

// TestHISSInvariantsWiki_MatchesCanonicalTable_Positive replays AGENTS.md's own invariant
// table against the generated wiki page. BUG-378 was exactly this comparison failing
// silently: HISS-03 and HISS-14 invented, HISS-17..HISS-21 omitted. A future edit to either
// table that reintroduces that drift fails here instead of shipping.
func TestHISSInvariantsWiki_MatchesCanonicalTable_Positive(t *testing.T) {
	data, err := os.ReadFile("../../AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	canonical := parseCanonicalTable(t, string(data))
	if len(canonical) == 0 {
		t.Fatal("parsed zero rows from AGENTS.md's invariant table; the parser or the table heading moved")
	}

	page := generateHISSInvariantsWiki()
	wiki := parseWikiTable(t, page.Content)

	missing, invented := compareInvariantTables(canonical, wiki)
	if len(missing) != 0 {
		t.Errorf("canonical invariants missing from the generated wiki: %v", missing)
	}
	if len(invented) != 0 {
		t.Errorf("generated wiki invents invariants absent from AGENTS.md: %v", invented)
	}

	for id, canonicalRow := range canonical {
		wikiRow, ok := wiki[id]
		if !ok {
			continue // already reported above
		}
		if wikiRow.scope != canonicalRow.scope {
			t.Errorf("%s: wiki scope %q does not match AGENTS.md scope %q", id, wikiRow.scope, canonicalRow.scope)
		}
	}
}

// TestCompareInvariantTables_Negative_MissingAndInvented proves the comparison used above
// actually catches both drift directions, using fixtures instead of waiting for AGENTS.md
// or wiki.go to drift for real.
func TestCompareInvariantTables_Negative_MissingAndInvented(t *testing.T) {
	canonical := map[string]hissInvariantRow{
		"HISS-01": {id: "HISS-01", scope: "control flow"},
		"HISS-02": {id: "HISS-02", scope: "loops, I/O"},
	}
	wiki := map[string]hissInvariantRow{
		"HISS-01": {id: "HISS-01", scope: "control flow"},
		"HISS-99": {id: "HISS-99", scope: "invented invariant"},
	}

	missing, invented := compareInvariantTables(canonical, wiki)
	if len(missing) != 1 || missing[0] != "HISS-02" {
		t.Errorf("expected HISS-02 reported as an omitted canonical row, got %v", missing)
	}
	if len(invented) != 1 || invented[0] != "HISS-99" {
		t.Errorf("expected HISS-99 reported as an invented row, got %v", invented)
	}
}

// TestRenderHISSInvariantTable_Boundary_EmptyAndPipeEscaping covers the two edge shapes
// the batch spec calls out: an empty invariant set still renders a valid table, and a cell
// containing a literal pipe cannot be mistaken for an extra column.
func TestRenderHISSInvariantTable_Boundary_EmptyAndPipeEscaping(t *testing.T) {
	empty := renderHISSInvariantTable(nil)
	if !strings.HasPrefix(empty, "| Invariant | Scope | Rule | Verification |\n| :--- | :--- | :--- | :--- |") {
		t.Errorf("empty table missing header/separator: %q", empty)
	}
	if strings.Count(empty, "\n") != 1 {
		t.Errorf("expected header + separator only (one newline) for an empty table, got %q", empty)
	}

	rows := []hissInvariantRow{{id: "HISS-99", scope: "a | b", rule: "x", verification: "y"}}
	rendered := renderHISSInvariantTable(rows)
	if !strings.Contains(rendered, `a \| b`) {
		t.Errorf("expected the pipe in scope to be escaped, got %q", rendered)
	}

	lines := strings.Split(rendered, "\n")
	rowLine := lines[len(lines)-1]
	// An unescaped '|' inside a cell would add a phantom column boundary. Strip every
	// escaped pipe first, then the remaining ones must be exactly the 5 real delimiters
	// of a 4-column row ("| a | b | c | d |").
	delimitersOnly := strings.ReplaceAll(rowLine, `\|`, "")
	if got := strings.Count(delimitersOnly, "|"); got != 5 {
		t.Errorf("expected 5 real column delimiters in %q, got %d", rowLine, got)
	}
}
