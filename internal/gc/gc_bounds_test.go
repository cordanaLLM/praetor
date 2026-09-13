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
