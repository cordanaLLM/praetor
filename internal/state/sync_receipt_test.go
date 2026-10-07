package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: writing, rewriting and removing the gate's receipt at the ledger root leaves a
// synchronized ledger current. `praetorctl gate run` writes it after the agent synced, so before
// #136 every gate run staled the ledger it had just been told was current.
func TestVerifyStateSync_Positive_GateReceiptOutsideTheBinding(t *testing.T) {
	root := syncFixture(t)
	receipt := filepath.Join(root, util.GateReceiptFile)
	for _, step := range []string{"written", "rewritten"} {
		writeIntegrityFile(t, receipt, `{"receipt":"`+step+`"}`+"\n")
		if err := VerifyStateSync(t.Context(), root); err != nil {
			t.Fatalf("a %s gate receipt staled the ledger: %v", step, err)
		}
	}
	if err := os.Remove(receipt); err != nil {
		t.Fatal(err)
	}
	if err := VerifyStateSync(t.Context(), root); err != nil {
		t.Fatalf("removing the gate receipt staled the ledger: %v", err)
	}
}

// Negative: with the receipt present, any other change still stales the ledger.
func TestVerifyStateSync_Negative_ReceiptHidesNoOtherChange(t *testing.T) {
	for _, kind := range []string{"tracked", "staged", "untracked", "ledger"} {
		t.Run(kind, func(t *testing.T) {
			root := syncFixture(t)
			writeIntegrityFile(t, filepath.Join(root, util.GateReceiptFile), "{}\n")
			changeSyncInput(t, root, kind)
			if err := VerifyStateSync(t.Context(), root); err == nil {
				t.Fatalf("a %s change beside the gate receipt was accepted", kind)
			}
		})
	}
}

// Boundary: exactly one path is excluded. The receipt's name below the root, and names that
// only start with it, are ordinary untracked files and still bind.
func TestVerifyStateSync_Boundary_OnlyTheRootReceiptIsExcluded(t *testing.T) {
	for _, rel := range []string{
		filepath.Join("sub", util.GateReceiptFile),
		util.GateReceiptFile + ".bak",
		"x" + util.GateReceiptFile,
	} {
		t.Run(filepath.ToSlash(rel), func(t *testing.T) {
			root := syncFixture(t)
			if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o700); err != nil {
				t.Fatal(err)
			}
			writeIntegrityFile(t, filepath.Join(root, rel), "{}\n")
			if err := VerifyStateSync(t.Context(), root); err == nil {
				t.Fatalf("untracked %s was excluded with the gate receipt", rel)
			}
		})
	}
}
