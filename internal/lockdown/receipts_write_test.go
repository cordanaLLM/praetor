package lockdown

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func sampleReceiptFile() *ReceiptFile {
	return &ReceiptFile{ExecutionReceipt: ExecutionReceipt{Version: ReceiptVersion, Command: "praetorctl gate run"}, GateOutput: "out\n"}
}

// Positive: SaveReceiptFile creates the envelope at the repository root, and a later save
// replaces it.
func TestSaveReceiptFile_Positive_CreateAndReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), receiptTestName)
	for _, output := range []string{"first\n", "second\n"} {
		rf := sampleReceiptFile()
		rf.GateOutput = output
		if err := SaveReceiptFile(path, rf, 0o644); err != nil {
			t.Fatalf("SaveReceiptFile: %v", err)
		}
		loaded, err := LoadReceiptFile(path)
		if err != nil || loaded.GateOutput != output {
			t.Fatalf("saved envelope = %+v, %v; want gate output %q", loaded, err, output)
		}
	}
}

// Negative: a link planted at the receipt path is refused instead of written through, both
// one that leaves the repository and one that stays inside it (BUG-826).
func TestSaveReceiptFile_Negative_LinkedDestination(t *testing.T) {
	outside := writeReceiptBytes(t, t.TempDir(), []byte("outside"))
	repo := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, receiptTestName)); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := SaveReceiptFile(filepath.Join(repo, receiptTestName), sampleReceiptFile(), 0o644); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping receipt link = %v, want ErrPathEscapesRoot", err)
	}

	inRepo := t.TempDir()
	sibling := filepath.Join(inRepo, "other.json")
	if err := os.WriteFile(sibling, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.json", filepath.Join(inRepo, receiptTestName)); err != nil {
		t.Fatal(err)
	}
	if err := SaveReceiptFile(filepath.Join(inRepo, receiptTestName), sampleReceiptFile(), 0o644); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Errorf("in-repository receipt link = %v, want ErrSymlinkDestination", err)
	}
	for path, body := range map[string]string{outside: "outside", sibling: "kept"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != body { // #nosec G304 -- test-local path
			t.Errorf("%s = %q, %v; want %q untouched", path, data, err, body)
		}
	}
}

// Boundary: perm is a ceiling, so replacing a receipt the owner narrowed keeps the narrower
// mode, and a fresh receipt gets no bit perm does not grant.
func TestSaveReceiptFile_Boundary_PermissionCeiling(t *testing.T) {
	if !util.ModeIsProtection() {
		t.Skip("permission bits are not enforced on this platform")
	}
	path := writeReceiptBytes(t, t.TempDir(), []byte("{}"))
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if err := SaveReceiptFile(path, sampleReceiptFile(), 0o644); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("replaced receipt mode = %v, %v; want 0640 kept", info, err)
	}
	fresh := filepath.Join(t.TempDir(), receiptTestName)
	if err := SaveReceiptFile(fresh, sampleReceiptFile(), 0o600); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(fresh); err != nil || info.Mode().Perm()&^0o600 != 0 {
		t.Fatalf("fresh receipt mode = %v, %v; want within 0600", info, err)
	}
}
