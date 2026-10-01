package main

import (
	"strings"
	"testing"
)

// forceContractClauses are the clauses of the --force contract (adopt.ForceContract, which the
// CLI flag help prints) that the standards_adopt force description restates in the agent
// register: the lock rebuild and the source it needs, the audit-locked rewrite, the editor JSON
// merge, the harness refresh, the Paperclip platform, the backup location and what stays kept.
var forceContractClauses = []string{
	"Rebuild .standards.lock + pinned catalog from source_root (required, dry_run included)",
	"rewrite drifted files audit compares byte for byte",
	"merge managed values into editor JSON",
	"regenerate AGENTS.md harness, repository additions kept",
	"set Paperclip platform naming another repository",
	"backup under .workingdir/adopt-backups when git ignores backup path",
	"Other files audit never verifies: kept; delete one, rerun adopt to regenerate",
}

// TestCreateAdoptTool_ForceStatesTheContract (#502): the standards_adopt force property states
// every clause of the --force contract the CLI flag help states (positive); the reset wording
// earlier releases served is gone, and source_root no longer reads as optional, since force
// needs it (negative); force stays a boolean that defaults to false, and
// the tool keeps its destructive, idempotent, closed-world annotations, since a forced run still
// replaces bytes (boundary).
func TestCreateAdoptTool_ForceStatesTheContract(t *testing.T) {
	srv, err := NewServer(t.TempDir(), "v")
	if err != nil {
		t.Fatal(err)
	}
	tool, ok := srv.tools["standards_adopt"]
	if !ok {
		t.Fatal("standards_adopt is not registered")
	}
	force, ok := tool.InputSchema.Properties["force"]
	if !ok {
		t.Fatal("standards_adopt has no force property")
	}
	for _, clause := range append(forceContractClauses, "(default: false)") {
		if !strings.Contains(force.Description, clause) {
			t.Fatalf("force description lacks %q:\n%s", clause, force.Description)
		}
	}
	if strings.Contains(force.Description, "Overwrite existing standards configurations") {
		t.Fatalf("force is still described as a reset:\n%s", force.Description)
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
