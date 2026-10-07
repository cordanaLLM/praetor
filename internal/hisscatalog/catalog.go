// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Rule is one invariant of the High-Integrity Systems Standard (HISS). The standard is
// named HISS; HISS-16 is only its context-integrity invariant.
//
// Title, Specification, Enforcement and FailureAction explain the rule as praetor's own
// repository enforces it. Scope, Directive and Adoption state it for an adopted repository:
// the invariant table of the AGENTS.md harness praetorctl adopt writes renders one row per
// rule from them, and standards_explain_rule closes its answer with Adoption.
type Rule struct {
	ID            string
	Title         string
	Specification string
	Enforcement   string
	FailureAction string
	// Scope is the short area label that opens the rule's row in an adopted AGENTS.md.
	Scope string
	// Directive is the rule that row prints, as clauses AdoptedDirective joins for one
	// repository: a clause that names one language's construct renders only where that
	// language is present (#68). It is agent-only text, so it is written in the internal
	// register and must pass the caveman lint.
	Directive []Clause
	// Adoption is the check an adopted repository gets from what adoption generates (the
	// Makefile verify-all target and the lefthook configuration) and the stages that run it.
	// The zero value means adoption generates none, and every surface must say the rule is not
	// enforced rather than borrow praetor's own mechanism (BUG-804).
	Adoption AdoptedCheck
}

// hiss04Specification lists HISS-04's caps. Its function length is the audit ceiling itself
// (hiss.DefaultMaxFuncLOC, which config.AuditMaxFuncLOC reads), not a copy of it: the copy
// said 75 while the audit enforced 60 (#574).
var hiss04Specification = "\n  - McCabe Cyclomatic Complexity <= 10\n  - Cognitive Complexity <= 15\n  - Function Length <= " +
	strconv.Itoa(hiss.DefaultMaxFuncLOC) + " LOC\n  - Executable Statements <= 50"

