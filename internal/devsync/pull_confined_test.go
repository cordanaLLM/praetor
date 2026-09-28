package devsync

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Negative: a directory already inside the pull target that links outside it is refused
// before an archive's directory is created through it (BUG-826).
func TestPullArchive_Negative_EscapingLinkRefused(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	target, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(target, "dev"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(target, "dev", "org")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opts := PullOptions{Remote: DefaultRemote, Host: "ws1", Rclone: rclone}
	outcome := pullArchive(ctx, opts, target, RemoteArchive{Host: "ws1", Path: "dev/org/app" + archiveSuffix})
	if outcome.Status != StatusFailed || !errors.Is(outcome.Err, util.ErrPathEscapesRoot) {
		t.Fatalf("outcome = %+v, want a failed escape", outcome)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("outside directory = %v, %v; want nothing created through the link", entries, err)
	}
}

// Boundary: a link inside the pull target that stays inside it is followed; the archive's
// directory is created at its target and only the (absent) download fails.
func TestPullArchive_Boundary_InTargetLinkFollowed(t *testing.T) {
	ctx, rclone, _ := newFakeRclone(t, "")
	target := t.TempDir()
	for _, dir := range []string{"dev", filepath.Join("real", "org")} {
		if err := os.MkdirAll(filepath.Join(target, dir), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join("..", "real", "org"), filepath.Join(target, "dev", "org")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	opts := PullOptions{Remote: DefaultRemote, Host: "ws1", Rclone: rclone}
	outcome := pullArchive(ctx, opts, target, RemoteArchive{Host: "ws1", Path: "dev/org/app" + archiveSuffix})
	if errors.Is(outcome.Err, util.ErrPathEscapesRoot) {
		t.Fatalf("in-target link refused as an escape: %v", outcome.Err)
	}
	if info, err := os.Stat(filepath.Join(target, "real", "org", "app")); err != nil || !info.IsDir() {
		t.Fatalf("archive directory = %v, %v; want it created through the in-target link", info, err)
	}
}
