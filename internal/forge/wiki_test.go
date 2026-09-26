package forge

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/util"
)

// canonicalAgentsMD reads the repository's own AGENTS.md, the table GenerateWiki copies.
func canonicalAgentsMD(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	return string(data)
}

// wikiRepoRoot returns a fresh repository root named name whose AGENTS.md is agentsMD. The
// wiki portal is named after the root's base name, so tests that compare against the
// checked-in pages name it "praetor".
func wikiRepoRoot(t *testing.T, name, agentsMD string) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, wikiCanonicalSource), []byte(agentsMD), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

// generatedPages runs GenerateWiki over root and indexes the pages by file name.
func generatedPages(t *testing.T, root string) map[string]WikiPage {
	t.Helper()
	manifest, err := GenerateWiki(t.Context(), root, t.TempDir())
	if err != nil {
		t.Fatalf("GenerateWiki: %v", err)
	}
	pages := make(map[string]WikiPage, len(manifest.Pages))
	for _, page := range manifest.Pages {
		pages[page.Name] = page
	}
	return pages
}

// invariantRowID matches the first cell of a generated HISS table row: "| **HISS-01** |".
var invariantRowID = regexp.MustCompile(`(?m)^\| \*\*(HISS-\d+)\*\* \|`)

// pageInvariantIDs returns the invariant IDs of every HISS table row on a generated page,
// in page order.
func pageInvariantIDs(content string) []string {
	var ids []string
	for _, m := range invariantRowID.FindAllStringSubmatch(content, -1) {
		ids = append(ids, m[1])
	}
	return ids
}

// compareInvariantIDs reports every canonical ID a page omits and every ID a page carries
// beyond the canonical set, a repeated row included. Both the positive guard tests (against
// the real sources) and the negative test (against synthetic fixtures) call it, so the
// comparison itself is under test, not just its verdict against today's content.
func compareInvariantIDs(canonical, page []string) (missing, invented []string) {
	for _, id := range canonical {
		if !slices.Contains(page, id) {
			missing = append(missing, id)
		}
	}
	seen := make(map[string]bool, len(page))
	for _, id := range page {
		if !slices.Contains(canonical, id) || seen[id] {
			invented = append(invented, id)
		}
		seen[id] = true
	}
	sort.Strings(missing)
	sort.Strings(invented)
	return missing, invented
}

// TestHISSWiki_Positive_EveryCanonicalInvariantIsPublished replays both canonical sources
// against the generated pages. The matrix must carry every catalog invariant, HISS-01
// through HISS-21, with its title and its gated mark; the invariants page must carry every
// row of AGENTS.md's table cell for cell. BUG-378 was this comparison failing silently
// (HISS-03 and HISS-14 invented, HISS-17..HISS-21 omitted), and BUG-990 was the matrix
// stopping at HISS-19 because it was copied by hand.
func TestHISSWiki_Positive_EveryCanonicalInvariantIsPublished(t *testing.T) {
	agents := canonicalAgentsMD(t)
	gated, err := hisscatalog.ParseGatedInvariants(agents)
	if err != nil {
		t.Fatalf("parse AGENTS.md: %v", err)
	}
	pages := generatedPages(t, wikiRepoRoot(t, filepath.Join("cordanaLLM", "praetor"), agents))
	rules := hisscatalog.Rules()

	matrix := pages[hissMatrixPage+".md"].Content
	if missing, invented := compareInvariantIDs(hisscatalog.RuleIDs(), pageInvariantIDs(matrix)); len(missing)+len(invented) != 0 {
		t.Errorf("matrix: catalog invariants missing %v, invented %v", missing, invented)
	}
	gatedIDs := make([]string, 0, len(gated))
	for _, row := range gated {
		gatedIDs = append(gatedIDs, row.ID)
		want := "| **" + row.ID + "** | " + escapeTableCell(row.Scope) + " | " + escapeTableCell(row.Rule) + " | " +
			escapeTableCell(row.Enforcement) + " | " + escapeTableCell(row.OnFail) + " |"
		if !strings.Contains(pages[hissInvariantsPage+".md"].Content, want+"\n") {
			t.Errorf("invariants page lacks AGENTS.md's %s row %q", row.ID, want)
		}
	}
	for _, rule := range rules {
		mark := "no"
		if slices.Contains(gatedIDs, rule.ID) {
			mark = "yes"
		}
		if want := "| **" + rule.ID + "** | " + escapeTableCell(rule.Title) + " | " + mark + " | "; !strings.Contains(matrix, want) {
			t.Errorf("matrix lacks row prefix %q", want)
		}
	}
	invariants := pages[hissInvariantsPage+".md"].Content
	if missing, invented := compareInvariantIDs(gatedIDs, pageInvariantIDs(invariants)); len(missing)+len(invented) != 0 {
		t.Errorf("invariants page: gated invariants missing %v, invented %v", missing, invented)
	}
	if want := catalogRange(rules); !strings.Contains(invariants, want) || !strings.Contains(matrix, want) {
		t.Errorf("HISS pages do not state the catalog range %q", want)
	}
}

