package dogfood

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeSnapshotLimitsDefaultsAndBounds(t *testing.T) {
	defaults, err := NormalizeSnapshotLimits(nil)
	if err != nil || defaults.MaxEntries != maxPublicTreeEntries || defaults.MaxFileBytes != maxPublicFileBytes || defaults.MaxTreeBytes != maxPublicTreeBytes {
		t.Fatalf("defaults: %+v %v", defaults, err)
	}
	valid := SnapshotLimits{MaxEntries: 1, MaxFileBytes: 1, MaxTreeBytes: 1}
	if _, err := NormalizeSnapshotLimits(&valid); err != nil {
		t.Fatal(err)
	}
	for _, limits := range []SnapshotLimits{{}, {MaxEntries: maxPublicTreeEntries + 1, MaxFileBytes: 1, MaxTreeBytes: 1}, {MaxEntries: 1, MaxFileBytes: 1 << 30, MaxTreeBytes: 1 << 30}, {MaxEntries: 1, MaxFileBytes: 2, MaxTreeBytes: 1}} {
		if _, err := NormalizeSnapshotLimits(&limits); err == nil {
			t.Fatalf("invalid limits accepted: %+v", limits)
		}
	}
}

func TestSnapshotStreamingExplicitFileLimitAndDigest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "large.bin")
	size := int64(31 << 20)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeRepeated(file, size, 0x5a); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, report, err := snapshotTreeWithLimits(context.Background(), dir, false, nil); err == nil || report.Status != "partial" || report.OffendingPath != "large.bin" || report.Limit != "max_file_bytes" {
		t.Fatalf("default file bound: report=%+v err=%v", report, err)
	}
	limits := SnapshotLimits{MaxEntries: maxPublicTreeEntries, MaxFileBytes: 32 << 20, MaxTreeBytes: 32 << 20}
	tree, report, err := snapshotTreeWithLimits(context.Background(), dir, false, &limits)
	if err != nil || report.Status != "complete" || report.FilesObserved != 1 || report.BytesObserved != size {
		t.Fatalf("explicit bound: report=%+v err=%v", report, err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("%04o:%x", info.Mode().Perm(), sha256.Sum256(data))
	if tree["large.bin"] != want {
		t.Fatalf("digest mismatch: got %q want %q", tree["large.bin"], want)
	}
}

func TestSnapshotReportsCancellationAndFIFO(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if tree, report, err := snapshotTreeWithLimits(ctx, dir, false, nil); err == nil || tree != nil || report.Status != "partial" {
		t.Fatalf("cancellation: tree=%v report=%+v err=%v", tree, report, err)
	}
	fifo := filepath.Join(dir, "fifo")
	if _, err := exec.LookPath("mkfifo"); err != nil {
		t.Skipf("mkfifo unavailable: %v", err)
	}
	if err := exec.CommandContext(t.Context(), "mkfifo", fifo).Run(); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}
	// A successful mkfifo is not a FIFO everywhere. Git for Windows ships an MSYS mkfifo that
	// emulates one with a regular file, fifo.lnk, which the snapshot correctly records as a
	// file; asserting a refusal of it failed a case whose precondition never held.
	if info, err := os.Lstat(fifo); err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Skipf("mkfifo did not create a named pipe on this platform (%v)", err)
	}
	if tree, report, err := snapshotTreeWithLimits(context.Background(), dir, false, nil); err == nil || tree != nil || report.OffendingPath != "fifo" {
		t.Fatalf("fifo: tree=%v report=%+v err=%v", tree, report, err)
	}
}

func writeRepeated(file *os.File, size int64, value byte) error {
	block := []byte(strings.Repeat(string([]byte{value}), 1<<20))
	for written := int64(0); written < size; {
		want := size - written
		if want > int64(len(block)) {
			want = int64(len(block))
		}
		n, err := file.Write(block[:want])
		written += int64(n)
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("short write")
		}
	}
	return nil
}

func TestSnapshotExactBoundsAndIgnoredGit(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	publicWrite(t, filepath.Join(root, ".git", "ignored"), "ignored", 0o600)
	publicWrite(t, filepath.Join(root, "a"), "ab", 0o600)
	publicWrite(t, filepath.Join(root, "b"), "cd", 0o600)
	limits := SnapshotLimits{MaxEntries: 3, MaxFileBytes: 2, MaxTreeBytes: 4}
	tree, report, err := snapshotTreeWithLimits(t.Context(), root, false, &limits)
	if err != nil || report.Status != "complete" || len(tree) != 3 || report.BytesObserved != 4 {
		t.Fatalf("exact bounds with ignored Git: tree=%v report=%+v err=%v", tree, report, err)
	}
	limits.MaxEntries = 2
	if tree, report, err := snapshotTreeWithLimits(t.Context(), root, false, &limits); err == nil || tree != nil || report.Limit != "max_entries" || report.OffendingPath == "" {
		t.Fatalf("entry overflow: tree=%v report=%+v err=%v", tree, report, err)
	}
	limits.MaxEntries, limits.MaxTreeBytes = 3, 3
	if tree, report, err := snapshotTreeWithLimits(t.Context(), root, false, &limits); err == nil || tree != nil || report.Limit != "max_tree_bytes" || report.OffendingPath == "" {
		t.Fatalf("byte overflow: tree=%v report=%+v err=%v", tree, report, err)
	}
}
