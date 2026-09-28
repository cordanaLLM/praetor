package paperclip

import (
	"strings"
	"testing"
)

const (
	// pinnedReceiptRow and unpinnedReceiptPrefix are the two receipt rows receiptContract writes.
	pinnedReceiptRow      = "Ed25519 Exit-0 Receipts: mint via `praetorctl gate run`; attach receipt to every PR proposal."
	unpinnedReceiptPrefix = "Ed25519 Exit-0 Receipts: none. .standards.yaml pins no valid receipt.public_key -> attach no receipt"
)

// receiptRow synthesizes the harness for a repository whose manifest declares acme/widget and
// the given receipt section, and returns the contract row on receipts.
func receiptRow(t *testing.T, receiptSection string) (string, *Harness) {
	t.Helper()
	repo := t.TempDir()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: widget\n"+receiptSection)
	h, err := SynthesizeHarness(t.Context(), repo, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range h.OperatingContract {
		if strings.HasPrefix(row, "Ed25519 Exit-0 Receipts:") {
			return row, h
		}
	}
	t.Fatalf("contract has no receipt row: %q", h.OperatingContract)
	return "", nil
}

// TestSynthesizeHarness_Positive_PinnedKeyPrescribesReceipts: with a pinned receipt key a
// receipt verifies, so the contract prescribes minting and attaching one, and the rendered
// rules repeat it as one list item, wrapped as renderRules wraps every item (wrapListItem).
func TestSynthesizeHarness_Positive_PinnedKeyPrescribesReceipts(t *testing.T) {
	row, h := receiptRow(t, "receipt:\n  public_key: \""+strings.Repeat("ab", 32)+"\"\n")
	if row != pinnedReceiptRow {
		t.Fatalf("receipt row = %q, want %q", row, pinnedReceiptRow)
	}
	if !strings.Contains(renderRules(h), wrapListItem(pinnedReceiptRow)) {
		t.Fatalf("rendered rules omit the receipt row:\n%s", renderRules(h))
	}
}

// TestSynthesizeHarness_Negative_NoPinnedKeyPrescribesNoReceipt: without a pinned key every
// attached receipt is refused (lockdown.ErrNoPinnedKey), so the contract must not tell the run
// to attach one (BUG-804).
func TestSynthesizeHarness_Negative_NoPinnedKeyPrescribesNoReceipt(t *testing.T) {
	row, h := receiptRow(t, "")
	if !strings.HasPrefix(row, unpinnedReceiptPrefix) {
		t.Fatalf("receipt row = %q, want the unpinned row", row)
	}
	if contract := strings.Join(h.OperatingContract, "\n"); strings.Contains(contract, "attach receipt to") ||
		strings.Contains(contract, "attach cryptographic execution receipts") {
		t.Fatalf("contract still prescribes a receipt nothing can verify:\n%s", contract)
	}
	if err := validateHarnessValues(h.OperatingContract); err != nil || len(h.OperatingContract) != 6 {
		t.Fatalf("unpinned contract left the harness bounds (%d rows): %v", len(h.OperatingContract), err)
	}
}

// TestSynthesizeHarness_Boundary_ReceiptKeyShape: the key is read as lockdown reads it. A
// key one hex digit short or an empty one pins nothing; surrounding whitespace is trimmed, so
// a padded well-formed key still pins.
func TestSynthesizeHarness_Boundary_ReceiptKeyShape(t *testing.T) {
	cases := map[string]struct {
		section string
		pinned  bool
	}{
		"63 hex digits": {"receipt:\n  public_key: \"" + strings.Repeat("ab", 31) + "a\"\n", false},
		"empty key":     {"receipt:\n  public_key: \"\"\n", false},
		"padded key":    {"receipt:\n  public_key: \"  " + strings.Repeat("cd", 32) + "  \"\n", true},
	}
	for name, tc := range cases {
		row, _ := receiptRow(t, tc.section)
		if got := row == pinnedReceiptRow; got != tc.pinned || (!got && !strings.HasPrefix(row, unpinnedReceiptPrefix)) {
			t.Errorf("%s: receipt row = %q, want pinned=%v", name, row, tc.pinned)
		}
	}
}
