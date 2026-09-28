package operationalsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// overlayOperation returns an operation whose expected overlay names every owner path, and a
// working tree that already has the overlay's directories.
func overlayOperation(t *testing.T) (*operation, string) {
	t.Helper()
	op := &operation{expected: map[string][]byte{}}
	root := t.TempDir()
	for _, path := range ownerPaths {
		op.expected[path] = []byte("overlay " + path + "\n")
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(path)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return op, root
}

// Positive: every overlay file lands in the working tree with the expected bytes.
func TestWriteOverlayFiles_Positive(t *testing.T) {
	op, root := overlayOperation(t)
	if err := op.writeOverlayFiles(root); err != nil {
		t.Fatalf("writeOverlayFiles: %v", err)
	}
	for _, path := range ownerPaths {
		if data, err := os.ReadFile(filepath.Join(root, path)); err != nil || string(data) != string(op.expected[path]) { // #nosec G304 -- test-local path
			t.Fatalf("%s = %q, %v", path, data, err)
		}
	}
}

// Negative: an overlay file planted as a link inside the tree is refused instead of written
// through, and the link target keeps its content (BUG-826).
func TestWriteOverlayFiles_Negative_LinkedOverlayFile(t *testing.T) {
	op, root := overlayOperation(t)
	target := filepath.Join(root, "README.md")
	if err := os.WriteFile(target, []byte("kept\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "README.md"), filepath.Join(root, ".paperclip", "rules.md")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := op.writeOverlayFiles(root); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked overlay file = %v, want ErrSymlinkDestination", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "kept\n" { // #nosec G304 -- test-local path
		t.Fatalf("link target = %q, %v; want it untouched", data, err)
	}
}

// Boundary: an overlay directory that is a relative link staying inside the tree is followed;
// the confinement edge is the tree's root.
func TestWriteOverlayFiles_Boundary_InRootLinkedDirectory(t *testing.T) {
	op, root := overlayOperation(t)
	if err := os.RemoveAll(filepath.Join(root, ".paperclip")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state", filepath.Join(root, ".paperclip")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := op.writeOverlayFiles(root); err != nil {
		t.Fatalf("in-tree linked .paperclip = %v, want the write to follow it", err)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "harness.json")); err != nil {
		t.Fatalf("harness.json not written through the in-tree link: %v", err)
	}
}
