package baseline

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

const baselineTestName = ".standards-baseline.json"

func emptySnapshot() *Baseline {
	return &Baseline{Version: 1, Infractions: []Infraction{}}
}

// Positive: a saved snapshot loads back, and a second save replaces it.
func TestSaveBaselineConfined_Positive_SaveAndReplace(t *testing.T) {
	path := filepath.Join(t.TempDir(), baselineTestName)
	for _, repo := range []string{"acme/first", "acme/second"} {
		b := emptySnapshot()
		b.Repository = repo
		if err := SaveBaseline(path, b); err != nil {
			t.Fatalf("SaveBaseline: %v", err)
		}
		loaded, err := LoadBaseline(path)
		if err != nil || loaded.Repository != repo {
			t.Fatalf("loaded = %+v, %v; want repository %q", loaded, err, repo)
		}
	}
}

// Negative: a link planted at the baseline path is refused instead of written through, both
// one that leaves the repository and one that stays inside it (BUG-826).
func TestSaveBaselineConfined_Negative_LinkedDestination(t *testing.T) {
	outside := filepath.Join(t.TempDir(), "victim.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, baselineTestName)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := SaveBaseline(filepath.Join(repo, baselineTestName), emptySnapshot()); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Errorf("escaping baseline link = %v, want ErrPathEscapesRoot", err)
	}

	inRepo := t.TempDir()
	sibling := filepath.Join(inRepo, "other.json")
	if err := os.WriteFile(sibling, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("other.json", filepath.Join(inRepo, baselineTestName)); err != nil {
		t.Fatal(err)
	}
	if err := SaveBaseline(filepath.Join(inRepo, baselineTestName), emptySnapshot()); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Errorf("in-repository baseline link = %v, want ErrSymlinkDestination", err)
	}
	for path, body := range map[string]string{outside: "outside", sibling: "kept"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != body { // #nosec G304 -- test-local path
			t.Errorf("%s = %q, %v; want %q untouched", path, data, err, body)
		}
	}
}

// Boundary: FilePerm is a ceiling, so replacing a baseline the owner narrowed keeps the
// narrower mode instead of widening it back to 0644.
func TestSaveBaselineConfined_Boundary_PermissionCeiling(t *testing.T) {
	if !util.ModeIsProtection() {
		t.Skip("permission bits are not enforced on this platform")
	}
	path := filepath.Join(t.TempDir(), baselineTestName)
	if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := SaveBaseline(path, emptySnapshot()); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("replaced baseline mode = %v, %v; want 0600 kept", info, err)
	}
}
