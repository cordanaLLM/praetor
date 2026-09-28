package forge

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/hisscatalog"
	"github.com/cordanaLLM/praetor/internal/lockdown"
)

// Invariant bounds adhering to HISS-02.
const (
	MaxWikiPagesLimit = 100
	// wikiPageFilePerm is the mode applied to every generated wiki page.
	wikiPageFilePerm = 0o644
	// wikiDirPerm is the mode applied to the generated wiki output directory.
	wikiDirPerm = 0o750
	// wikiCanonicalSource is the file whose "Core Directives & Invariants" table the HISS
	// pages copy: the AGENTS.md that compile-context reads as its --source default.
	wikiCanonicalSource = "AGENTS.md"
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
// The HISS pages are derived, never copied by hand: the invariant list and the matrix rows
// come from the rule catalog in internal/hisscatalog, and the gated invariants from the "Core
// Directives & Invariants" table of repoRoot's AGENTS.md. A repository whose AGENTS.md is
// missing or carries no readable table fails here instead of publishing an empty or stale
// page.
//
// A relative outputDir is a location inside repoRoot and every page is written confined to
// repoRoot, so a repository that ships docs/wiki (or an ancestor) as a link leading outside
// it cannot redirect the pages (BUG-826). An absolute outputDir is written as given.
//
// The portal is named after repoRoot's identity (resolveWikiRepoName), with defaultOwner
// (forge.default_owner) as the last owner step.
func GenerateWiki(ctx context.Context, repoRoot, outputDir, defaultOwner string) (*WikiManifest, error) {
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("context cancelled before wiki generation: %w", err)
	}
	if strings.TrimSpace(outputDir) == "" {
		return nil, errors.New("output directory cannot be empty")
	}

	repoName, err := resolveWikiRepoName(ctx, repoRoot, defaultOwner)
	if err != nil {
		return nil, err
	}
	gated, err := readGatedInvariants(ctx, repoRoot)
	if err != nil {
		return nil, err
	}
	rules := hisscatalog.Rules()

	pages := []WikiPage{
		generateHomeWiki(repoName, rules),
		generateHISSInvariantsWiki(rules, gated),
		generateHISSInvariantsMovedWiki(),
		generateHISSMatrixWiki(repoName, rules, gated),
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

// readGatedInvariants reads repoRoot's AGENTS.md through the bounded, symlink-rejecting
// snapshot read and returns its gated invariant rows.
func readGatedInvariants(ctx context.Context, repoRoot string) ([]hisscatalog.GatedInvariant, error) {
	data, err := contextopt.ReadSnapshot(ctx, filepath.Join(repoRoot, wikiCanonicalSource))
	if err != nil {
		return nil, fmt.Errorf("read the canonical HISS invariant table from %s: %w", wikiCanonicalSource, err)
	}
	gated, err := hisscatalog.ParseGatedInvariants(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse the canonical HISS invariant table in %s: %w", wikiCanonicalSource, err)
	}
	return gated, nil
}

// resolveWikiRepoName derives the "<owner>/<repo>" name the wiki portal is generated for
// through config.ResolveRepositoryIdentity (ADR-0014 §3): the manifest's repository block,
// then the origin remote, with defaultOwner as the last owner step. The checkout path is
// never read, so a checkout's parent directory never names the portal, and a repository
// that resolves no identity fails instead of publishing a guessed one.
func resolveWikiRepoName(ctx context.Context, repoRoot, defaultOwner string) (string, error) {
	owner, repo, err := config.ResolveRepositoryIdentity(ctx, repoRoot, "", defaultOwner)
	if err != nil {
		return "", fmt.Errorf("cannot derive a repository name from %q: %w", repoRoot, err)
	}
	return owner + "/" + repo, nil
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

// generateHomeWiki renders the portal page. Its figure, governance-lifecycle
// (docs/figures/governance-lifecycle.ts), follows the real data flow: AGENTS.md is
// compile-context's input (its --source default) and the vendor files are the output;
// .standards.yaml resolves the effective policy and is not the transpiler's input.
func generateHomeWiki(repoName string, rules []hisscatalog.Rule) WikiPage {
	content := fmt.Sprintf(`# %s Wiki Portal

Welcome to the official repository governance wiki for %s.

## Governance Lifecycle Architecture

`+figureFence("governance-lifecycle")+`

The lifecycle has three flows. `+"`praetorctl compile-context`"+` reads `+"`AGENTS.md`"+` and writes
the vendor instruction files. `+"`.standards.yaml`"+` and `+"`.standards.lock`"+` resolve to one
effective policy, which the devcontainer toolchain, `+"`praetorctl audit`"+` and `+"`praetorctl plan`"+`
apply. `+"`praetorctl gate run`"+` runs the gate stages, and a passing run mints the Ed25519
Exit-0 receipt.

## Quick Navigation

| Document | Description |
| :--- | :--- |
| [%s](%s.md) | The High-Integrity Systems Standard (HISS) and the invariants AGENTS.md gates, with the rule and verification for each. |
| [%s](%s.md) | The full HISS catalog of %s, with the enforcement and failure action of each. |
| [Architecture-Lattice](Architecture-Lattice.md) | Mathematical join-semilattice and Highest Standard Wins resolution. |
| [API-Reference](API-Reference.md) | CLI commands, MCP tools, and multi-forge driver specifications. |
`, repoName, repoName, hissInvariantsPage, hissInvariantsPage, hissMatrixPage, hissMatrixPage, catalogRange(rules))

	return WikiPage{
		Name:    "Home.md",
		Title:   "Home",
		Content: content,
	}
}

// Wiki page names of the HISS pages, without the .md suffix, as [links](links.md) spell them. The
// invariants page was published as HISS-16-Invariants until the page was named after the
// standard; hissInvariantsMovedPage keeps that name resolving for existing links.
const (
	hissInvariantsPage      = "HISS-Invariants"
	hissInvariantsMovedPage = "HISS-16-Invariants"
	hissMatrixPage          = "HISS-Matrix"
)

// catalogRange names the span of rules for prose: "21 invariants, HISS-01 through HISS-21".
// A catalog of one or none still reads as a sentence.
func catalogRange(rules []hisscatalog.Rule) string {
	switch len(rules) {
	case 0:
		return "no invariants"
	case 1:
		return "1 invariant, " + rules[0].ID
	default:
		return fmt.Sprintf("%d invariants, %s through %s", len(rules), rules[0].ID, rules[len(rules)-1].ID)
	}
}

// A figure fence names an interactive figure by slug: docs/figures/<slug>.ts is its spec, and
// the site hook and the wiki sync render the fence from docs/assets/figures/<slug>.json
// (docs/adr/0015-interactive-figures-from-vendored-interfig.md). The two halves are constants
// so a constant page section can name a figure too; the fence ends without a newline.
const (
	figureFenceOpen  = "```figure\n"
	figureFenceClose = "\n```"
)

// figureFence returns the fence that names the figure slug.
func figureFence(slug string) string {
	return figureFenceOpen + slug + figureFenceClose
}

// markdownTable renders a GitHub-flavored markdown table with a left-aligned delimiter row.
// Every cell is escaped by escapeTableCell, and a nil or empty rows slice still renders a
// valid, row-less table instead of breaking the page around it. The result carries no
// trailing newline.
func markdownTable(header []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n|")
	for range header {
		b.WriteString(" :--- |")
	}
	for _, row := range rows {
		cells := make([]string, len(row))
		for i, cell := range row {
			cells[i] = escapeTableCell(cell)
		}
		b.WriteString("\n| " + strings.Join(cells, " | ") + " |")
	}
	return b.String()
}

// escapeTableCell keeps a cell's own content inside its cell: a pipe is escaped so a
// renderer cannot read it as an additional column, and a line break, which would end the
// row, collapses into a space with the surrounding whitespace.
func escapeTableCell(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "|", "\\|")), " ")
}

