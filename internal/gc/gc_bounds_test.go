package gc

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCollect_OverLimitReleasedArtifactFailsClosed(t *testing.T) {
	root := t.TempDir()
	eph := filepath.Join(root, ".standards", "ephemeral", "overflow")
	if err := os.MkdirAll(eph, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i <= MaxFileScanLimit; i++ {
		path := filepath.Join(eph, "f-"+strconv.Itoa(i))
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("write overflow fixture: %v", err)
		}
		if err := os.Chtimes(path, time.Now().Add(-48*time.Hour), time.Now().Add(-48*time.Hour)); err != nil {
			t.Fatalf("age overflow fixture: %v", err)
		}
	}
	report, err := Collect(context.Background(), Options{
		RootDir:        root,
		EphemeralDir:   filepath.Join(root, ".standards", "ephemeral"),
		ReleasedPaths:  []string{".standards/ephemeral/overflow"},
		MaxArtifactAge: 24 * time.Hour,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	})
	if err == nil || report == nil || report.Complete {
		t.Fatalf("over-limit scan must be incomplete: report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(eph); statErr != nil {
		t.Fatalf("over-limit artifact was removed: %v", statErr)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "limit") && !strings.Contains(strings.ToLower(err.Error()), "bound") {
		t.Fatalf("expected bound diagnostic, got %v", err)
	}
}

// poolWithEntries fills the default ephemeral pool with count empty files.
func poolWithEntries(t *testing.T, count int) string {
	t.Helper()
	root := t.TempDir()
	pool := filepath.Join(root, ".standards", "ephemeral")
	if err := os.MkdirAll(pool, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(pool, "e-"+strconv.Itoa(i)), nil, 0o600); err != nil {
			t.Fatalf("write pool fixture: %v", err)
		}
	}
	return root
}

// TestCollect_PoolEntryBound pins BUG-232: a pool holding MaxEntriesLimit direct entries is
// scanned in full, and one more fails collection with the bound named in report.Errors
// instead of planning from a truncated listing.
func TestCollect_PoolEntryBound(t *testing.T) {
	atLimit, err := Collect(context.Background(), Options{RootDir: poolWithEntries(t, MaxEntriesLimit), DryRun: true})
	if err != nil || !atLimit.Complete {
		t.Fatalf("a pool at the %d-entry bound must scan: %v", MaxEntriesLimit, err)
	}
	if got := len(atLimit.SkippedArtifacts); got != MaxEntriesLimit {
		t.Fatalf("every entry of a pool at the bound must be accounted for, got %d", got)
	}
	over, err := Collect(context.Background(), Options{RootDir: poolWithEntries(t, MaxEntriesLimit+1), DryRun: true})
	if err == nil || !strings.Contains(err.Error(), "scan entry limit exceeded") {
		t.Fatalf("a pool over the bound must fail closed, got %v", err)
	}
	if over.Complete || len(over.Errors) != 1 || !strings.Contains(over.Errors[0], "scan entry limit exceeded") {
		t.Fatalf("the bound must be reported in report.Errors: %+v", over)
	}
}

func TestCollect_NestedSymlinkInReleasedArtifactFailsClosed(t *testing.T) {
	root := t.TempDir()
	eph := filepath.Join(root, ".standards", "ephemeral", "linked")
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "secret")
	writeFileT(t, outsideFile, "must survive")
	if err := os.MkdirAll(eph, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(eph, "outside-link")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	ageTree(t, eph, 48*time.Hour)
	report, err := Collect(context.Background(), Options{
		RootDir:        root,
		EphemeralDir:   filepath.Join(root, ".standards", "ephemeral"),
		ReleasedPaths:  []string{".standards/ephemeral/linked"},
		MaxArtifactAge: 24 * time.Hour,
		SkipGitPrune:   true,
		SkipTestCache:  true,
	})
	if err == nil || report == nil || report.Complete {
		t.Fatalf("nested symlink scan must be incomplete: report=%+v err=%v", report, err)
	}
	if _, statErr := os.Stat(outsideFile); statErr != nil {
		t.Fatalf("outside target was modified: %v", statErr)
	}
}
