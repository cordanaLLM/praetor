package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/mcp"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// forceContractClause pairs one clause of adopt.ForceContract, which adopt --help prints, with its
// restatement in the standards_adopt force description, which says it in the agent register.
type forceContractClause struct {
	cli, mcp string
}

// forceContractClauses are the clauses of the --force contract, in order: the lock rebuild and the
// source it needs, the audit-locked rewrite, the editor JSON merge, the harness refresh, the
// Paperclip platform reset, the report and backup, and what stays kept.
var forceContractClauses = []forceContractClause{
	{"rebuild .standards.lock and its pinned catalog from the lock source",
		"Rebuild .standards.lock + pinned catalog from source_root (required, dry_run included)"},
	{"rewrite each drifted file audit compares byte for byte (documentation gate files, the branch ruleset while the " +
		"policy requires one, the DevContainer, an edited Makefile or .gitattributes managed block)",
		"rewrite drifted files audit compares byte for byte (documentation gate, branch ruleset while policy requires " +
			"one, DevContainer, edited Makefile or .gitattributes managed block)"},
	{"merge managed values into editor JSON", "merge managed values into editor JSON"},
	{"regenerate the AGENTS.md harness, keeping the repository's additions",
		"regenerate AGENTS.md harness, repository additions kept"},
	{"reset a Paperclip platform that names another repository to this repository",
		"reset Paperclip platform naming another repository to this repository"},
	{"Each such file is reported as replace, or merge for editor JSON, with its line delta, and backed up under " +
		".workingdir/adopt-backups when git ignores that path",
		"Replaced or merged file: line delta + backup under .workingdir/adopt-backups when git ignores backup path"},
	{"Every other file audit leaves unverified stays as it is: delete one and re-run adopt to regenerate it",
		"Other files audit never verifies: kept; delete one, rerun adopt to regenerate"},
}

// forceContractClauseCount counts the clauses a contract text states: the parts its sentences and
// semicolons separate.
func forceContractClauseCount(contract string) int {
	return strings.Count(contract, "; ") + strings.Count(contract, ". ") + 1
}

// forceContractDrift names every way contract (the CLI text) and description (the MCP text) have
// drifted apart from clauses: a clause one of them no longer states, or a contract clause no pair
// restates. It returns nil when both state every clause.
func forceContractDrift(contract, description string, clauses []forceContractClause) []string {
	var drift []string
	if got := forceContractClauseCount(contract); got != len(clauses) {
		drift = append(drift, "the CLI contract states "+strconv.Itoa(got)+" clauses, the MCP restates "+
			strconv.Itoa(len(clauses)))
	}
	for _, clause := range clauses {
		if !strings.Contains(contract, clause.cli) {
			drift = append(drift, "the CLI contract no longer states "+clause.cli)
		}
		if !strings.Contains(description, clause.mcp) {
			drift = append(drift, "the MCP force description no longer restates "+clause.cli)
		}
	}
	return drift
}

// adoptTool returns the standards_adopt tool a fresh server registers.
func adoptTool(t *testing.T) mcp.Tool {
	t.Helper()
	srv, err := NewServer(t.TempDir(), "v")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := srv.tools["standards_adopt"]
	if !ok {
		t.Fatal("standards_adopt is not registered")
	}
	if _, ok := tool.InputSchema.Properties["force"]; !ok {
		t.Fatal("standards_adopt has no force property")
	}
	return tool
}

// TestCreateAdoptTool_ForceStatesTheContract (#502): the standards_adopt force property restates
// every clause of adopt.ForceContract, the text the CLI flag help prints, so the two cannot drift
// (positive); the reset wording earlier releases served is gone, and source_root no longer reads as
// optional, since force needs it (negative); force stays a boolean that defaults to false, and the
// tool keeps its destructive, idempotent, closed-world annotations, since a forced run still
// replaces bytes (boundary).
func TestCreateAdoptTool_ForceStatesTheContract(t *testing.T) {
	tool := adoptTool(t)
	force := tool.InputSchema.Properties["force"]
	description := force.Description
	if drift := forceContractDrift(adopt.ForceContract, description, forceContractClauses); drift != nil {
		t.Fatalf("the --force contract drifted between adopt.ForceContract and standards_adopt:\n%s\nCLI: %s\nMCP: %s",
			strings.Join(drift, "\n"), adopt.ForceContract, description)
	}
	if !strings.Contains(description, "(default: false)") {
		t.Fatalf("force description lacks its default:\n%s", description)
	}
	if strings.Contains(description, "Overwrite existing standards configurations") {
		t.Fatalf("force is still described as a reset:\n%s", description)
	}
	if source := tool.InputSchema.Properties["source_root"].Description; strings.HasPrefix(source, "Optional") ||
		!strings.Contains(source, "required for missing lock and with force") {
		t.Fatalf("source_root must say force needs it: %q", source)
	}
	hints := tool.Annotations
	if force.Type != "boolean" || hints.ReadOnlyHint || !hints.DestructiveHint || !hints.IdempotentHint || hints.OpenWorldHint {
		t.Fatalf("standards_adopt force type %q, annotations %+v", force.Type, hints)
	}
}

// TestForceContractDrift_NegativeAndBoundary holds the pairing check itself: a clause reworded in
// adopt.ForceContract alone, or dropped from the MCP description alone, is reported (negative),
// and a clause added to adopt.ForceContract with no MCP restatement changes the clause count and
// is reported even though every paired clause still matches (boundary).
func TestForceContractDrift_NegativeAndBoundary(t *testing.T) {
	description := adoptTool(t).InputSchema.Properties["force"].Description
	paperclip := forceContractClauses[4]
	cases := []struct {
		name, contract, description, want string
	}{
		{"negative: CLI clause reworded", strings.Replace(adopt.ForceContract, paperclip.cli,
			"set a Paperclip platform that names another repository", 1), description,
			"the CLI contract no longer states " + paperclip.cli},
		{"negative: MCP clause dropped", adopt.ForceContract, strings.Replace(description, paperclip.mcp, "", 1),
			"the MCP force description no longer restates " + paperclip.cli},
		{"boundary: unpaired CLI clause", adopt.ForceContract + "; also prune stale hooks", description,
			"the CLI contract states 8 clauses, the MCP restates 7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			drift := forceContractDrift(tc.contract, tc.description, forceContractClauses)
			if !strings.Contains(strings.Join(drift, "\n"), tc.want) {
				t.Fatalf("drift = %q, want one naming %q", drift, tc.want)
			}
		})
	}
}
