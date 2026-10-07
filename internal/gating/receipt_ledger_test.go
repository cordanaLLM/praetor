package gating

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

// TestRunReceiptStage_3D_StateLedgerStaysCurrent replays the loop from #136 with the real
// receipt stage: sync the ledger, let the gate write its receipt, verify. The ledger must stay
// current across the receipt the gate itself wrote (positive), go stale on any unrelated edit
// afterwards (negative), and stay current across a second receipt for the same commit, which
// rewrites the file in place (boundary).
func TestRunReceiptStage_3D_StateLedgerStaysCurrent(t *testing.T) {
	repo := newHermeticGitRepo(t)
	commitFile(t, repo, ".gitignore", "/.workingdir/\n")
	sandboxReceiptKey(t)
	if _, err := state.SyncState(t.Context(), repo, "before the gate run"); err != nil {
		t.Fatalf("SyncState: %v", err)
	}
	mint := func(label string) {
		t.Helper()
		rep := &PipelineReport{Repository: "example/repo", CommitSHA: headOf(t, repo), WorktreeClean: true,
			Stages: make([]StageResult, 0, maxStages)}
		cfg := newStageConfig(repo, false, rep)
		cfg.verified = []string{languageGo}
		if _, err := runReceiptStage(t.Context(), cfg); err != nil {
			t.Fatalf("%s receipt stage: %v", label, err)
		}
		if rep.ReceiptPath != filepath.Join(repo, ReceiptFileName) {
			t.Fatalf("%s receipt written to %q, want the repository root", label, rep.ReceiptPath)
		}
		if err := state.VerifyStateSync(t.Context(), repo); err != nil {
			t.Fatalf("the %s receipt the gate wrote staled the ledger: %v", label, err)
		}
	}
	mint("first")
	mint("second")

	writeFile(t, filepath.Join(repo, "scratch.txt"), "unrelated\n")
	if err := state.VerifyStateSync(t.Context(), repo); err == nil {
		t.Fatal("an unrelated edit after the gate run left the ledger current")
	}
}