// catalog is the one registry of every HISS invariant, in ascending ID order. The
// standards_explain_rule MCP tool serves it, the generated wiki's HISS matrix renders it, the
// adopted AGENTS.md harness and ADR index cite it, and .config/hiss/coverage.yaml may declare
// evidence only for its IDs, so no two surfaces can disagree about which invariants exist
// (HISS-19). It lives outside
// internal/hiss because that package's rule literals are, by contract, the findings Scan()
// reports (TestHISSAuditSkill_Positive_LadderMatchesScan).
var catalog = []Rule{
	{
		ID:            "HISS-01",
		Title:         "Control Flow - Acyclic DAG Control Flow",
		Specification: "Call graphs must form a Directed Acyclic Graph: G = (V, E), ∀v ∈ V, (v, v) ∉ E*\nDirect and mutual recursion are strictly prohibited in production runtimes.",
		Enforcement:   "The internal/hiss scanner, deciding a subset per language. Go: goto, direct recursion, and mutual or indirect recursion between plain functions (a cycle through methods is not decided). Rust, Python, JavaScript, TypeScript, Svelte and shell: direct recursion only, shell including the run: blocks of GitHub Actions workflows. C and C++: goto only, less the single-level forward cleanup gotos a declared hiss.exceptions.c_goto_cleanup accepts (hiss.CleanupGoto). Each claim replays against .config/hiss/coverage.yaml via 'praetorctl hiss coverage --verify'.",
		FailureAction: "Immediate build failure.",
		Scope:         "control flow",
		Directive: []Clause{
			{Text: "recursion prohibited; call graph = DAG"},
			{Languages: LanguageGo | LanguageC, Text: "zero `goto`", Waiver: ExceptionCleanupGoto},
			{Languages: LanguageC, Exception: ExceptionCleanupGoto,
				Text: hiss.CleanupGotoRule() + " (declared exception); audit reports every other `goto`"},
		},
		Adoption: auditCheck(scannedLanguages, "Go `goto`, recursion + plain-function call cycles; Rust, Python direct recursion; C `goto`"),
	},
	{
		ID:            "HISS-02",
		Title:         "Loops & I/O - Bounded Loops & Mandatory I/O Timeouts",
		Specification: "Every loop construct must possess a statically verifiable scalar upper bound: iterations(L) <= N_max.\nUnbounded loops without counter termination are banned. All I/O operations must accept and enforce explicit context.Context deadlines.",
		Enforcement:   "The internal/hiss scanner, deciding a subset per language. Go: a for statement without a condition, a context without a deadline reaching a call, and the context-less exec.Command, net.Dial and http.Get families, outside tests and main.main. Rust, Python, C, JavaScript, TypeScript and Svelte: unbounded loop shapes only. Shell, including the run: blocks of GitHub Actions workflows: unbounded loop shapes and a curl transfer without --max-time. systemd units: a oneshot service without a start timeout, a disabled timeout, and a restarting service without a start rate limit. Each claim replays against .config/hiss/coverage.yaml via 'praetorctl hiss coverage --verify'.",
		FailureAction: "Pre-commit and CI blocker.",
		Scope:         "loops, I/O",
		Directive: []Clause{
			{Text: "scalar upper bound on every loop; explicit deadline on every I/O call"},
			{Languages: LanguageGo, Text: "I/O takes `context.Context` deadline"},
		},
		Adoption: auditCheck(scannedLanguages, "unbounded loop shapes in Go, C, Rust, Python; I/O deadlines unchecked"),
	},
	{
		ID:            "HISS-03",
		Title:         "Zero Frame Malloc",
		Specification: "Hot simulation and frame loops must maintain zero dynamic heap allocations: ΔHeapAlloc_tick = 0.",
		Enforcement:   "NOT ENFORCED. No allocation benchmark gate exists in this repository.",
		FailureAction: "None today; the rule is advisory until a check is attached.",
		Scope:         "memory",
		Directive:     []Clause{{Languages: LanguageGo | LanguageRust | LanguageC, Text: "zero heap allocation in hot simulation/tick loops"}},
	},
	{
		ID:            "HISS-04",
		Title:         "Complexity Bounds & Modular Sizing",
		Specification: hiss04Specification,
		Enforcement:   "gocyclo, gocognit and funlen via golangci-lint (.golangci.yml) at its configured thresholds; the HISS scanner enforces function length and measures cyclomatic, cognitive and statement counts without enforcing them, the measurement standards_inspect_symbols and standards-lsp share.",
		FailureAction: "Build sweep blocker.",
		Scope:         "complexity",
		Directive: []Clause{
			{Text: "McCabe cyclomatic <= %d, cognitive <= %d, statements <= %d", Complexity: true},
			{Text: "func LOC <=", FuncLOC: true},
		},
		Adoption: auditCheck(scannedLanguages, "function length only; other caps need repository linter"),
	},
	{
		ID:            "HISS-05",
		Title:         "Variable Scoping",
		Specification: "Identifiers are declared in the smallest lexical scope that serves them.",
		Enforcement:   "NOT ENFORCED. No executable check exists in this repository, and no configured linter decides this rule.",
		FailureAction: "None today; the rule is advisory until a check is attached.",
		Scope:         "scoping",
		Directive:     []Clause{{Text: "declare every identifier in smallest lexical scope serving it"}},
	},
	{
		ID:            "HISS-06",
		Title:         "Bounded Concurrency",
		Specification: "Worker pools and concurrent fan-out carry an explicit scalar upper bound.",
		Enforcement:   "NOT ENFORCED for the axiom. The race detector cannot observe an unbounded pool: a lock-order inversion or an unbounded but race-free fan-out produces no data race. 'go test -race' runs, but it does not decide this rule.",
		FailureAction: "None today; the rule is advisory until a check is attached.",
		Scope:         "concurrency",
		Directive:     []Clause{{Text: "explicit scalar upper bound on every worker pool + concurrent fan-out"}},
	},
	{
		ID:            "HISS-07",
		Title:         "Checked Errors & Zero Unwrap",
		Specification: "Zero .unwrap() and .expect() in non-test code. Total ban on unchecked Go error returns. All error flows must be handled or wrapped with context.",
		Enforcement:   "golangci-lint (errcheck and wrapping rules) in make lint.",
		FailureAction: "Compiler / linter error.",
		Scope:         "errors",
		Directive: []Clause{
			{Text: "every error handled or wrapped with context"},
			{Languages: LanguageGo, Text: "zero unchecked `error` return"},
			{Languages: LanguageRust, Text: "zero `.unwrap()` / `.expect()` outside tests"},
		},
		Adoption: auditCheck(LanguageGo|LanguageRust|LanguagePython, "partial in Go, Rust, Python"),
	},
	{
		ID:            "HISS-08",
		Title:         "Static Determinism & Banned Functions",
		Specification: "Total ban on eval(), exec(), and dynamic runtime code evaluation. Ban on insecure C runtime functions (gets, strcpy, sprintf).",
		Enforcement:   "Semgrep rules.",
		FailureAction: "Admission rejection.",
		Scope:         "determinism",
		Directive: []Clause{
			{Text: "zero dynamic code execution (`eval` / `exec`)"},
			{Languages: LanguageC, Text: "zero banned libc (`gets` / `strcpy` / `sprintf`)"},
		},
		Adoption: auditCheck(LanguageC|LanguagePython, "C banned calls, Python `eval` / `exec`; Go unchecked"),
	},
	{
		ID:            "HISS-09",
		Title:         "Reference Safety & Mandatory Safety Proofs",
		Specification: "Any unsafe block must be preceded by an explanatory '// SAFETY:' comment proving invariants.",
		Enforcement:   "AST check.",
		FailureAction: "Immediate AST check rejection.",
		Scope:         "reference safety",
		Directive:     []Clause{{Languages: LanguageGo | LanguageRust, Text: "`// SAFETY:` proof before every `unsafe` block"}},
		Adoption:      auditCheck(LanguageGo|LanguageRust, "Go, Rust `unsafe` without proof; C, Python unchecked"),
	},
	{
		ID:            "HISS-10",
		Title:         "5-Layer Zero-Warnings Cascade",
		Specification: "Warnings are treated as fatal errors across IDE, Pre-Commit, Pre-Push, CI, and Pre-Apply layers.",
		Enforcement:   "go vet and golangci-lint in make lint; any finding fails the run.",
		FailureAction: "Exit code 1.",
		Scope:         "warnings",
		Directive:     []Clause{{Text: "zero warnings: compiler, linter, format sweeps"}},
		Adoption:      AdoptedCheck{Check: "`go vet` + `gofmt`", Coverage: "Go only", Trigger: "vet finding", Stages: StagePreCommit, Languages: LanguageGo},
	},
	{
		ID:            "HISS-11",
		Title:         "Hermetic Supply Chain",
		Specification: "Pinned lockfiles mandatory. Zero floating tags. SLSA Level 3 provenance attestations and Sigstore Cosign signatures.",
		Enforcement:   "CI attestation gate.",
		FailureAction: "Deployment rejection.",
		Scope:         "supply chain",
		Directive:     []Clause{{Text: "pinned lockfiles; zero floating tags; signed provenance"}},
	},
	{
		ID:            "HISS-12",
		Title:         "Secret Leak Prevention",
		Specification: "Zero credentials in Git history.",
		Enforcement:   "'make secrets' runs gitleaks over repository history inside verify-all.",
		FailureAction: "Verification gate rejection.",
		Scope:         "secrets",
		Directive:     []Clause{{Text: "zero credentials in Git history"}},
	},
	{
		ID:            "HISS-13",
		Title:         "Monotonic Debt Ratchet",
		Specification: "Total recorded infractions never grow against the committed baseline; an increase requires a deliberately recorded rationale.",
		Enforcement:   "'praetorctl baseline' and the gate's HISS stage, evaluated against .standards-baseline.json. The scan feeding it refuses to certify a scope it did not fully examine.",
		FailureAction: "PR status gate rejection.",
		Scope:         "debt ratchet",
		Directive:     []Clause{{Text: "recorded infractions never grow vs committed baseline"}},
		Adoption:      AdoptedCheck{Check: "`praetorctl audit` ratchet vs `.standards-baseline.json`", Trigger: "growth", Stages: auditStages},
	},
	{
		ID:            "HISS-14",
		Title:         "Append-Only ABI & Migration Footers",
		Specification: "Public APIs are append-only. Breaking changes require conventional commit breaking indicator (!) and mandatory Migration: footer.",
		Enforcement:   "praetorctl forge check-commits in CI, over every commit of a pull request, and praetorctl forge check-message in a commit-msg hook reject a breaking-change marker without a Migration: footer, whatever the language, and a subject that is not a Conventional Commits subject (build, chore, ci, docs, feat, fix, perf, refactor, revert, style or test, an optional scope and !) or one git wrote for a merge or a revert. Where api:public-contract is declared and git tracks a go.mod, the Go API Compatibility workflow (.github/workflows/praetor-api.yml) runs tools/apicompat/gate/main.go, which compares the exported API of every Go module with the base using go-apidiff: before a v1 root release tag it reports an incompatible change, from v1 on it rejects one, and a comparison that did not run fails. No other language's API is compared.",
		FailureAction: "PR blocker.",
		Scope:         "append-only ABI",
		Directive:     []Clause{{Text: "public API append-only; breaking change = `!` subject + `Migration:` footer"}},
	},
	{
		ID:            "HISS-15",
		Title:         "3D Test Discipline",
		Specification: "Mandatory Positive, Negative, and Boundary tests for all public interfaces. Touched-file clean rule enforced.",
		Enforcement:   "CI coverage gate (go test -race -coverprofile with a minimum statement-coverage floor enforced by 'go tool cover') and PR checklist validation of the 3D test attestation.",
		FailureAction: "Merge gate rejection.",
		Scope:         "3D testing",
		Directive:     []Clause{{Text: "positive + negative + boundary tests, every public interface"}},
	},
	{
		ID:            "HISS-16",
		Title:         "Canonical AGENTS.md & Server Gates",
		Specification: "Single source of agent instructions (AGENTS.md). Vendor targets compiled via praetorctl compile-context. Sandboxed verification.",
		Enforcement:   "Pre-commit blocker, server-side admission.",
		FailureAction: "Merge blocker.",
		Scope:         "context integrity",
		Directive:     []Clause{{Text: "single canonical `AGENTS.md`; vendor files compiled via `praetorctl compile-context`"}},
		Adoption:      AdoptedCheck{Check: "`praetorctl compile-context --verify`", Trigger: "drift", Stages: StageVerifyAll | StagePreCommit},
	},
	{
		ID:            "HISS-17",
		Title:         "State Ledger Discipline",
		Specification: "Every agent turn starts with 'praetorctl state status' and the open tasks in .workingdir/OPEN.md, never a read of the whole .workingdir/STATE.md; tasks are tracked via 'praetorctl state task'; every turn ends with 'praetorctl state sync .'.",
		Enforcement:   "Pre-commit state-sync hook and the CI / pre-push state audit.",
		FailureAction: "Pre-commit / CI gate rejection.",
		Scope:         "state ledger",
		Directive:     []Clause{{Text: "turn start `praetorctl state status`; turn end `praetorctl state sync .`"}},
		Adoption:      AdoptedCheck{Check: "`praetorctl state sync .`", Stages: StagePostCommit},
	},
	{
		ID:            "HISS-18",
		Title:         "CI Efficiency",
		Specification: "Diff-aware change gating: heavy race and security gates are skipped on docs-only or state-only changes as classified by 'praetorctl ci filter'.",
		Enforcement:   "CI filter step exporting run_* outputs that every heavy gate's condition consumes.",
		FailureAction: "CI optimization gate.",
		Scope:         "CI efficiency",
		Directive:     []Clause{{Text: "diff-aware gating via `praetorctl ci filter`"}},
	},
	{
		ID:            "HISS-19",
		Title:         "Reuse Before Writing",
		Specification: "One behavior has exactly one implementation. An existing function, loader, parser or command is extended or called rather than reimplemented, and configuration formats are held to the same rule: a second config system beside an existing loader is the same defect. Duplication that is genuinely unavoidable is justified in the commit body.",
		Enforcement:   "'praetorctl dedupe scan .' function-level clone and utility-sprawl detection, run by 'make dedupe' inside verify-all.",
		FailureAction: "Verification gate rejection.",
		Scope:         "reuse before writing",
		Directive:     []Clause{{Text: "one behavior = one implementation; extend or call existing code"}},
	},
	{
		ID:            "HISS-20",
		Title:         "Replayable Enforcement Evidence",
		Specification: "Every rule carries fixtures replayed in both directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence must leave its gap fixtures undetected. A coverage claim is reproducible, never asserted.",
		Enforcement:   "'praetorctl hiss coverage --verify', run inside verify-all against '.config/hiss/coverage.yaml'.",
		FailureAction: "Verification gate rejection.",
		Scope:         "replayable evidence",
		Directive:     []Clause{{Text: "every enforcement claim backed by fixtures replayed both directions"}},
	},
	{
		ID:            "HISS-21",
		Title:         "Platform Neutrality",
		Specification: "Gates, hooks and emitted templates run on Linux, macOS and Windows, or declare the platform they require and skip with a stated reason where it is absent. A gate that cannot run is not a passing gate.",
		Enforcement:   "Platform Neutrality matrix in CI.",
		FailureAction: "Verification gate rejection.",
		Scope:         "platform neutrality",
		Directive:     []Clause{{Text: "gates, hooks, emitted templates run on Linux, macOS, Windows, or skip with stated reason"}},
	},
}

