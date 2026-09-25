package forge

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
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

// mermaidNode matches a node definition such as AGENTS["AGENTS.md\n(canonical source)"].
var mermaidNode = regexp.MustCompile(`(\w+)\["([^"]*)"\]`)

// mermaidEdge matches one "A --> B" edge; the source side may carry its label inline.
var mermaidEdge = regexp.MustCompile(`^\s*(\w+)(?:\["[^"]*"\])?\s*-->\s*(\w+)`)

// flowEdges returns every mermaid edge in content as {from, to}, each node resolved to the
// first line of its label.
func flowEdges(content string) [][2]string {
	labels := make(map[string]string)
	for _, m := range mermaidNode.FindAllStringSubmatch(content, -1) {
		labels[m[1]] = strings.SplitN(m[2], `\n`, 2)[0]
	}
	var edges [][2]string
	for _, line := range strings.Split(content, "\n") {
		if m := mermaidEdge.FindStringSubmatch(line); m != nil {
			edges = append(edges, [2]string{labels[m[1]], labels[m[2]]})
		}
	}
	return edges
}

// compileContextFlowViolations names every way a diagram misstates the compile-context data
// flow: AGENTS.md must feed compile-context, compile-context must feed the vendor files,
// and no edge may produce AGENTS.md.
func compileContextFlowViolations(content string) []string {
	var violations []string
	feedsTranspiler, feedsVendors := false, false
	for _, edge := range flowEdges(content) {
		from, to := edge[0], edge[1]
		switch {
		case strings.HasPrefix(to, "AGENTS.md"):
			violations = append(violations, from+" -> "+to+" makes AGENTS.md an output")
		case strings.HasPrefix(from, "AGENTS.md") && strings.Contains(to, "compile-context"):
			feedsTranspiler = true
		case strings.Contains(from, "compile-context") && strings.Contains(to, "CLAUDE.md"):
			feedsVendors = true
		}
	}
	if !feedsTranspiler {
		violations = append(violations, "AGENTS.md does not feed compile-context")
	}
	if !feedsVendors {
		violations = append(violations, "compile-context does not feed the vendor files")
	}
	return violations
}

// TestHomeWiki_Positive_CompileContextFlowsFromAGENTS pins BUG-680: compile-context reads
// AGENTS.md (its --source default) and writes the vendor files. The preset landing page
// carried the same inverted diagram and is held to the same rule.
func TestHomeWiki_Positive_CompileContextFlowsFromAGENTS(t *testing.T) {
	if got := compileContextFlowViolations(generateHomeWiki("cordanaLLM/praetor").Content); len(got) != 0 {
		t.Errorf("generated Home diagram: %v", got)
	}
	preset, err := os.ReadFile(filepath.Join("..", "..", "docs", "presets", "mkdocs", "docs", "index.md"))
	if err != nil {
		t.Fatal(err)
	}
	if got := compileContextFlowViolations(string(preset)); len(got) != 0 {
		t.Errorf("mkdocs preset index diagram: %v", got)
	}
}

// TestHomeWiki_Negative_RejectsManifestToAGENTSFlow replays the diagram Home.md shipped
// before the fix, .standards.yaml -> compile-context -> AGENTS.md, through the same check.
func TestHomeWiki_Negative_RejectsManifestToAGENTSFlow(t *testing.T) {
	old := "```mermaid\nflowchart LR\n" +
		`    MANIFEST[".standards.yaml"] --> TRANSPILER["praetorctl compile-context"]` + "\n" +
		`    TRANSPILER --> AGENTS["AGENTS.md\n(Canonical Truth)"]` + "\n" +
		`    AGENTS --> GATES["Verification Cascade"]` + "\n```\n"
	got := compileContextFlowViolations(old)
	want := []string{
		"praetorctl compile-context -> AGENTS.md makes AGENTS.md an output",
		"AGENTS.md does not feed compile-context",
		"compile-context does not feed the vendor files",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("violations = %q, want %q", got, want)
	}
}

// TestCheckedInWiki_Boundary_MatchesGenerator holds every generator-owned page under
// docs/wiki byte-equal to GenerateWiki's output, so a generator fix cannot land without the
// published copy (the Home.md diagram and the HISS-16 table both drifted that way). The
// repository name comes from the root's base name, so the root is spelled ".../praetor".
// HISS-Matrix.md is hand-written and not generated, so it is not compared.
func TestCheckedInWiki_Boundary_MatchesGenerator(t *testing.T) {
	manifest, err := GenerateWiki(t.Context(), filepath.Join(t.TempDir(), "praetor"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Pages) == 0 {
		t.Fatal("generator produced no pages")
	}
	for _, page := range manifest.Pages {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", "wiki", page.Name))
		if err != nil {
			t.Fatalf("generated page %s has no checked-in copy: %v", page.Name, err)
		}
		if got, _ := util.NormalizeLineEndings(string(data)); got != page.Content {
			t.Errorf("docs/wiki/%s differs from GenerateWiki output; regenerate it", page.Name)
		}
	}
}
