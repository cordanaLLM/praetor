// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hisscatalog

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Rule is one invariant of the High-Integrity Systems Standard (HISS). The standard is
// named HISS; HISS-16 is only its context-integrity invariant.
type Rule struct {
	ID            string
	Title         string
	Specification string
	Enforcement   string
	FailureAction string
}

// catalog is the one registry of every HISS invariant, in ascending ID order. The
// standards_explain_rule MCP tool serves it and the generated wiki's HISS matrix renders it,
// so the two can no longer disagree about which invariants exist (HISS-19). It lives outside
// internal/hiss because that package's rule literals are, by contract, the findings Scan()
// reports (TestHISSAuditSkill_Positive_LadderMatchesScan).
var catalog = []Rule{
	{
		ID:            "HISS-01",
		Title:         "Control Flow - Acyclic DAG Control Flow",
		Specification: "Call graphs must form a Directed Acyclic Graph: G = (V, E), ∀v ∈ V, (v, v) ∉ E*\nDirect and mutual recursion are strictly prohibited in production runtimes.",
		Enforcement:   "The internal/hiss scanner, deciding a subset per language: Go goto plus direct and mutual recursion between plain functions (a cycle through methods is not decided); Rust and Python direct recursion only; C and C++ goto only. Each claim replays against .config/hiss/coverage.yaml via 'praetorctl hiss coverage --verify'.",
		FailureAction: "Immediate build failure.",
	},
	{
		ID:            "HISS-02",
		Title:         "Loops & I/O - Bounded Loops & Mandatory I/O Timeouts",
		Specification: "Every loop construct must possess a statically verifiable scalar upper bound: iterations(L) <= N_max.\nUnbounded loops without counter termination are banned. All I/O operations must accept and enforce explicit context.Context deadlines.",
		Enforcement:   "Semgrep rules and AST sweep.",
		FailureAction: "Pre-commit and CI blocker.",
	},
	{
		ID:            "HISS-03",
		Title:         "Zero Frame Malloc",
		Specification: "Hot simulation and frame loops must maintain zero dynamic heap allocations: ΔHeapAlloc_tick = 0.",
		Enforcement:   "Heap benchmark allocations gate.",
		FailureAction: "CI failure.",
	},
	{
		ID:            "HISS-04",
		Title:         "Complexity Bounds & Modular Sizing",
		Specification: "\n  - McCabe Cyclomatic Complexity <= 10\n  - Cognitive Complexity <= 15\n  - Function Length <= 75 LOC\n  - Executable Statements <= 50",
		Enforcement:   "gocyclo, gocognit and funlen via golangci-lint (.golangci.yml), plus the standards_inspect_symbols AST scanner.",
		FailureAction: "Build sweep blocker.",
	},
	{
		ID:            "HISS-05",
		Title:         "Variable Scoping",
		Specification: "Identifiers are declared in the smallest lexical scope that serves them.",
		Enforcement:   "NOT ENFORCED. No executable check exists in this repository, and no configured linter decides this rule.",
		FailureAction: "None today; the rule is advisory until a check is attached.",
	},
	{
		ID:            "HISS-06",
		Title:         "Bounded Concurrency",
		Specification: "Worker pools and concurrent fan-out carry an explicit scalar upper bound.",
		Enforcement:   "NOT ENFORCED for the axiom. The race detector cannot observe an unbounded pool: a lock-order inversion or an unbounded but race-free fan-out produces no data race. 'go test -race' runs, but it does not decide this rule.",
		FailureAction: "None today; the rule is advisory until a check is attached.",
	},
	{
		ID:            "HISS-07",
		Title:         "Checked Errors & Zero Unwrap",
		Specification: "Zero .unwrap() and .expect() in non-test code. Total ban on unchecked Go error returns. All error flows must be handled or wrapped with context.",
		Enforcement:   "golangci-lint, clippy.",
		FailureAction: "Compiler / linter error.",
	},
	{
		ID:            "HISS-08",
		Title:         "Static Determinism & Banned Functions",
		Specification: "Total ban on eval(), exec(), and dynamic runtime code evaluation. Ban on insecure C runtime functions (gets, strcpy, sprintf).",
		Enforcement:   "Semgrep rules.",
		FailureAction: "Admission rejection.",
	},
	{
		ID:            "HISS-09",
		Title:         "Reference Safety & Mandatory Safety Proofs",
		Specification: "Any unsafe block must be preceded by an explanatory '// SAFETY:' comment proving invariants.",
		Enforcement:   "AST check.",
		FailureAction: "Immediate AST check rejection.",
	},
	{
		ID:            "HISS-10",
		Title:         "5-Layer Zero-Warnings Cascade",
		Specification: "Warnings are treated as fatal errors across IDE, Pre-Commit, Pre-Push, CI, and Pre-Apply layers.",
		Enforcement:   "Compile and linter flags (-Werror, zero-warning tolerance).",
		FailureAction: "Exit code 1.",
	},
	{
		ID:            "HISS-11",
		Title:         "Hermetic Supply Chain",
		Specification: "Pinned lockfiles mandatory. Zero floating tags. SLSA Level 3 provenance attestations and Sigstore Cosign signatures.",
		Enforcement:   "CI attestation gate.",
		FailureAction: "Deployment rejection.",
	},
	{
		ID:            "HISS-12",
		Title:         "Secret Leak Prevention",
		Specification: "Zero credentials in Git history.",
		Enforcement:   "'make secrets' runs gitleaks over repository history inside verify-all.",
		FailureAction: "Verification gate rejection.",
	},
	{
		ID:            "HISS-13",
		Title:         "Monotonic Debt Ratchet",
		Specification: "Total recorded infractions never grow against the committed baseline; an increase requires a deliberately recorded rationale.",
		Enforcement:   "'praetorctl baseline' and the gate's HISS stage, evaluated against .standards-baseline.json. The scan feeding it refuses to certify a scope it did not fully examine.",
		FailureAction: "PR status gate rejection.",
	},
	{
		ID:            "HISS-14",
		Title:         "Append-Only ABI & Migration Footers",
		Specification: "Public APIs are append-only. Breaking changes require conventional commit breaking indicator (!) and mandatory Migration: footer.",
		Enforcement:   "Git log and API diff analyzer.",
		FailureAction: "PR blocker.",
	},
	{
		ID:            "HISS-15",
		Title:         "3D Test Discipline",
		Specification: "Mandatory Positive, Negative, and Boundary tests for all public interfaces. Touched-file clean rule enforced.",
		Enforcement:   "CI coverage gate (go test -race -coverprofile with a minimum statement-coverage floor enforced by 'go tool cover') and PR checklist validation of the 3D test attestation.",
		FailureAction: "Merge gate rejection.",
	},
	{
		ID:            "HISS-16",
		Title:         "Canonical AGENTS.md & Server Gates",
		Specification: "Single source of agent instructions (AGENTS.md). Vendor targets compiled via praetorctl compile-context. Sandboxed verification.",
		Enforcement:   "Pre-commit blocker, server-side admission.",
		FailureAction: "Merge blocker.",
	},
	{
		ID:            "HISS-17",
		Title:         "State Ledger Discipline",
		Specification: "Every agent turn starts with 'praetorctl state status' and the open tasks in .workingdir/OPEN.md, never a read of the whole .workingdir/STATE.md; tasks are tracked via 'praetorctl state task'; every turn ends with 'praetorctl state sync .'.",
		Enforcement:   "Pre-commit state-sync hook and the CI / pre-push state audit.",
		FailureAction: "Pre-commit / CI gate rejection.",
	},
	{
		ID:            "HISS-18",
		Title:         "CI Efficiency",
		Specification: "Diff-aware change gating: heavy race and security gates are skipped on docs-only or state-only changes as classified by 'praetorctl ci filter'.",
		Enforcement:   "CI filter step exporting run_* outputs that every heavy gate's condition consumes.",
		FailureAction: "CI optimization gate.",
	},
	{
		ID:            "HISS-19",
		Title:         "Reuse Before Writing",
		Specification: "One behavior has exactly one implementation. An existing function, loader, parser or command is extended or called rather than reimplemented, and configuration formats are held to the same rule: a second config system beside an existing loader is the same defect. Duplication that is genuinely unavoidable is justified in the commit body.",
		Enforcement:   "'praetorctl dedupe scan .' function-level clone and utility-sprawl detection, run by 'make dedupe' inside verify-all.",
		FailureAction: "Verification gate rejection.",
	},
	{
		ID:            "HISS-20",
		Title:         "Replayable Enforcement Evidence",
		Specification: "Every rule carries fixtures replayed in both directions: a claim of enforcement must report each of its positive fixtures, and a claim of absence must leave its gap fixtures undetected. A coverage claim is reproducible, never asserted.",
		Enforcement:   "'praetorctl hiss coverage --verify', run inside verify-all against '.config/hiss/coverage.yaml'.",
		FailureAction: "Verification gate rejection.",
	},
	{
		ID:            "HISS-21",
		Title:         "Platform Neutrality",
		Specification: "Gates, hooks and emitted templates run on Linux, macOS and Windows, or declare the platform they require and skip with a stated reason where it is absent. A gate that cannot run is not a passing gate.",
		Enforcement:   "Platform Neutrality matrix in CI.",
		FailureAction: "Verification gate rejection.",
	},
}

