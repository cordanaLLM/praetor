package hisscoverage

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReplayBucketDistinguishesAbsentFromMisplaced: a claim may have no bucket, but a regular
// file where a bucket directory belongs must not be replayed as an empty bucket. On Windows the
// read reports not-exist for both, which made the misplaced bucket silently count as empty.
func TestReplayBucketDistinguishesAbsentFromMisplaced(t *testing.T) {
	base := t.TempDir()
	if results, err := replayBucket(t.Context(), base, bucketPositive, "HISS-00", &Report{}); err != nil || len(results) != 0 {
		t.Fatalf("absent bucket was not empty: %v %v", results, err)
	}
	if err := os.WriteFile(filepath.Join(base, bucketPositive), []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := replayBucket(t.Context(), base, bucketPositive, "HISS-00", &Report{}); err == nil {
		t.Fatal("a regular file was replayed as an empty bucket")
	}
}