// Rules returns every HISS invariant in ascending ID order. The slice and each rule's
// directive clauses are copies, so a caller cannot edit the registry through them.
func Rules() []Rule {
	rules := slices.Clone(catalog)
	for i := range rules {
		rules[i].Directive = slices.Clone(rules[i].Directive)
	}
	return rules
}

// RuleIDs returns every HISS invariant ID in ascending order.
func RuleIDs() []string {
	ids := make([]string, 0, len(catalog))
	for _, rule := range catalog {
		ids = append(ids, rule.ID)
	}
	return ids
}

// LookupRule returns the invariant with the given ID, its directive clauses copied. The ID is
// matched exactly; callers normalise case and whitespace first.
func LookupRule(id string) (Rule, bool) {
	for _, rule := range catalog {
		if rule.ID == id {
			rule.Directive = slices.Clone(rule.Directive)
			return rule, true
		}
	}
	return Rule{}, false
}

// Reference names the rule with its title, for prose that cites it.
func (r Rule) Reference() string {
	return r.ID + " (" + r.Title + ")"
}

// Explanation renders the rule as praetor's own repository enforces it; AdoptedExplanation
// adds what an adopted repository enforces. A
// specification that opens with a line break is a list and starts on its own line.
func (r Rule) Explanation() string {
	separator := " "
	if strings.HasPrefix(r.Specification, "\n") {
		separator = ""
	}
	return "Rule: " + r.Reference() + "\n" +
		"Formal Specification:" + separator + r.Specification + "\n" +
		"Enforcement: " + r.Enforcement + "\n" +
		"Failure Action: " + r.FailureAction
}