// Rules returns every HISS invariant in ascending ID order. The slice is a copy, so a
// caller cannot edit the registry through it.
func Rules() []Rule {
	return slices.Clone(catalog)
}

// RuleIDs returns every HISS invariant ID in ascending order.
func RuleIDs() []string {
	ids := make([]string, 0, len(catalog))
	for _, rule := range catalog {
		ids = append(ids, rule.ID)
	}
	return ids
}

// LookupRule returns the invariant with the given ID. The ID is matched exactly; callers
// normalise case and whitespace first.
func LookupRule(id string) (Rule, bool) {
	for _, rule := range catalog {
		if rule.ID == id {
			return rule, true
		}
	}
	return Rule{}, false
}

// Explanation renders the rule as the standards_explain_rule MCP tool returns it. A
// specification that opens with a line break is a list and starts on its own line.
func (r Rule) Explanation() string {
	separator := " "
	if strings.HasPrefix(r.Specification, "\n") {
		separator = ""
	}
	return "Rule: " + r.ID + " (" + r.Title + ")\n" +
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

// gatedInvariantRow matches one row of that table, for example
// "| **HISS-01** control flow | recursion prohibited; call graph = DAG | build | immediate
// build failure |". A cell may carry an escaped pipe.
var gatedInvariantRow = regexp.MustCompile(
	`^\|\s*\*\*(HISS-\d+)\*\*\s*((?:\\\||[^|])*?)\s*\|\s*((?:\\\||[^|])*?)\s*\|\s*((?:\\\||[^|])*?)\s*\|\s*((?:\\\||[^|])*?)\s*\|$`)

// ParseGatedInvariants reads the invariant rows of agentsMD's "Core Directives &
// Invariants" section, in source order. The section ends at the next heading; fenced code
// is skipped through util.MarkdownFence, so a shell comment in an example never reads as a
// heading. Every row must name an invariant the catalog defines, exactly once, and the
// section must hold at least one row: a table that cannot be read is an error, never an
// empty result.
func ParseGatedInvariants(agentsMD string) ([]GatedInvariant, error) {
	lines := strings.Split(agentsMD, "\n")
	if len(lines) > maxGatedTableLines {
		return nil, fmt.Errorf("%w: %d lines, limit %d", ErrGatedTableTooLong, len(lines), maxGatedTableLines)
	}
	var (
		rows      []GatedInvariant
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
		if row, ok := parseGatedInvariantRow(line); ok && inSection {
			rows = append(rows, row)
		}
	}
	if err := validateGatedInvariants(rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// parseGatedInvariantRow decodes one table line, reporting false for any line that is not
// an invariant row (the header, the delimiter row, prose).
func parseGatedInvariantRow(line string) (GatedInvariant, bool) {
	m := gatedInvariantRow.FindStringSubmatch(line)
	if m == nil {
		return GatedInvariant{}, false
	}
	return GatedInvariant{
		ID:          m[1],
		Scope:       unescapeTableCell(m[2]),
		Rule:        unescapeTableCell(m[3]),
		Enforcement: unescapeTableCell(m[4]),
		OnFail:      unescapeTableCell(m[5]),
	}, true
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
