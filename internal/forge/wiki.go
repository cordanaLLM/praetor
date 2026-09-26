package forge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxWikiPagesLimit = 100
	// wikiPageFilePerm is the mode applied to every generated wiki page.
	wikiPageFilePerm = 0o644
	// wikiDirPerm is the mode applied to the generated wiki output directory.
	wikiDirPerm = 0o750
	// wikiOwner is the organization the generated wiki portal belongs to.
	wikiOwner = "cordanaLLM"
)

// WikiPage represents an individual wiki markdown page.
type WikiPage struct {
	Name    string `json:"name"`
	Title   string `json:"title"`
	Content string `json:"content"`
	Path    string `json:"path"`
}

// WikiManifest documents the collection of generated wiki documents.
type WikiManifest struct {
	GeneratedAt time.Time  `json:"generated_at"`
	OutputDir   string     `json:"output_dir"`
	Pages       []WikiPage `json:"pages"`
}

// GenerateWiki synthesizes the formal governance wiki documentation suite.
//
// A relative outputDir is a location inside repoRoot and every page is written confined to
// repoRoot, so a repository that ships docs/wiki (or an ancestor) as a link leading outside
// it cannot redirect the pages (BUG-826). An absolute outputDir is written as given.
func GenerateWiki(ctx context.Context, repoRoot, outputDir string) (*WikiManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before wiki generation: %w", err)
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("output directory cannot be empty")
	}

	repoName, err := resolveWikiRepoName(repoRoot)
	if err != nil {
		return nil, err
	}

	pages := []WikiPage{
		generateHomeWiki(repoName),
		generateHISSInvariantsWiki(),
		generateArchitectureLatticeWiki(),
		generateAPIReferenceWiki(),
	}

	if err := writeWikiPages(ctx, generatedDir{root: repoRoot, dir: outputDir}, pages); err != nil {
		return nil, fmt.Errorf("failed writing wiki pages to %s: %w", outputDir, err)
	}

	return &WikiManifest{
		GeneratedAt: time.Now().UTC(),
		OutputDir:   outputDir,
		Pages:       pages,
	}, nil
}

// resolveWikiRepoName derives the "<owner>/<repo>" name the wiki portal is generated for.
//
// The caller commonly passes ".", so the path is made absolute before its last element is
// taken: filepath.Base(".") is "." and would otherwise select a hard-coded placeholder
// name that is wrong for every real invocation.
func resolveWikiRepoName(repoRoot string) (string, error) {
	absRoot, err := filepath.Abs(strings.TrimSpace(repoRoot))
	if err != nil {
		return "", fmt.Errorf("failed to resolve repository root %q: %w", repoRoot, err)
	}
	base := filepath.Base(filepath.Clean(absRoot))
	if base == "." || base == ".." || base == string(filepath.Separator) || base == "" {
		return "", fmt.Errorf("cannot derive a repository name from %q", repoRoot)
	}
	return wikiOwner + "/" + base, nil
}

func writeWikiPages(ctx context.Context, out generatedDir, pages []WikiPage) error {
	if err := out.mkdir(wikiDirPerm); err != nil {
		return fmt.Errorf("failed to create wiki output directory %s: %w", out.location(), err)
	}

	for i := 0; i < len(pages) && i < MaxWikiPagesLimit; i++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("context cancelled during wiki generation at page %s: %w", pages[i].Name, err)
		}
		page := &pages[i]
		page.Path = out.path(page.Name)
		if err := out.write(page.Name, []byte(page.Content), wikiPageFilePerm); err != nil {
			return fmt.Errorf("failed writing wiki page %s: %w", page.Name, err)
		}
	}
	return nil
}

// generateHomeWiki renders the portal page. Its diagram follows the real data flow:
// AGENTS.md is compile-context's input (its --source default) and the vendor files are the
// output; .standards.yaml feeds the audit, not the transpiler.
func generateHomeWiki(repoName string) WikiPage {
	content := fmt.Sprintf(`# %s Wiki Portal

Welcome to the official repository governance wiki for cordanaLLM.

## Governance Lifecycle Architecture

`+"```mermaid"+`
flowchart LR
    AGENTS["AGENTS.md\n(canonical source)"] --> TRANSPILER["praetorctl compile-context"]
    TRANSPILER --> VENDORS["CLAUDE.md, .cursor/rules, copilot-instructions,\n.windsurfrules, GEMINI.md, .codex/rules.md"]
    MANIFEST[".standards.yaml\n+ .standards.lock"] --> AUDIT["praetorctl audit"]
    VENDORS --> GATES["Verification Cascade\n(make verify-all)"]
    AUDIT --> GATES
    GATES --> RECEIPT["Ed25519 Exit-0 Receipt"]
`+"```"+`

## Quick Navigation

| Document | Description |
| :--- | :--- |
| [[HISS-16-Invariants]] | The invariants AGENTS.md gates, with the rule and verification for each. |
| [[Architecture-Lattice]] | Mathematical join-semilattice and Highest Standard Wins resolution. |
| [[API-Reference]] | CLI commands, MCP tools, and multi-forge driver specifications. |
`, repoName)

	return WikiPage{
		Name:    "Home.md",
		Title:   "Home",
		Content: content,
	}
}