// renderGatedInvariantTable renders AGENTS.md's gated rows cell for cell. The first
// column bolds the ID exactly as AGENTS.md does.
func renderGatedInvariantTable(gated []hisscatalog.GatedInvariant) string {
	rows := make([][]string, 0, len(gated))
	for _, row := range gated {
		rows = append(rows, []string{"**" + row.ID + "**", row.Scope, row.Rule, row.Enforcement, row.OnFail})
	}
	return markdownTable([]string{"Invariant", "Scope", "Rule", "Verification", "On fail"}, rows)
}

func generateHISSInvariantsWiki(rules []hisscatalog.Rule, gated []hisscatalog.GatedInvariant) WikiPage {
	content := `# The High-Integrity Systems Standard (HISS)

HISS establishes formal engineering determinism across polyglot repositories. It defines
` + catalogRange(rules) + `; [` + hissMatrixPage + `](` + hissMatrixPage + `.md) lists every one with its enforcement.
HISS-16 is one of them, the context-integrity invariant, not the name of the standard.

## Gated Invariants

This repository's ` + "`AGENTS.md`" + ` gates the invariants below. ` + "`praetorctl forge sync-wiki`" + `
copies them from its "Core Directives & Invariants" table each time it regenerates this page.

` + renderGatedInvariantTable(gated) + `

## Verification Ladder

` + figureFence("verification-ladder") + `

A change climbs four tiers: diagnostics from ` + "`standards-lsp`" + ` in the editor, the lefthook
pre-commit and pre-push hooks, the six-stage gate pipeline that signs the Exit-0 receipt, and pull
request admission, where CI re-checks that receipt with ` + "`standardsctl forge validate-pr`" + `. A
failing hook blocks the change locally; a missing or invalid receipt fails admission.
[` + hissMatrixPage + `](` + hissMatrixPage + `.md) states the admission rules.
`

	return WikiPage{
		Name:    hissInvariantsPage + ".md",
		Title:   "HISS Invariants",
		Content: content,
	}
}

