package contextopt

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestReplaceSnapshotCreateUpdateAndStaleRejection(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "config")
	if err := EnsureDirectory(t.Context(), dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	before := []byte("first")
	if err := ReplaceSnapshot(t.Context(), path, before, ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSnapshot(t.Context(), path, []byte("second"), ReplaceOptions{Expected: before, Exists: true, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o600) {
		t.Fatalf("private permissions widened: %v, %v", info, err)
	}
	for _, opts := range []ReplaceOptions{{Mode: 0o600}, {Expected: before, Exists: true, Mode: 0o600}} {
		if err := ReplaceSnapshot(t.Context(), path, []byte("lost update"), opts); err == nil {
			t.Fatal("stale or absent expectation replaced existing file")
		}
	}
	data, err := ReadSnapshot(t.Context(), path)
	if err != nil || string(data) != "second" {
		t.Fatalf("successful content lost: %q, %v", data, err)
	}
}

func TestReplaceSnapshotBoundsAndInvalidInputs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bounded")
	exact := bytes.Repeat([]byte("x"), MaxSourceBytes)
	if err := ReplaceSnapshot(t.Context(), path, exact, ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{append(append([]byte{}, exact...), 'x'), {0xff}, []byte("a\x00b")} {
		if err := ReplaceSnapshot(t.Context(), path, data, ReplaceOptions{Expected: exact, Exists: true, Mode: 0o600}); err == nil {
			t.Fatal("invalid or oversized content accepted")
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := ReplaceSnapshot(ctx, path, nil, ReplaceOptions{Mode: 0o600}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled write accepted: %v", err)
	}
	var absent context.Context
	if err := ReplaceSnapshot(absent, path, nil, ReplaceOptions{Mode: 0o600}); err == nil {
		t.Fatal("nil context accepted")
	}
	if err := ReplaceSnapshot(t.Context(), path, nil, ReplaceOptions{Mode: 0o666}); err == nil {
		t.Fatal("world-writable permissions accepted")
	}
	if err := ReplaceSnapshot(t.Context(), filepath.Join(dir, "empty"), nil, ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatalf("empty text creation failed: %v", err)
	}
}

func TestReplaceSnapshotRejectsSymlinkAndKeepsExternalContent(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "target"), []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirectory(t.Context(), filepath.Join(dir, "linked", "child"), 0o700); err == nil {
		t.Fatal("directory creation followed a symlink")
	}
	if err := ReplaceSnapshot(t.Context(), filepath.Join(dir, "linked", "target"), []byte("changed"), ReplaceOptions{Expected: []byte("untouched"), Exists: true, Mode: 0o600}); err == nil {
		t.Fatal("write followed a symlink parent")
	}
	if err := os.Symlink(filepath.Join(outside, "target"), filepath.Join(dir, "target")); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceSnapshot(t.Context(), filepath.Join(dir, "target"), []byte("changed"), ReplaceOptions{Expected: []byte("untouched"), Exists: true, Mode: 0o600}); err == nil {
		t.Fatal("write followed a symlink target")
	}
	data, err := os.ReadFile(filepath.Join(outside, "target"))
	if err != nil || string(data) != "untouched" {
		t.Fatalf("external content changed: %q, %v", data, err)
	}
}

func TestReplaceSnapshotConcurrentWritersKeepOneCompleteResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state")
	before := []byte("before")
	if err := ReplaceSnapshot(t.Context(), path, before, ReplaceOptions{Mode: 0o600}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, value := range []string{"first", "second"} {
		wg.Go(func() {
			results <- ReplaceSnapshot(t.Context(), path, []byte(value), ReplaceOptions{Expected: before, Exists: true, Mode: 0o600})
		})
	}
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	data, err := ReadSnapshot(t.Context(), path)
	if err != nil || succeeded != 1 || (string(data) != "first" && string(data) != "second") {
		t.Fatalf("concurrent update lost integrity: successes=%d data=%q error=%v", succeeded, data, err)
	}
}

func TestSyncDirectoryRejectsAbsentRootAndCancellation(t *testing.T) {
	if err := SyncDirectory(t.Context(), nil); err == nil {
		t.Fatal("nil directory accepted")
	}
	root, err := OpenDirectory(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SyncDirectory(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation did not propagate: %v", err)
	}
}