// TestHISSWiki_Negative_StandardIsNeverCalledHISS16 pins the naming fix: no generated page
// titles the standard "HISS-16", and only the moved stub still carries the old page name.
func TestHISSWiki_Negative_StandardIsNeverCalledHISS16(t *testing.T) {
	pages := generatedPages(t, wikiRepoRoot(t, filepath.Join("cordanaLLM", "praetor"), canonicalAgentsMD(t)))
	for name, page := range pages {
		for _, line := range strings.Split(page.Content, "\n") {
			if strings.HasPrefix(line, "# ") && strings.Contains(line, "HISS-16") && name != hissInvariantsMovedPage+".md" {
				t.Errorf("%s titles the standard HISS-16: %q", name, line)
			}
		}
		if name != hissInvariantsMovedPage+".md" && strings.Contains(page.Content, hissInvariantsMovedPage) {
			t.Errorf("%s still links the old page name %s", name, hissInvariantsMovedPage)
		}
	}
}

// TestGenerateWiki_Negative_UnreadableCanonicalTable proves the generator fails closed: a
// repository without AGENTS.md, with no invariant table, or gating an invariant the catalog
// does not define writes no page at all.
func TestGenerateWiki_Negative_UnreadableCanonicalTable(t *testing.T) {
	noTable := "# Harness\n\n## Operational Rules\n\n1. text\n"
	invented := "# Harness\n\n" + hisscatalog.GatedInvariantsHeading + "\n\n| Invariant | Rule | Enforcement | On fail |\n" +
		"| :--- | :--- | :--- | :--- |\n| **HISS-99** invented | a | b | c |\n"
	cases := map[string]struct {
		agents string
		want   error
	}{
		"no table":          {noTable, hisscatalog.ErrNoGatedInvariants},
		"unknown invariant": {invented, hisscatalog.ErrUnknownInvariant},
	}
	for name, tc := range cases {
		out := t.TempDir()
		if _, err := GenerateWiki(t.Context(), wikiRepoRoot(t, "repo", tc.agents), out); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
		requireEmptyDir(t, out)
	}

	missingRoot := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(missingRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := GenerateWiki(t.Context(), missingRoot, out); err == nil || !strings.Contains(err.Error(), wikiCanonicalSource) {
		t.Errorf("a root without AGENTS.md: err = %v, want an error naming %s", err, wikiCanonicalSource)
	}
	requireEmptyDir(t, out)
}

// TestCompareInvariantIDs_Negative_MissingInventedAndRepeated proves the comparison used
// above catches every drift direction, using fixtures instead of waiting for a source or
// the generator to drift for real.
func TestCompareInvariantIDs_Negative_MissingInventedAndRepeated(t *testing.T) {
	missing, invented := compareInvariantIDs(
		[]string{"HISS-01", "HISS-02"},
		[]string{"HISS-01", "HISS-99", "HISS-01"},
	)
	if len(missing) != 1 || missing[0] != "HISS-02" {
		t.Errorf("expected HISS-02 reported as an omitted canonical row, got %v", missing)
	}
	if strings.Join(invented, ",") != "HISS-01,HISS-99" {
		t.Errorf("expected the repeated HISS-01 and the invented HISS-99, got %v", invented)
	}
}

// TestHISSWiki_Boundary_SingleGatedRowAndMovedStub drives the smallest valid table: the
// invariants page carries exactly that row, the matrix still lists the whole catalog with a
// single gated mark, and the moved stub points at a page the generator really publishes.
func TestHISSWiki_Boundary_SingleGatedRowAndMovedStub(t *testing.T) {
	agents := "# Harness\n\n" + hisscatalog.GatedInvariantsHeading + "\n\n| Invariant | Rule | Enforcement | On fail |\n" +
		"| :--- | :--- | :--- | :--- |\n| **HISS-16** context integrity | one source | pre-commit | blocker |\n"
	pages := generatedPages(t, wikiRepoRoot(t, "repo", agents))

	if ids := pageInvariantIDs(pages[hissInvariantsPage+".md"].Content); strings.Join(ids, ",") != "HISS-16" {
		t.Errorf("invariants page rows = %v, want HISS-16 only", ids)
	}
	matrix := pages[hissMatrixPage+".md"].Content
	if got := len(pageInvariantIDs(matrix)); got != len(hisscatalog.Rules()) {
		t.Errorf("matrix rows = %d, want %d", got, len(hisscatalog.Rules()))
	}
	if got := strings.Count(matrix, " | yes | "); got != 1 {
		t.Errorf("matrix gated marks = %d, want 1", got)
	}

	stub, ok := pages[hissInvariantsMovedPage+".md"]
	if !ok {
		t.Fatalf("no moved stub named %s.md", hissInvariantsMovedPage)
	}
	if !strings.Contains(stub.Content, "["+hissInvariantsPage+"]("+hissInvariantsPage+".md)") {
		t.Errorf("moved stub does not link [%s](%s.md): %q", hissInvariantsPage, hissInvariantsPage, stub.Content)
	}
	if _, ok := pages[hissInvariantsPage+".md"]; !ok {
		t.Errorf("moved stub points at %s, which the generator does not publish", hissInvariantsPage)
	}
}

// TestMarkdownTable_Boundary_EmptyPipeAndLineBreak covers the edge shapes of the table
// renderer: no rows still renders a valid table, a literal pipe cannot become a column
// boundary, and a line break cannot end the row early.
func TestMarkdownTable_Boundary_EmptyPipeAndLineBreak(t *testing.T) {
	empty := markdownTable([]string{"A", "B"}, nil)
	if empty != "| A | B |\n| :--- | :--- |" {
		t.Errorf("empty table = %q", empty)
	}

	rendered := markdownTable([]string{"A", "B"}, [][]string{{"a | b", "line one\n  line two"}})
	rowLine := strings.Split(rendered, "\n")[2]
	if rowLine != `| a \| b | line one line two |` {
		t.Errorf("row = %q", rowLine)
	}
	// An unescaped '|' inside a cell would add a phantom column boundary. Strip every
	// escaped pipe first, then the remaining ones must be exactly the 3 real delimiters of a
	// 2-column row.
	if got := strings.Count(strings.ReplaceAll(rowLine, `\|`, ""), "|"); got != 3 {
		t.Errorf("expected 3 real column delimiters in %q, got %d", rowLine, got)
	}

	for rules, want := range map[int]string{0: "no invariants", 1: "1 invariant, HISS-01", 21: "21 invariants, HISS-01 through HISS-21"} {
		if got := catalogRange(hisscatalog.Rules()[:rules]); got != want {
			t.Errorf("catalogRange(%d rules) = %q, want %q", rules, got, want)
		}
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
	if got := compileContextFlowViolations(generateHomeWiki("cordanaLLM/praetor", hisscatalog.Rules()).Content); len(got) != 0 {
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

// TestCheckedInWiki_Boundary_MatchesGenerator holds docs/wiki byte-equal to GenerateWiki's
// output over the repository's own AGENTS.md, so a generator or table change cannot land
// without the published copy (the Home.md diagram, the invariant table and the hand-written
// matrix all drifted that way). docs/wiki may carry no page the generator does not own: the
// wiki sync publishes every file there. The repository name comes from the root's base
// name, so the root is spelled ".../praetor".
func TestCheckedInWiki_Boundary_MatchesGenerator(t *testing.T) {
	pages := generatedPages(t, wikiRepoRoot(t, filepath.Join("cordanaLLM", "praetor"), canonicalAgentsMD(t)))
	if len(pages) == 0 {
		t.Fatal("generator produced no pages")
	}
	for name, page := range pages {
		data, err := os.ReadFile(filepath.Join("..", "..", "docs", "wiki", name))
		if err != nil {
			t.Fatalf("generated page %s has no checked-in copy: %v", name, err)
		}
		if got, _ := util.NormalizeLineEndings(string(data)); got != page.Content {
			t.Errorf("docs/wiki/%s differs from GenerateWiki output; regenerate it with 'praetorctl forge sync-wiki' from a checkout named praetor", name)
		}
	}
	entries, err := os.ReadDir(filepath.Join("..", "..", "docs", "wiki"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if _, ok := pages[entry.Name()]; !ok {
			t.Errorf("docs/wiki/%s is not generated by GenerateWiki; the wiki sync would publish it unchecked", entry.Name())
		}
	}
}

func TestResolveWikiRepoName_Positive_DerivesFromRemote(t *testing.T) {
	root := t.TempDir()
	if _, err := util.RunGit(t.Context(), root, "init"); err != nil {
		t.Fatal(err)
	}
	if _, err := util.RunGit(t.Context(), root, "remote", "add", "origin", "https://github.com/acme/widgets.git"); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWikiRepoName(t.Context(), root)
	if err != nil {
		t.Fatalf("resolveWikiRepoName failed: %v", err)
	}
	if got != "acme/widgets" {
		t.Errorf("resolveWikiRepoName = %q, want acme/widgets", got)
	}
}

func TestResolveWikiRepoName_Negative_NeutralFallback(t *testing.T) {
	// A directory named "repo" inside a temp dir with no git remote.
	// We want to ensure it never returns "cordanaLLM/praetor" (unless it actually is that).
	parent := filepath.Join(t.TempDir(), "owner")
	root := filepath.Join(parent, "repo")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := resolveWikiRepoName(t.Context(), root)
	if err != nil {
		t.Fatalf("resolveWikiRepoName failed: %v", err)
	}
	if got == "cordanaLLM/praetor" {
		t.Errorf("resolveWikiRepoName = %q, should not fall back to cordanaLLM/praetor", got)
	}
	if got != "owner/repo" {
		t.Errorf("resolveWikiRepoName = %q, want owner/repo", got)
	}
}

func TestResolveWikiRepoName_Boundary_NoOwner(t *testing.T) {
	// Root of filesystem or just a single temp dir with no parent directory that isn't tmp.
	// ResolveRepoIdentity will return an error or we just get an error.
	got, err := resolveWikiRepoName(t.Context(), "/")
	if err == nil {
		t.Errorf("resolveWikiRepoName(/) = %q, want error", got)
	}
}
