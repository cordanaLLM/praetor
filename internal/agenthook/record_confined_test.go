package agenthook

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: a capture lands as one regular file directly in the recording root.
func TestWriteRecording_Positive_OneFileInRoot(t *testing.T) {
	dir := t.TempDir()
	if err := writeRecording(dir, "claude", "pre-tool", []byte("{}")); err != nil {
		t.Fatalf("writeRecording: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 || !entries[0].Type().IsRegular() {
		t.Fatalf("recording root = %v, %v; want one regular file", entries, err)
	}
}

// Negative: a client name that climbs out of the recording root is refused before anything is
// written outside it (BUG-826).
func TestWriteRecording_Negative_EscapingNameRefused(t *testing.T) {
	parent := t.TempDir()
	dir := filepath.Join(parent, "records")
	if err := writeRecording(dir, filepath.Join("..", "escaped"), "pre-tool", []byte("{}")); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("escaping client name = %v, want ErrPathEscapesRoot", err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "records" {
		t.Fatalf("parent of the recording root = %v, %v; want only the root itself", entries, err)
	}
}

// Boundary: the recording root may itself be a link (macOS ships /tmp as one); it is the
// operator's chosen boundary, resolved when opened, and the capture lands in its target.
func TestWriteRecording_Boundary_LinkedRootResolved(t *testing.T) {
	target := t.TempDir()
	link := filepath.Join(t.TempDir(), "records")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeRecording(link, "claude", "pre-tool", []byte("{}")); err != nil {
		t.Fatalf("linked recording root = %v, want the capture written", err)
	}
	if entries, err := os.ReadDir(target); err != nil || len(entries) != 1 {
		t.Fatalf("link target = %v, %v; want the one capture", entries, err)
	}
}
