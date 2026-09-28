package dogfood

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: a fresh evidence directory is created below its run directory, owner-only.
func TestMkdirEvidenceDir_Positive_CreatesOwnerOnly(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "case-01")
	if err := mkdirEvidenceDir(dir); err != nil {
		t.Fatalf("mkdirEvidenceDir: %v", err)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("evidence dir = %v, %v", info, err)
	}
	if util.ModeIsProtection() && info.Mode().Perm() != 0o700 {
		t.Fatalf("evidence dir mode = %#o, want 0700", info.Mode().Perm())
	}
}

// Negative: a link planted at the evidence directory that leaves the run directory is
// refused, and the directory it names keeps its mode instead of being tightened (BUG-826).
func TestMkdirEvidenceDir_Negative_EscapingLinkRefused(t *testing.T) {
	outside := t.TempDir()
	if err := os.Chmod(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	run := t.TempDir()
	link := filepath.Join(run, "case-01")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := mkdirEvidenceDir(link); !errors.Is(err, util.ErrPathEscapesRoot) {
		t.Fatalf("escaping evidence link = %v, want ErrPathEscapesRoot", err)
	}
	if info, err := os.Stat(outside); err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o755) {
		t.Fatalf("outside directory = %v, %v; want its 0755 mode kept", info, err)
	}
}

// Negative: a dogfood report path that is a link is refused instead of written through, so
// the link target keeps its content (BUG-826).
func TestWriteDogfoodReport_Negative_LinkedReportRefused(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "kept.json")
	if err := os.WriteFile(target, []byte("kept"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("kept.json", filepath.Join(dir, "report.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := writeDogfoodReport(filepath.Join(dir, "report.json"), &DogfoodReport{}); !errors.Is(err, util.ErrSymlinkDestination) {
		t.Fatalf("linked report = %v, want ErrSymlinkDestination", err)
	}
	if data, err := os.ReadFile(target); err != nil || string(data) != "kept" { // #nosec G304 -- test-local path
		t.Fatalf("link target = %q, %v; want it untouched", data, err)
	}
}

// Boundary: a link that stays inside the run directory is followed; the confinement edge is
// the run directory itself.
func TestMkdirEvidenceDir_Boundary_InRunLinkFollowed(t *testing.T) {
	run := t.TempDir()
	if err := os.Mkdir(filepath.Join(run, "real"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(run, "case-01")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err := mkdirEvidenceDir(filepath.Join(run, "case-01")); err != nil {
		t.Fatalf("in-run link = %v, want it followed", err)
	}
	if info, err := os.Stat(filepath.Join(run, "real")); err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o700) {
		t.Fatalf("link target = %v, %v; want it tightened to 0700", info, err)
	}
}