// GatedInvariantsHeading opens the table in AGENTS.md that lists the invariants a
// repository gates. A harness written by praetorctl adopt suffixes the heading, so it is
// matched as a prefix.
const GatedInvariantsHeading = "## Core Directives & Invariants"

// maxGatedTableLines bounds the lines ParseGatedInvariants reads (HISS-02). compile-context
// refuses an AGENTS.md over 1 MiB, so no canonical file comes near it.
const maxGatedTableLines = 1 << 20

var (
	// ErrNoGatedInvariants reports an AGENTS.md without a single invariant row under
	// GatedInvariantsHeading.
	ErrNoGatedInvariants = errors.New("hisscatalog: AGENTS.md carries no invariant rows under " + GatedInvariantsHeading)
	// ErrUnknownInvariant reports a gated row whose ID the catalog does not define.
	ErrUnknownInvariant = errors.New("hisscatalog: AGENTS.md gates an invariant the HISS catalog does not define")
	// ErrDuplicateInvariant reports a gated row whose ID an earlier row already gates.
	ErrDuplicateInvariant = errors.New("hisscatalog: AGENTS.md gates an invariant twice")
	// ErrGatedTableTooLong reports a document with more lines than the parser reads.
	ErrGatedTableTooLong = errors.New("hisscatalog: AGENTS.md is too long to read its invariant table")
)

