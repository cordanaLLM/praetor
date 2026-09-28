package lockdown

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

const receiptTestName = ".standards-receipt.json"

func writeReceiptBytes(t *testing.T, dir string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, receiptTestName)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Positive: an envelope SaveReceiptFile wrote reads back with its signed fields and gate
// output intact.
func TestLoadReceiptFile_Positive_RoundTrip(t *testing.T) {
	_, priv, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	output := gateOutputWith(WorktreeCleanLine(true))
	receipt, err := CreateReceipt("praetorctl gate run", 0, []byte(output), "deadbeef", "acme/widget", priv)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), receiptTestName)
	if err := SaveReceiptFile(path, &ReceiptFile{ExecutionReceipt: *receipt, GateOutput: output}, 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadReceiptFile(path)
	if err != nil {
		t.Fatalf("LoadReceiptFile: %v", err)
	}
	if loaded.Signature != receipt.Signature || loaded.GateOutput != output || loaded.CommitSHA != "deadbeef" {
		t.Fatalf("loaded envelope = %+v, want the saved one", loaded)
	}
}

// Negative: a link at the receipt path that resolves outside its directory, a FIFO, and a
// second envelope appended to the first are each refused (BUG-857). The FIFO read is bounded
// so the test fails instead of hanging if the refusal regresses.
func TestLoadReceiptFile_Negative_EscapeFIFOAndSecondDocument(t *testing.T) {
	outside := writeReceiptBytes(t, t.TempDir(), []byte(`{"version":"v1"}`))
	dir := t.TempDir()
	link := filepath.Join(dir, receiptTestName)
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if _, err := LoadReceiptFile(link); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping link = %v, want ErrPathEscapesRoot", err)
	}

	doubled := writeReceiptBytes(t, t.TempDir(), []byte(`{"version":"v1"}`+"\n"+`{"version":"v2"}`))
	var syntax *json.SyntaxError
	if _, err := LoadReceiptFile(doubled); !errors.As(err, &syntax) {
		t.Errorf("two envelopes = %v, want a JSON syntax error", err)
	}

	fifo := filepath.Join(t.TempDir(), receiptTestName)
	testsupport.MakeFIFO(t, fifo)
	err := testsupport.RunWithin(t, 10*time.Second, func() error {
		_, readErr := LoadReceiptFile(fifo)
		return readErr
	})
	if !errors.Is(err, util.ErrNotRegularFile) {
		t.Errorf("FIFO receipt = %v, want ErrNotRegularFile", err)
	}
}

// Boundary: an envelope of exactly maxReceiptFileBytes is read; one byte more is refused
// rather than allocated and parsed.
func TestLoadReceiptFile_Boundary_ByteBound(t *testing.T) {
	prefix, suffix := `{"version":"v1","gate_output":"`, `"}`
	pad := maxReceiptFileBytes - len(prefix) - len(suffix)
	exact := prefix + strings.Repeat("x", pad) + suffix
	loaded, err := LoadReceiptFile(writeReceiptBytes(t, t.TempDir(), []byte(exact)))
	if err != nil || len(loaded.GateOutput) != pad {
		t.Fatalf("envelope at the bound = %v, want it read in full", err)
	}
	over := prefix + strings.Repeat("x", pad+1) + suffix
	if _, err := LoadReceiptFile(writeReceiptBytes(t, t.TempDir(), []byte(over))); !errors.Is(err, util.ErrFileTooLarge) {
		t.Fatalf("envelope one byte past the bound = %v, want ErrFileTooLarge", err)
	}
}
