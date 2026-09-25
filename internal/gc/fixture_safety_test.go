package gc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestAgeTree_Negative_LeavesSymlinkTargetsUntouched is the defect: os.Chtimes follows
// symlinks, so backdating a fixture that contains one wrote to a file outside the fixture.
// The gc suite plants exactly such a link (gc_bounds_test.go), and on a developer's machine
// the target is whatever the link names.
func TestAgeTree_Negative_LeavesSymlinkTargetsUntouched(t *testing.T) {
	outside := t.TempDir()
	target := writeFileT(t, filepath.Join(outside, "target"), "must keep its mtime")
	fixture := mkdirT(t, filepath.Join(t.TempDir(), "fixture"))
	writeFileT(t, filepath.Join(fixture, "real.txt"), "aged")
	if err := os.Symlink(target, filepath.Join(fixture, "link")); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}

	before, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	ageTree(t, fixture, 48*time.Hour)

	after, err := os.Lstat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("the link target was backdated from %s to %s; ageTree wrote outside its fixture",
			before.ModTime(), after.ModTime())
	}
}

// TestAgeTree_Positive_BackdatesTheFixtureItself keeps the skip narrow: real entries and the
// directories holding them are still aged, which is what the staleness fixtures depend on.
func TestAgeTree_Positive_BackdatesTheFixtureItself(t *testing.T) {
	fixture := mkdirT(t, filepath.Join(t.TempDir(), "fixture"))
	nested := mkdirT(t, filepath.Join(fixture, "nested"))
	file := writeFileT(t, filepath.Join(nested, "real.txt"), "aged")

	cutoff := time.Now().Add(-24 * time.Hour)
	ageTree(t, fixture, 48*time.Hour)

	for _, path := range []string{fixture, nested, file} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.ModTime().After(cutoff) {
			t.Errorf("%s was not backdated: mtime %s is newer than %s", path, info.ModTime(), cutoff)
		}
	}
}

// TestAgeTree_Boundary_SingleRegularFile covers the smallest input the helper accepts: one
// file, handed directly rather than as a tree.
func TestAgeTree_Boundary_SingleRegularFile(t *testing.T) {
	file := writeFileT(t, filepath.Join(t.TempDir(), "solo.json"), "{}")
	cutoff := time.Now().Add(-24 * time.Hour)
	ageTree(t, file, 48*time.Hour)

	info, err := os.Lstat(file)
	if err != nil {
		t.Fatal(err)
	}
	if info.ModTime().After(cutoff) {
		t.Errorf("a single file must be backdated, mtime %s", info.ModTime())
	}
}