// GatedInvariant is one row of AGENTS.md's "Core Directives & Invariants" table. Scope is
// the short descriptor after the ID in the first cell; Rule, Enforcement and OnFail are the
// remaining cells, with escaped pipes restored.
type GatedInvariant struct {
	ID          string
	Scope       string
	Rule        string
	Enforcement string
	OnFail      string
}

// InvariantRow is one row of the invariant table as SplitInvariantRow splits it.
type InvariantRow struct {
	// Line is the row as written, surrounding whitespace trimmed.
	Line string
	// ID is the bold identifier that opens the first cell, such as HISS-02 or ACME-01.
	ID string
	// Cells are the text after the ID in the first cell (the scope), then every further cell,
	// each trimmed, escaped pipes restored.
	Cells []string
}

// invariantIDCell matches the first cell of an invariant row: a bold identifier without
// whitespace, then the scope, for example "**HISS-01** control flow".
var invariantIDCell = regexp.MustCompile(`^\*\*([^*\s]+)\*\*\s*(.*)$`)

// gatedInvariantID is the ID grammar of a catalog row. A row under any other ID, such as a
// repository's own ACME-01, is the repository's and never gated.
var gatedInvariantID = regexp.MustCompile(`^HISS-\d+$`)

// gatedInvariantCells is the cell count of a catalog row: scope, rule, enforcement, on fail.
const gatedInvariantCells = 4