// generateHISSInvariantsMovedWiki keeps the page's former name published as a pointer. The
// wiki sync removes a page it published before once docs/wiki stops carrying it
// (scripts/sync_github_wiki.sh), so without this stub every existing [HISS-16-Invariants](HISS-16-Invariants.md)
// link and bookmark would break.
func generateHISSInvariantsMovedWiki() WikiPage {
	content := `# Moved: HISS Invariants

This page is now [` + hissInvariantsPage + `](` + hissInvariantsPage + `.md). The standard is named HISS; HISS-16 is only
its context-integrity invariant. [` + hissMatrixPage + `](` + hissMatrixPage + `.md) lists every invariant.
`

	return WikiPage{
		Name:    hissInvariantsMovedPage + ".md",
		Title:   "HISS-16 Invariants (moved)",
		Content: content,
	}
}

// renderHISSMatrixTable renders one row per catalog rule. Gated reports whether AGENTS.md's
// table lists the rule.
func renderHISSMatrixTable(rules []hisscatalog.Rule, gated []hisscatalog.GatedInvariant) string {
	gatedIDs := make(map[string]bool, len(gated))
	for _, row := range gated {
		gatedIDs[row.ID] = true
	}
	rows := make([][]string, 0, len(rules))
	for _, rule := range rules {
		mark := "no"
		if gatedIDs[rule.ID] {
			mark = "yes"
		}
		rows = append(rows, []string{"**" + rule.ID + "**", rule.Title, mark, rule.Enforcement, rule.FailureAction})
	}
	return markdownTable([]string{"Invariant", "Title", "Gated", "Enforcement", "Failure action"}, rows)
}

