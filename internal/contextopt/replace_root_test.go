package contextopt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestReplaceRootSnapshotCreatesAndUpdatesInPinnedDirectory is the positive path: the
// root-handle variant creates an absent name and replaces it against the observed bytes,
// writing inside the directory the caller pinned.
func TestReplaceRootSnapshotCreatesAndUpdatesInPinnedDirectory(t *testing.T) {
	root := openTestRoot(t)
	if err := ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("first"), ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatalf("create: %v", err)
	}
	opts := ReplaceOptions{Expected: []byte("first"), Exists: true, Mode: 0o600}
	if err := ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("second"), opts); err != nil {
		t.Fatalf("update: %v", err)
	}
	data, err := ReadSnapshot(t.Context(), filepath.Join(root.Name(), "LEDGER.md"))
	if err != nil || string(data) != "second" {
		t.Fatalf("published content = (%q, %v), want second", data, err)
	}
}

// TestReplaceRootSnapshotRejectsStaleAndInvalidArguments is the negative path: a stale
// expectation, a nil handle, a non-flat name, a nil context and a widened mode are all
// refused, and the published content survives every refusal.
func TestReplaceRootSnapshotRejectsStaleAndInvalidArguments(t *testing.T) {
	root := openTestRoot(t)
	if err := ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("kept"), ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	stale := ReplaceOptions{Expected: []byte("other"), Exists: true, Mode: 0o600}
	var absent context.Context
	cases := map[string]func() error{
		"stale expectation": func() error { return ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("lost"), stale) },
		"absent expectation": func() error {
			return ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("lost"), ReplaceOptions{Mode: 0o600})
		},
		"nil root":    func() error { return ReplaceRootSnapshot(t.Context(), nil, "LEDGER.md", []byte("x"), stale) },
		"nested name": func() error { return ReplaceRootSnapshot(t.Context(), root, "sub/LEDGER.md", []byte("x"), stale) },
		"parent name": func() error { return ReplaceRootSnapshot(t.Context(), root, "../LEDGER.md", []byte("x"), stale) },
		"dot name":    func() error { return ReplaceRootSnapshot(t.Context(), root, ".", []byte("x"), stale) },
		"empty name":  func() error { return ReplaceRootSnapshot(t.Context(), root, "", []byte("x"), stale) },
		"nil context": func() error { return ReplaceRootSnapshot(absent, root, "LEDGER.md", []byte("x"), stale) },
		"wide mode": func() error {
			return ReplaceRootSnapshot(t.Context(), root, "LEDGER.md", []byte("x"), ReplaceOptions{Expected: []byte("kept"), Exists: true, Mode: 0o666})
		},
	}
	for label, call := range cases {
		if err := call(); err == nil {
			t.Errorf("%s accepted", label)
		}
	}
	data, err := ReadSnapshot(t.Context(), filepath.Join(root.Name(), "LEDGER.md"))
	if err != nil || string(data) != "kept" {
		t.Fatalf("content after refusals = (%q, %v), want kept", data, err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(root.Name()), "LEDGER.md")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("parent name wrote outside the pinned directory: %v", err)
	}
}

// TestReplaceRootSnapshotBoundaryLimitsAndCancellation pins the edges: exactly
// MaxSourceBytes publishes, one byte more is refused, and a cancelled context writes
// nothing.
func TestReplaceRootSnapshotBoundaryLimitsAndCancellation(t *testing.T) {
	root := openTestRoot(t)
	exact := bytes.Repeat([]byte("x"), MaxSourceBytes)
	if err := ReplaceRootSnapshot(t.Context(), root, "bounded", exact, ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatalf("exact bound refused: %v", err)
	}
	over := append(append([]byte{}, exact...), 'x')
	if err := ReplaceRootSnapshot(t.Context(), root, "bounded", over, ReplaceOptions{Expected: exact, Exists: true, Mode: 0o600}); err == nil {
		t.Fatal("one byte over the bound accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ReplaceRootSnapshot(ctx, root, "cancelled", []byte("x"), ReplaceOptions{Mode: 0o600}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write = %v, want context.Canceled", err)
	}
	if _, err := root.Lstat("cancelled"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled write created the file: %v", err)
	}
}