// SplitInvariantRow reads line as a row of the invariant table: a line that opens and closes
// with a pipe and whose first cell opens with a bold ID, for example "| **HISS-01** control
// flow | recursion prohibited | build | immediate build failure |". A backslash before a pipe
// escapes it. It reports false for any other line (the header, the delimiter row, prose) and
// checks no catalog, so a repository's own row, such as "| **ACME-01** ... |", splits too.
// ParseGatedInvariants and the adopt harness refresh both read rows through it.
func SplitInvariantRow(line string) (InvariantRow, bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 || line[0] != '|' {
		return InvariantRow{}, false
	}
	cells, ok := splitTableCells(line[1:])
	if !ok {
		return InvariantRow{}, false
	}
	m := invariantIDCell.FindStringSubmatch(cells[0])
	if m == nil {
		return InvariantRow{}, false
	}
	cells[0] = m[2]
	for i := range cells {
		cells[i] = unescapeTableCell(cells[i])
	}
	return InvariantRow{Line: line, ID: m[1], Cells: cells}, true
}

// splitTableCells splits rest, a row after its opening pipe, at every unescaped pipe into
// trimmed cells. It reports false when rest does not close with a pipe or holds no cell.
func splitTableCells(rest string) ([]string, bool) {
	var cells []string
	start, escaped := 0, false
	for i := 0; i < len(rest); i++ {
		switch {
		case escaped:
			escaped = false
		case rest[i] == '\\':
			escaped = i+1 < len(rest) && rest[i+1] == '|'
		case rest[i] == '|':
			cells = append(cells, strings.TrimSpace(rest[start:i]))
			start = i + 1
		}
	}
	if start != len(rest) || len(cells) == 0 {
		return nil, false
	}
	return cells, true
}