// hissMatrixAdmission is the matrix page's fixed tail: how a pull request is admitted
// and the verification ladder behind it. It states behaviour of generic adopter CI
// rather than praetor's internal files.
const hissMatrixAdmission = `---

## Pull Request Admission

Every pull request is admitted by ` + "`standardsctl forge validate-pr`" + `, which requires all three
of the following in the PR description:

1. A checked HISS-16 context-integrity box.
2. A checked HISS-15 3D-testing box.
3. A fenced ` + "` ```receipt `" + ` (or ` + "` ~~~receipt `" + `) block carrying the ` + "`.standards-receipt.json`" + `
   envelope produced by ` + "`praetorctl gate run`" + `. The block is parsed as JSON, its Ed25519
   signature is verified against ` + "`receipt.public_key`" + ` pinned in ` + "`.standards.yaml`" + `, its
   recorded output hash is checked against the gate output it carries, that gate output must
   open with ` + "`" + lockdown.GateOutputVersion + "`" + `, and its ` + "`commit_sha`" + ` must equal the pull request head.
   Prose, a bare code block, or the words "Exit-0 Receipt" satisfy nothing. A receipt minted
   before v2 is refused, because its stage lines recorded skipped stages as passed.

A checked box is a task-list item (` + "`- [x]`" + `, ` + "`* [x]`" + `, ` + "`1. [x]`" + `); a ` + "`[x]`" + ` quoted mid-sentence
or inside a code fence is not counted. A description that ends inside an unclosed fence is
rejected, because everything after the opening delimiter renders as code. The rules live in
` + "`internal/forge/pr.go`" + ` and are pinned by ` + "`internal/forge/pr_template_test.go`" + `.

---

## The Verification Ladder

` + figureFenceOpen + "verification-ladder" + figureFenceClose + `

Tier 4, the CI re-check, is the "Validate PR Governance Checklist & Exit-0 Receipts" step in
` + "`.github/workflows/ci.yml`" + `, which runs ` + "`standardsctl forge validate-pr`" + ` as described under
[Pull Request Admission](#pull-request-admission). Adopter CI runs the checks:
` + "`.config/github-app/manifest.json`" + ` specifies an example app but nothing provisions it, and
` + "`internal/forge/pr.go`" + ` requests a review bot.
`

func generateHISSMatrixWiki(repoName string, rules []hisscatalog.Rule, gated []hisscatalog.GatedInvariant) WikiPage {
	content := `# HISS Compliance Matrix

The High-Integrity Systems Standard (HISS) defines ` + catalogRange(rules) + `. This matrix
lists each one for ` + "`" + repoName + "`" + `: its enforcement, its failure action, and whether this
repository's ` + "`AGENTS.md`" + ` gates it ([` + hissInvariantsPage + `](` + hissInvariantsPage + `.md) shows the gated rules). The rows
come from the core HISS rule catalog, the registry the ` + "`standards_explain_rule`" + ` MCP tool serves.
The Enforcement column describes the checks praetor's own repository runs; an adopted repository
runs the checks its adoption generates.

` + renderHISSMatrixTable(rules, gated) + `

` + hissMatrixAdmission

	return WikiPage{
		Name:    hissMatrixPage + ".md",
		Title:   "HISS Matrix",
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
- **Memory & Error Unwraps**: The stricter setting wins (ZeroFrameMalloc, StrictBan).

## Layer Order

` + figureFence("lattice-join") + `

` + "`ResolvePolicy`" + ` folds the layers in a fixed order: the built-in defaults, the profiles and
facets pinned in ` + "`.standards.lock`" + `, the external fleet, organization, deployment and
workstation layers, the repository's ` + "`overrides.complexity`" + `, and, for an audit, the
audit-compatibility ceiling. A profile or facet whose file no longer matches its lock digest is
rejected. The repository's branch-protection and supply-chain overrides apply after the join
(` + "`ApplyOverrides`" + `), so they can only tighten it; ` + "`review_mode`" + ` may relax to
` + "`single_maintainer`" + ` there and nowhere else.
`

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
	"Writes a framework kit's `llms.txt`, `llms-full.txt`, `<dir>/.agents/rules/<kit_name>.md` and " +
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
	"kit_name: example-ui-kit\n" +
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

` + figureFence("forge-federation") + `

praetorctl builds only the GitHub driver: ` + "`sync --remote`" + `, ` + "`issue reconcile`" + ` and ` + "`needs epic --publish`" + ` call ` + "`forge.NewGitHubDriver`" + ` directly. ` + "`forge.NewForge`" + ` is internal and has no production caller, so the GitLab and Gitea drivers are reached only from tests. They check that a token is set and return ` + "`ErrNotImplemented`" + ` from every enforcement method.
`

	return WikiPage{
		Name:    "API-Reference.md",
		Title:   "API Reference",
		Content: content,
	}
}
