package docdistill

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the catalog and a distilled doc are written below the repository and read back.
func TestDocCacheConfined_Positive_RoundTrip(t *testing.T) {
	repo := t.TempDir()
	if err := SaveCachedDoc(repo, sampleDoc("github.com/example/confined")); err != nil {
		t.Fatalf("SaveCachedDoc: %v", err)
	}
	if _, ok := GetCachedDoc(repo, "github.com/example/confined", "v1.0.0"); !ok {
		t.Fatal("cached doc not found after save")
	}
}

// Negative: a catalog planted as a link inside the repository is refused instead of being
// replaced or written through, and the link target keeps its content (BUG-826).
func TestDocCacheConfined_Negative_LinkedCatalog(t *testing.T) {
	repo := t.TempDir()
	docs := filepath.Join(repo, filepath.FromSlash(DocsDirRel))
	if err := os.MkdirAll(docs, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(docs, "kept.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("kept.json", filepath.Join(docs, "catalog.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := SaveCatalog(repo, &DocCatalog{Packages: map[string]DistilledDoc{}}); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked catalog = %v, want ErrSymlinkDestination", err)
	}
	if info, err := os.Lstat(filepath.Join(docs, "catalog.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("catalog link = %v, %v; want the link left in place", info, err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "{}" { // #nosec G304 -- test-local path
		t.Fatalf("link target = %q, %v; want it untouched", data, err)
	}
}

// Boundary: a .workingdir that is a relative link staying inside the repository is followed;
// the confinement edge is the repository root.
func TestDocCacheConfined_Boundary_InRootLinkedWorkingDir(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "state"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("state", filepath.Join(repo, ".workingdir")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := SaveCatalog(repo, &DocCatalog{Packages: map[string]DistilledDoc{}}); err != nil {
		t.Fatalf("in-repository linked .workingdir = %v, want the write to follow it", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "state", "docs", "catalog.json")); err != nil {
		t.Fatalf("catalog not written through the in-repository link: %v", err)
	}
}