// InvariantTableRows returns every row of agentsMD's "Core Directives & Invariants" section
// that SplitInvariantRow reads, in source order, whatever its ID. The section ends at the next
// heading; fenced code is skipped through util.MarkdownFence, so a shell comment in an example
// never reads as a heading. It checks no catalog: a document without the section, or without a
// row in it, returns no rows and no error.
func InvariantTableRows(agentsMD string) ([]InvariantRow, error) {
	lines := strings.Split(agentsMD, "\n")
	if len(lines) > maxGatedTableLines {
		return nil, fmt.Errorf("%w: %d lines, limit %d", ErrGatedTableTooLong, len(lines), maxGatedTableLines)
	}
	var (
		rows      []InvariantRow
		fence     util.MarkdownFence
		inSection bool
	)
	for i := 0; i < len(lines) && i < maxGatedTableLines; i++ {
		line := strings.TrimSpace(lines[i])
		if fence.Inside(line) {
			continue
		}
		if strings.HasPrefix(line, "#") {
			if inSection {
				break
			}
			inSection = strings.HasPrefix(line, GatedInvariantsHeading)
			continue
		}
		if row, ok := SplitInvariantRow(line); ok && inSection {
			rows = append(rows, row)
		}
	}
	return rows, nil
}

// ParseGatedInvariants reads the catalog rows of agentsMD's "Core Directives & Invariants"
// section (InvariantTableRows), in source order: rows under a HISS ID with the four cells of
// the table. A row under another ID is the repository's own and is skipped. Every catalog row
// must name an invariant the catalog defines, exactly once, and the section must hold at least
// one: a table that cannot be read is an error, never an empty result.
func ParseGatedInvariants(agentsMD string) ([]GatedInvariant, error) {
	table, err := InvariantTableRows(agentsMD)
	if err != nil {
		return nil, err
	}
	var rows []GatedInvariant
	for _, row := range table {
		if gated, ok := gatedInvariant(row); ok {
			rows = append(rows, gated)
		}
	}
	if err := validateGatedInvariants(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// InCatalogNamespace reports whether id has the ID grammar of a catalog row (HISS-<n>), whether
// or not the catalog defines it. A row under such an ID is the catalog's: ParseGatedInvariants
// reads it and refuses one the catalog does not define. A row under any other ID is the
// repository's own.
func InCatalogNamespace(id string) bool {
	return gatedInvariantID.MatchString(id)
}

// gatedInvariant returns row as a catalog row, reporting false for a row under another ID or
// with another cell count.
func gatedInvariant(row InvariantRow) (GatedInvariant, bool) {
	if !InCatalogNamespace(row.ID) || len(row.Cells) != gatedInvariantCells {
		return GatedInvariant{}, false
	}
	return GatedInvariant{ID: row.ID, Scope: row.Cells[0], Rule: row.Cells[1], Enforcement: row.Cells[2], OnFail: row.Cells[3]}, true
}

func unescapeTableCell(cell string) string {
	return strings.ReplaceAll(cell, `\|`, "|")
}

// validateGatedInvariants holds the parsed rows to the catalog: at least one row, every ID
// defined, none repeated.
func validateGatedInvariants(rows []GatedInvariant) error {
	if len(rows) == 0 {
		return ErrNoGatedInvariants
	}
	seen := make(map[string]bool, len(rows))
	for _, row := range rows {
		if _, ok := LookupRule(row.ID); !ok {
			return fmt.Errorf("%w: %s", ErrUnknownInvariant, row.ID)
		}
		if seen[row.ID] {
			return fmt.Errorf("%w: %s", ErrDuplicateInvariant, row.ID)
		}
		seen[row.ID] = true
	}
	return nil
}
