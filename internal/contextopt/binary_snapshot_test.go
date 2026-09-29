package contextopt

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadBinarySnapshotExactBytesAndBoundary(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "asset.bin")
	content := bytes.Repeat([]byte{0xff, 0}, MaxSourceBytes/2)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadBinarySnapshot(t.Context(), path)
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("binary boundary changed: %v", err)
	}
	if _, err := ReadSnapshot(t.Context(), path); err == nil {
		t.Fatal("text reader accepted binary input")
	}
	if err := os.WriteFile(path, append(content, 1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadBinarySnapshot(t.Context(), path); err == nil {
		t.Fatal("binary reader accepted overflow")
	}
}

// TestDigestBinarySnapshotStreamsPastReadBound digests content larger than
// MaxSourceBytes, which ReadBinarySnapshot refuses, and checks the exact bound.
func TestDigestBinarySnapshotStreamsPastReadBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "artifact.bin")
	content := bytes.Repeat([]byte{0x5a, 0}, MaxSourceBytes)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	limit := int64(len(content))
	digest, size, err := DigestBinarySnapshot(t.Context(), path, limit)
	if err != nil || digest != hex.EncodeToString(sum[:]) || size != limit {
		t.Fatalf("digest at exact bound = %q, %d, %v", digest, size, err)
	}
	if _, err := ReadBinarySnapshot(t.Context(), path); err == nil {
		t.Fatal("buffered reader accepted content past MaxSourceBytes")
	}
	if _, _, err := DigestBinarySnapshot(t.Context(), path, limit-1); err == nil {
		t.Fatal("digest accepted a file one byte over its bound")
	}
	for _, bad := range []int64{0, -1} {
		if _, _, err := DigestBinarySnapshot(t.Context(), path, bad); err == nil {
			t.Fatalf("non-positive limit %d accepted", bad)
		}
	}
}

func TestDigestBinarySnapshotEmptyAndInvalidInputs(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	digest, size, err := DigestBinarySnapshot(t.Context(), empty, 1)
	if err != nil || size != 0 || digest != "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855" {
		t.Fatalf("empty file digest = %q, %d, %v", digest, size, err)
	}
	for _, path := range []string{filepath.Join(root, "missing"), root} {
		if _, _, err := DigestBinarySnapshot(t.Context(), path, 1<<20); err == nil {
			t.Fatalf("invalid path accepted: %s", path)
		}
	}
	var missingContext context.Context
	if _, _, err := DigestBinarySnapshot(missingContext, empty, 1); err == nil {
		t.Fatal("nil context accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(empty, link); err != nil {
		t.Skipf("symlinks unavailable on this platform or account: %v", err)
	}
	if _, _, err := DigestBinarySnapshot(t.Context(), link, 1); err == nil {
		t.Fatal("symlink accepted")
	}
}

// failingObserver refuses every write, standing in for an inspector that cannot continue.
type failingObserver struct{}

func (failingObserver) Write([]byte) (int, error) { return 0, errors.New("observer refused") }

// TestDigestBinarySnapshotToObserverSeesDigestedBytes checks that the observer receives
// exactly the bytes the digest covers, that an observer error fails the digest, and that
// an empty file and an over-bound file behave as they do without an observer.
func TestDigestBinarySnapshotToObserverSeesDigestedBytes(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "artifact.bin")
	content := bytes.Repeat([]byte{0x7f, 'E', 'L', 'F'}, MaxSourceBytes/2)
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
	var seen bytes.Buffer
	digest, size, err := DigestBinarySnapshotTo(t.Context(), path, int64(len(content)), &seen)
	plain, _, plainErr := DigestBinarySnapshot(t.Context(), path, int64(len(content)))
	if err != nil || plainErr != nil || digest != plain || size != int64(len(content)) || !bytes.Equal(seen.Bytes(), content) {
		t.Fatalf("observed digest = %q (plain %q), %d, %v, %v; observer saw %d of %d bytes", digest, plain, size, err, plainErr, seen.Len(), len(content))
	}

	if _, _, err := DigestBinarySnapshotTo(t.Context(), path, int64(len(content)), failingObserver{}); err == nil || !strings.Contains(err.Error(), "observer refused") {
		t.Fatalf("observer write error not returned: %v", err)
	}
	if _, _, err := DigestBinarySnapshotTo(t.Context(), path, int64(len(content))-1, &bytes.Buffer{}); err == nil {
		t.Fatal("observed digest accepted a file one byte over its bound")
	}

	empty := filepath.Join(root, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	seen.Reset()
	if _, size, err := DigestBinarySnapshotTo(t.Context(), empty, 1, &seen); err != nil || size != 0 || seen.Len() != 0 {
		t.Fatalf("empty file: size %d, observer saw %d bytes, err %v", size, seen.Len(), err)
	}
}

func TestReadBinarySnapshotRejectsInvalidPathsAndContext(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "missing")
	link := filepath.Join(root, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{target, link, root} {
		if _, err := ReadBinarySnapshot(t.Context(), path); err == nil {
			t.Fatalf("invalid path accepted: %s", path)
		}
	}
	var missingContext context.Context
	if _, err := ReadBinarySnapshot(missingContext, target); err == nil {
		t.Fatal("nil context accepted")
	}
}