// hissInvariantRow is one row of the canonical invariant table in AGENTS.md's "Core
// Directives & Invariants" section (Invariant | Rule | Enforcement | On fail). scope is
// that table's short descriptor next to the ID ("control flow", "loops, I/O", ...); rule
// and verification are its Rule and Enforcement columns, copied verbatim. Keep every field
// here byte-for-byte equal to the corresponding AGENTS.md cell:
// TestHISSInvariantsWiki_MatchesCanonicalTable reads AGENTS.md directly and fails the
// build the moment this table and that one disagree, in either direction.
type hissInvariantRow struct {
	id           string
	scope        string
	rule         string
	verification string
}

// canonicalHISSInvariants mirrors AGENTS.md's "Core Directives & Invariants" table as of
// this commit. It intentionally excludes HISS-03 and HISS-14: neither is in that table,
// and a prior version of this file invented rows for both.
var canonicalHISSInvariants = []hissInvariantRow{
	{"HISS-01", "control flow", "recursion prohibited; call graph = DAG", "build"},
	{"HISS-02", "loops, I/O", "scalar upper bound on every loop; explicit `context.Context` timeout on every I/O", "Semgrep / AST"},
	{"HISS-04", "complexity", "McCabe cyclomatic <= 10, cognitive <= 15, func LOC <= 75, statements <= 50", "AST sweep"},
	{"HISS-07", "errors", "zero `.unwrap()` / `.expect()`; every error handled or wrapped with context", "linter / compiler"},
	{"HISS-10", "warnings", "zero warnings: compiler, linter, format sweeps", "sweep"},
	{"HISS-15", "3D testing", "positive + negative + boundary tests mandatory, every public interface", "CI coverage gate"},
	{"HISS-16", "context integrity", "single canonical `AGENTS.md`; vendor files compiled via `standardsctl compile-context`; `AGENTS.md` passes caveman lint", "pre-commit"},
	{"HISS-17", "state ledger", "turn start: `praetorctl state status` + `.workingdir/OPEN.md`, never whole `.workingdir/STATE.md`; tasks via `standardsctl state task`; turn end: `standardsctl state sync .`", "pre-commit / CI"},
	{"HISS-18", "CI efficiency", "diff-aware gating; docs/state-only change skips heavy race + security gates via `standardsctl ci filter`", "CI"},
	{"HISS-19", "reuse before writing", "one behavior = one implementation; extend or call existing, config formats included", "`dedupe scan` in verify-all"},
	{"HISS-20", "replayable evidence", "every rule has fixtures replayed both directions; coverage claim reproducible, never asserted", "`hiss coverage --verify` in verify-all"},
	{"HISS-21", "platform neutrality", "gates, hooks, emitted templates run on Linux, macOS, Windows, or skip with stated reason; gate that cannot run != passing gate", "Platform Neutrality matrix in CI"},
}

// renderHISSInvariantTable renders rows as a GitHub-flavored markdown table. A literal
// pipe in any cell is escaped so it cannot be mistaken for a column boundary, and a nil or
// empty slice still renders a valid, row-less table instead of breaking the page around it.
func renderHISSInvariantTable(rows []hissInvariantRow) string {
	var b strings.Builder
	b.WriteString("| Invariant | Scope | Rule | Verification |\n")
	b.WriteString("| :--- | :--- | :--- | :--- |")
	for _, row := range rows {
		fmt.Fprintf(&b, "\n| **%s** | %s | %s | %s |",
			escapeTableCell(row.id), escapeTableCell(row.scope), escapeTableCell(row.rule), escapeTableCell(row.verification))
	}
	return b.String()
}

// escapeTableCell neutralizes a pipe so a cell's own content cannot be read as an
// additional column by a markdown renderer.
func escapeTableCell(s string) string {
	return strings.ReplaceAll(s, "|", "\\|")
}

func generateHISSInvariantsWiki() WikiPage {
	content := `# The High-Integrity Systems Standard (HISS-16)

HISS-16 establishes formal engineering determinism across polyglot repositories.

## Invariant Summary

` + renderHISSInvariantTable(canonicalHISSInvariants) + `

## Zero-Warning Cascade

` + "```mermaid" + `
flowchart TD
    IDE["1. IDE / standards-lsp"] --> HOOKS["2. Pre-Commit / lefthook"]
    HOOKS --> PUSH["3. Pre-Push / audit"]
    PUSH --> CI["4. CI Ephemeral Sandbox"]
    CI --> ADMIT["5. PR Admission\n(standardsctl forge validate-pr in CI)"]
` + "```\n"

	return WikiPage{
		Name:    "HISS-16-Invariants.md",
		Title:   "HISS-16 Invariants",
		Content: content,
	}
}

