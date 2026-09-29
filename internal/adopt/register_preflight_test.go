package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// The agent-harness step's text register policy is checked before the first step writes
// (preflightAgentHarness): an invalid register.tasks label used to abort the run after the
// lock was written and before AGENTS.md was.

// registerPreflightRepo is a Go repository whose manifest carries body.
func registerPreflightRepo(t *testing.T, body string) string {
	t.Helper()
	repoPath := newTestRepo(t, "register-preflight")
	mustWrite(t, filepath.Join(repoPath, "go.mod"), "module example.com/registerpreflight\n\ngo 1.22\n")
	mustWrite(t, filepath.Join(repoPath, manifestFile), body)
	return repoPath
}

// Positive: a register.tasks entry naming a declared target_tasks label passes the preflight
// and the harness is written.
func TestAdopt_Positive_ValidRegisterTaskPassesPreflight(t *testing.T) {
	repoPath := registerPreflightRepo(t, "version: 1\nregister:\n  tasks:\n    ci_debugging: docs\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if !fileExists(filepath.Join(repoPath, agentsFile)) {
		t.Fatal("AGENTS.md not written")
	}
}

// Negative: a register.tasks entry that is no target_tasks label fails the run in preflight,
// real and dry, and nothing is written: no lock, no .gitignore.
func TestAdopt_Negative_InvalidRegisterTaskRefusedBeforeAnyWrite(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		repoPath := registerPreflightRepo(t, "version: 1\nregister:\n  tasks:\n    deploy_prod: social\n")
		before := snapshotTree(t, repoPath)
		rep, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath, DryRun: dryRun})
		if err == nil || !strings.Contains(err.Error(), "agent-harness preflight") || !strings.Contains(err.Error(), "deploy_prod") {
			t.Fatalf("dry run %v: err = %v, want an agent-harness preflight refusal naming the label", dryRun, err)
		}
		if rep == nil || len(rep.CreatedFiles) != 0 || len(rep.ReconciledFiles) != 0 {
			t.Fatalf("dry run %v: files recorded before the refusal: %+v", dryRun, rep)
		}
		assertTreeUnchanged(t, before, snapshotTree(t, repoPath))
	}
}

// Boundary: with agent-harness declined the step never loads the register policy, so the same
// label does not stop adoption, and AGENTS.md stays absent.
func TestAdopt_Boundary_DeclinedHarnessSkipsRegisterPreflight(t *testing.T) {
	repoPath := registerPreflightRepo(t,
		"version: 1\nregister:\n  tasks:\n    deploy_prod: social\nadoption:\n  decline: [agent-harness]\n")
	if _, err := Adopt(t.Context(), AdoptOptions{LockSourceRoot: newAdoptLockSource(t), Path: repoPath}); err != nil {
		t.Fatalf("adopt: %v", err)
	}
	if fileExists(filepath.Join(repoPath, agentsFile)) {
		t.Fatal("declined harness written")
	}
}
