package paperclip

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const (
	// pinnedReceiptRow and unpinnedReceiptPrefix are the two receipt rows receiptContract writes.
	pinnedReceiptRow = "Ed25519 Exit-0 Receipts: only `praetorctl gate run` without `--dry-run` mints one; " +
		"attach minted receipt to PR proposal; report no unminted receipt."
	unpinnedReceiptPrefix = "Ed25519 Exit-0 Receipts: none. .standards.yaml pins no valid receipt.public_key -> attach no receipt"
	// goModule is a root go.mod, the file whose absence makes the gate's Go stages not applicable.
	goModule = "module example.com/widget\n\ngo 1.27\n"
)

// unconditionalReceiptClaims are the receipt promises earlier releases rendered on every
// repository or every pinned one (#550): the #523 pinned row and the older row of
// priorOperatingContract and cavemanOperatingContract. A gate run with --dry-run mints nothing,
// so no current rendering may carry either.
var unconditionalReceiptClaims = []string{
	"mint via `praetorctl gate run`; attach receipt to every PR proposal",
	"attach cryptographic execution receipts to all PR proposals",
}

// receiptRow synthesizes the harness for a repository whose manifest declares acme/widget and
// the given receipt section, and returns the contract row on receipts.
func receiptRow(t *testing.T, receiptSection string) (string, *Harness) {
	t.Helper()
	return receiptRowIn(t, t.TempDir(), receiptSection)
}

// receiptRowIn is receiptRow for repo, which may already hold other files such as a go.mod.
func receiptRowIn(t *testing.T, repo, receiptSection string) (string, *Harness) {
	t.Helper()
	writeRepoFile(t, repo, ".standards.yaml", "repository:\n  owner: acme\n  name: widget\n  forge: forgejo\n"+receiptSection)
	h, err := SynthesizeHarness(t.Context(), repo, unknownFacts)
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

// renderedForms returns both forms a harness is written in: harness.json and rules.md, the
// latter with its list-item wrapping folded back to single spaces.
func renderedForms(t *testing.T, h *Harness) map[string]string {
	t.Helper()
	data, err := MarshalHarness(h)
	if err != nil {
		t.Fatal(err)
	}
	return map[string]string{
		harnessFile: string(data),
		rulesFile:   strings.Join(strings.Fields(renderRules(h)), " "),
	}
}

// renderedRow is row as form writes it: a JSON string body in harness.json, where MarshalHarness
// escapes `>` and `&`, and the text itself in rules.md.
func renderedRow(t *testing.T, form, row string) string {
	t.Helper()
	if form != harnessFile {
		return row
	}
	quoted, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Trim(string(quoted), `"`)
}

// TestSynthesizeHarness_Positive_GoModuleKeepsReceiptRule: a Go module repository with a
// pinned key keeps the receipt rule in both rendered forms, stated with the condition under
// which `praetorctl gate run` mints: no --dry-run (#550).
func TestSynthesizeHarness_Positive_GoModuleKeepsReceiptRule(t *testing.T) {
	repo := t.TempDir()
	writeRepoFile(t, repo, "go.mod", goModule)
	row, h := receiptRowIn(t, repo, pinnedReceipt)
	if row != pinnedReceiptRow || !strings.Contains(row, "without `--dry-run`") ||
		!strings.Contains(row, "report no unminted receipt") {
		t.Fatalf("Go module receipt row = %q, want %q", row, pinnedReceiptRow)
	}
	for form, text := range renderedForms(t, h) {
		if !strings.Contains(text, "mints one; attach minted receipt to PR proposal") {
			t.Errorf("%s drops the receipt rule:\n%s", form, text)
		}
	}
}

// TestSynthesizeHarness_Negative_NoGoModPromisesNoUnconditionalReceipt: a repository without a
// go.mod, key pinned, gets no instruction to mint and attach a receipt on every proposal in
// either rendered form, and no earlier unconditional claim. Its row is the Go module's: the
// gate mints without a go.mod too (its Go stages report not_applicable), so only --dry-run,
// which the row names, decides whether a receipt exists.
func TestSynthesizeHarness_Negative_NoGoModPromisesNoUnconditionalReceipt(t *testing.T) {
	row, h := receiptRow(t, pinnedReceipt)
	if row != pinnedReceiptRow {
		t.Fatalf("no-go.mod receipt row = %q, want the Go module's %q", row, pinnedReceiptRow)
	}
	for form, text := range renderedForms(t, h) {
		for _, claim := range append([]string{"attach receipt to every PR proposal"}, unconditionalReceiptClaims...) {
			if strings.Contains(text, claim) {
				t.Errorf("%s still promises %q:\n%s", form, claim, text)
			}
		}
	}
}

// TestSynthesizeHarness_Boundary_ReceiptRuleForms: both rows, pinned and unpinned, reach both
// rendered forms unchanged; neither carries an earlier unconditional claim; and a harness this
// release's predecessor wrote with the #523 pinned row, harness.json beside its rules.md, is
// unmodified earlier output, so plain adopt refreshes it to the conditional row.
func TestSynthesizeHarness_Boundary_ReceiptRuleForms(t *testing.T) {
	for _, section := range []string{"", pinnedReceipt} {
		row, h := receiptRow(t, section)
		for form, text := range renderedForms(t, h) {
			if !strings.Contains(text, renderedRow(t, form, row)) {
				t.Errorf("%s omits receipt row %q:\n%s", form, row, text)
			}
			for _, claim := range unconditionalReceiptClaims {
				if strings.Contains(text, claim) {
					t.Errorf("%s carries earlier claim %q", form, claim)
				}
			}
		}
	}
	repo := t.TempDir()
	current := synthesizeWidget(t, repo, pinnedReceipt, unknownFacts)
	earlier := *current
	earlier.OperatingContract = slices.Clone(current.OperatingContract)
	earlier.OperatingContract[3] = priorPinnedReceiptRows[0]
	if !strings.Contains(earlier.OperatingContract[3], unconditionalReceiptClaims[0]) {
		t.Fatalf("fixture precondition: recorded #523 row %q", earlier.OperatingContract[3])
	}
	if err := WriteHarness(&earlier, repo); err != nil {
		t.Fatal(err)
	}
	state, err := PriorGenerated(t.Context(), repo, current)
	if err != nil || !state.Generated || !state.Rules {
		t.Fatalf("harness with the #523 pinned row not earlier output: prior=%+v err=%v", state, err)
	}
}
