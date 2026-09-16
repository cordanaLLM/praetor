// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func snapshotDirectory(t *testing.T, path string) *os.Root {
	t.Helper()
	root, err := OpenDirectory(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	})
	return root
}

func TestRootSnapshotPreservesExactTextAndByteBoundaries(t *testing.T) {
	files := map[string][]byte{
		".ledger":           []byte("# état\r\n| pipe | \\ literal\n\n"),
		"notes with space":  []byte(" trailing whitespace \t"),
		"empty":             {},
		"exact-boundary.md": bytes.Repeat([]byte("a"), MaxSourceBytes),
	}
	dir := contextFixture(t, files)
	root := snapshotDirectory(t, dir)
	for name, want := range files {
		t.Run(name, func(t *testing.T) {
			got, err := ReadRootSnapshot(context.Background(), root, name)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("snapshot changed bytes: got %d bytes, want %d: %v", len(got), len(want), err)
			}
		})
	}
	if err := os.WriteFile(filepath.Join(dir, "too-large.md"), bytes.Repeat([]byte("a"), MaxSourceBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadRootSnapshot(context.Background(), root, "too-large.md"); err == nil || got != nil {
		t.Fatalf("oversized snapshot exposed %d bytes: %v", len(got), err)
	}
}

func TestRootSnapshotRejectsNonFlatAndInvalidNames(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"inside": []byte("inside"), "nested/inside": []byte("nested")})
	outside := filepath.Join(filepath.Dir(dir), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	root := snapshotDirectory(t, dir)
	names := []string{"", ".", "..", "./inside", "nested/inside", "../outside", outside, "inside\n", "in\x00side", string([]byte{0xff}), strings.Repeat("a", MaxPathBytes+1)}
	for i, name := range names {
		if data, err := ReadRootSnapshot(context.Background(), root, name); err == nil || data != nil {
			t.Fatalf("invalid name case %d exposed %d bytes: %v", i, len(data), err)
		}
	}
}

func TestRootSnapshotRejectsInvalidTextAndNonregularFiles(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"nul": []byte("a\x00b"), "invalid-utf8": {0xff}, "nested/file": []byte("file")})
	root := snapshotDirectory(t, dir)
	for _, name := range []string{"nul", "invalid-utf8", "nested", "missing"} {
		if data, err := ReadRootSnapshot(context.Background(), root, name); err == nil || data != nil {
			t.Fatalf("invalid source %q exposed %d bytes: %v", name, len(data), err)
		}
	}
}

func TestRootSnapshotRejectsNilAndCanceledContexts(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"file": []byte("content")})
	root := snapshotDirectory(t, dir)
	var absentContext context.Context
	if data, err := ReadRootSnapshot(absentContext, root, "file"); err == nil || data != nil {
		t.Fatalf("nil context accepted: %v", err)
	}
	if data, err := ReadRootSnapshot(context.Background(), nil, "file"); err == nil || data != nil {
		t.Fatalf("nil root accepted: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if data, err := ReadRootSnapshot(ctx, root, "file"); !errors.Is(err, context.Canceled) || data != nil {
		t.Fatalf("canceled read exposed data or lost cancellation: %v", err)
	}
}

func TestOpenDirectoryRejectsInvalidContextAndNonDirectory(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"file": []byte("content")})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		ctx  context.Context
		path string
	}{
		{nil, dir},
		{ctx, dir},
		{ctx, filepath.VolumeName(dir) + string(filepath.Separator)},
		{context.Background(), filepath.Join(dir, "file")},
		{context.Background(), filepath.Join(dir, "missing")},
	}
	for i, tc := range cases {
		root, err := OpenDirectory(tc.ctx, tc.path)
		if root != nil {
			if closeErr := root.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
		if err == nil || root != nil {
			t.Errorf("invalid directory/context case %d accepted: %v", i, err)
		}
		if tc.ctx == ctx && !errors.Is(err, context.Canceled) {
			t.Errorf("case %d lost cancellation: %v", i, err)
		}
	}
}

func TestRootSnapshotRejectsLeafSymlinks(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"file": []byte("must not follow link")})
	root := snapshotDirectory(t, dir)
	for _, target := range []string{"file", filepath.Join(dir, "file"), "missing"} {
		link := filepath.Join(dir, "link")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if data, err := ReadRootSnapshot(context.Background(), root, "link"); err == nil || data != nil {
			t.Fatalf("symlink to %q exposed data: %v", target, err)
		}
		if err := os.Remove(link); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenDirectoryRejectsALeafSymlinkButAcceptsASymlinkedAncestor(t *testing.T) {
	dir := contextFixture(t, map[string][]byte{"child/file": []byte("content")})
	link := filepath.Join(filepath.Dir(dir), "linked")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	// The named root stays strict. Handing in a symlink is the caller naming one directory and
	// being given another, which is the substitution this package exists to prevent.
	root, err := OpenDirectory(context.Background(), link)
	if root != nil {
		if closeErr := root.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if err == nil || root != nil {
		t.Fatalf("a symlink handed in as the root must be refused: %v", err)
	}

	// Reaching a real directory *through* a symlinked ancestor is accepted, and this assertion
	// is the inverse of what it used to be. macOS ships /var and /tmp as symlinks, so the old
	// rule rejected every path under the platform's own temporary directory and left 30 of 56
	// packages unrunnable there (#109). An attacker holding /var does not need a symlink.
	viaAncestor, err := OpenDirectory(context.Background(), filepath.Join(link, "child"))
	if err != nil {
		t.Fatalf("a real directory behind a symlinked ancestor must open: %v", err)
	}
	if err := viaAncestor.Close(); err != nil {
		t.Error(err)
	}
}
