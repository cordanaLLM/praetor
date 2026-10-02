package contextopt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// keepModeFile writes name under a fresh directory with content "before" and the given mode.
func keepModeFile(t *testing.T, name string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("before"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Chmod, not the create mode: the process umask must not decide the fixture.
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func keepModePerm(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Mode().Perm()
}

// requireModeBits skips where a file mode is not what the host stores: Windows keeps one
// read-only attribute, so 0755 and 0644 are the same file there.
func requireModeBits(t *testing.T) {
	t.Helper()
	if !util.ModeIsProtection() {
		t.Skip("this host stores no permission bits beyond read-only; the kept mode cannot be observed")
	}
}

// TestReplaceSnapshotKeepMode_Positive: under KeepMode a replacement carries the permission
// bits the file had, whichever side of the Mode ceiling they are on.
func TestReplaceSnapshotKeepMode_Positive(t *testing.T) {
	requireModeBits(t)
	for _, mode := range []os.FileMode{0o755, 0o775, 0o700, 0o644, 0o600, 0o444} {
		path := keepModeFile(t, "kept.md", mode)
		opts := ReplaceOptions{Expected: []byte("before"), Exists: true, Mode: 0o644, KeepMode: true}
		if err := ReplaceSnapshot(t.Context(), path, []byte("after"), opts); err != nil {
			t.Fatalf("mode %o: %v", mode, err)
		}
		if got := keepModePerm(t, path); got != mode {
			t.Errorf("mode %o became %o", mode, got)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "after" {
			t.Errorf("mode %o: content %q, %v", mode, data, err)
		}
	}
}

// TestReplaceSnapshotKeepMode_Negative: without KeepMode the ceiling still narrows, and a
// replacement KeepMode refuses leaves content and mode alone.
func TestReplaceSnapshotKeepMode_Negative(t *testing.T) {
	requireModeBits(t)
	path := keepModeFile(t, "ceiling.md", 0o755)
	if err := ReplaceSnapshot(t.Context(), path, []byte("after"), ReplaceOptions{Expected: []byte("before"), Exists: true, Mode: 0o644}); err != nil {
		t.Fatal(err)
	}
	if got := keepModePerm(t, path); got != 0o644 {
		t.Errorf("the ceiling must still apply without KeepMode: %o", got)
	}

	stale := keepModeFile(t, "stale.md", 0o755)
	err := ReplaceSnapshot(t.Context(), stale, []byte("after"), ReplaceOptions{Expected: []byte("other"), Exists: true, Mode: 0o644, KeepMode: true})
	if err == nil {
		t.Fatal("a stale expectation replaced the file under KeepMode")
	}
	if data, readErr := os.ReadFile(stale); readErr != nil || string(data) != "before" || keepModePerm(t, stale) != 0o755 {
		t.Errorf("a refused replacement changed the file: %q, %v, %o", data, readErr, keepModePerm(t, stale))
	}
}

// TestReplaceSnapshotKeepMode_Boundary: KeepMode has nothing to keep for a file it creates,
// which takes Mode, and it never lifts the bound on Mode itself.
func TestReplaceSnapshotKeepMode_Boundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created.md")
	if err := ReplaceSnapshot(t.Context(), path, []byte("new"), ReplaceOptions{Mode: 0o600, KeepMode: true}); err != nil {
		t.Fatal(err)
	}
	if got := keepModePerm(t, path); util.ModeIsProtection() && got != 0o600 {
		t.Errorf("a created file must take Mode: %o", got)
	}
	for _, mode := range []os.FileMode{0, 0o755, 0o666} {
		opts := ReplaceOptions{Expected: []byte("new"), Exists: true, Mode: mode, KeepMode: true}
		if err := ReplaceSnapshot(t.Context(), path, []byte("again"), opts); err == nil {
			t.Errorf("Mode %o accepted under KeepMode", mode)
		}
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "new" {
		t.Errorf("a refused Mode changed the file: %q, %v", data, err)
	}
}