func generateArchitectureLatticeWiki() WikiPage {
	content := `# Composable Archetypes & Strictness Lattice

Repository configuration is modeled as a bounded Join-Semilattice:
$$\mathcal{P}_{\text{resolved}} = \mathcal{P}_1 \sqcup \mathcal{P}_2 \sqcup \dots \sqcup \mathcal{F}_n$$

## Lattice Resolution Rule: "Highest Standard Wins"

- **Complexity Limits**: Evaluated as the greatest lower bound ($\min$).
- **Review Approvals & SLSA**: Evaluated as the least upper bound ($\max$).
- **Linters & Features**: Cumulative deduplicated set union ($\cup$).

` + "```mermaid" + `
flowchart TD
    PROFILE["Profile: framework\n(Cyclomatic <= 10, Approvals: 1)"] --> LATTICE["Lattice Join Engine\n(internal/config)"]
    FACET["Facet: security:high\n(SLSA Level 3, Approvals: 2)"] --> LATTICE
    LATTICE --> RESOLVED["Resolved Policy\n(Cyclomatic <= 10, Approvals: 2, SLSA 3)"]
` + "```\n"

	return WikiPage{
		Name:    "Architecture-Lattice.md",
		Title:   "Architecture Lattice",
		Content: content,
	}
}

// frameworkKitReference documents compile-framework-assets (ADR-0007 clause 5). The flags
// follow cmd/standardsctl/compile_framework_assets.go and the keys FrameworkKitConfig in
// internal/compiler/framework_assets.go.
const frameworkKitReference = "- `praetorctl compile-framework-assets --config <kit.yaml> --output <dir>`: " +
	"Writes a framework kit's `llms.txt`, `llms-full.txt`, `.agents/rules/<kit_name>.md` and " +
	"starter templates (ADR-0007 clause 5).\n" +
	"\n" +
	"### Framework kit assets\n" +
	"\n" +
	"`compile-framework-assets` reads one YAML document with the keys `kit_name`, `language`, " +
	"`version`, `description`, `rules`, `skills` and `components` (the `FrameworkKitConfig` " +
	"fields in `internal/compiler/framework_assets.go`); any other key is refused. Both flags " +
	"are required: `--output` has no default, so the fixed asset names never overwrite the " +
	"working tree's own `llms.txt` by accident.\n" +
	"\n" +
	"```yaml\n" +
	"kit_name: sveltesentio\n" +
	"language: svelte\n" +
	"version: 5.0.0\n" +
	"description: Svelte 5 component library\n" +
	"rules: [Use Svelte 5 runes exclusively]\n" +
	"skills: [a11y-debugging]\n" +
	"components: [Button, Modal]\n" +
	"```\n" +
	"\n" +
	"`kit_name` names the agent rule file, so it must be one file-name component: 1-64 " +
	"letters, digits, `.`, `_` or `-`, starting with a letter or digit. Anything else, such " +
	"as `../x` or `a/b`, fails before any file is written " +
	"(`TestCompileFrameworkAssets_RejectsUnsafeKitNameBeforeWriting` in " +
	"`internal/compiler/framework_assets_test.go`).\n"

func generateAPIReferenceWiki() WikiPage {
	content := `# API & CLI Reference Manual

## praetorctl CLI Commands

- ` + "`praetorctl init`" + `: Scaffolds a new .standards.yaml manifest with profiles and facets.
- ` + "`praetorctl plan`" + `: Computes the lattice supremum and performs a dry-run drift calculation.
- ` + "`praetorctl sync`" + `: Applies declarative standards to branch protections, labels, and CI.
- ` + "`praetorctl compile-context`" + `: Transpiles AGENTS.md to CLAUDE.md, Cursor rules, and Copilot.
- ` + "`praetorctl baseline`" + `: Records or verifies legacy brownfield technical debt.
- ` + "`praetorctl audit`" + `: Validates 100% compliance against the active standards baseline.
` + frameworkKitReference + `
## Multi-Forge Federation

` + "```mermaid" + `
sequenceDiagram
    participant CLI as praetorctl
    participant GH as GitHub Driver
    participant GL as GitLab Driver
    participant GT as Gitea Driver
    CLI->>GH: Authenticate & Post Status Check
    CLI->>GL: Reconcile Branch Protections
    CLI->>GT: Synchronize Labels & Issues
` + "```\n"

	return WikiPage{
		Name:    "API-Reference.md",
		Title:   "API Reference",
		Content: content,
	}
}
