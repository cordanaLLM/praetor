package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
)

// requireRecoveryHint fails unless one validation error names the steps that replace the
// receipt: re-run the gate on the pushed head, then replace the fenced receipt block (#136).
func requireRecoveryHint(t *testing.T, res *PRChecklistResult, want ...string) {
	t.Helper()
	joined := strings.Join(res.Errors, "\n")
	for _, part := range append([]string{util.GateRepoRunCommand, util.GateReceiptFile, "```" + ReceiptFenceToken}, want...) {
		if !strings.Contains(joined, part) {
			t.Fatalf("the refusal does not name %q as a recovery step: %v", part, res.Errors)
		}
	}
}

// Positive: a receipt minted for an earlier push is refused and the refusal names the exact
// recovery -- mint a new receipt on the pushed head, then replace the receipt block.
func TestValidatePRChecklist_Positive_StaleReceiptNamesRecovery(t *testing.T) {
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	minted := "0f1e2d3c4b5a69788796a5b4c3d2e1f009182736"
	pushed := "1a2b3c4d5e6f708192a3b4c5d6e7f80910111213"
	body := prChecklistBoxes + signedGateOutputBlock(t, priv, minted, gateOutput(true, "all gates passed"))

	res, err := ValidatePRChecklistWithPolicy(body, ReceiptPolicy{PinnedKey: pub, HeadSHA: pushed})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid || res.HasReceipt {
		t.Fatalf("a receipt for another commit was accepted: %+v", res)
	}
	requireRecoveryHint(t, res, minted, pushed, "replace")
}

// Negative: the hint changes no verdict. A wrong-SHA receipt is still refused, and a missing
// receipt is refused with the same recovery named.
func TestValidatePRChecklist_Negative_RecoveryHintKeepsTheRefusal(t *testing.T) {
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	body := prChecklistBoxes + signedGateOutputBlock(t, priv, "abc", gateOutput(true, "all gates passed"))
	for name, policy := range map[string]ReceiptPolicy{
		"pinned":   {PinnedKey: pub, HeadSHA: "def"},
		"unpinned": {HeadSHA: "def"},
	} {
		res, err := ValidatePRChecklistWithPolicy(body, policy)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if res.Valid || res.HasReceipt || len(res.Errors) != 1 {
			t.Fatalf("%s: a wrong-SHA receipt was not refused exactly once: %+v", name, res)
		}
	}

	res, err := ValidatePRChecklistWithPolicy(prChecklistBoxes, ReceiptPolicy{PinnedKey: pub, HeadSHA: "def"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Valid || res.HasReceipt {
		t.Fatalf("a body without a receipt was accepted: %+v", res)
	}
	requireRecoveryHint(t, res, "missing mandatory Ed25519 Exit-0 receipt")
}

// Boundary: with no head SHA to bind, the commit check is still skipped -- a receipt for any
// commit verifies and no recovery hint is attached. Case and surrounding space in the SHA do
// not make a matching receipt stale.
func TestValidatePRChecklist_Boundary_EmptyHeadSkipsTheCommitBinding(t *testing.T) {
	pub, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		t.Fatalf("failed generating keypair: %v", err)
	}
	minted := "0f1e2d3c4b5a69788796a5b4c3d2e1f009182736"
	body := prChecklistBoxes + signedGateOutputBlock(t, priv, minted, gateOutput(true, "all gates passed"))
	for name, head := range map[string]string{"empty": "", "same commit, upper case": " " + strings.ToUpper(minted) + " "} {
		res, err := ValidatePRChecklistWithPolicy(body, ReceiptPolicy{PinnedKey: pub, HeadSHA: head})
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if !res.Valid || !res.HasReceipt || len(res.Errors) != 0 {
			t.Fatalf("%s: a receipt was refused without a commit to bind it to: %+v", name, res)
		}
	}
}
