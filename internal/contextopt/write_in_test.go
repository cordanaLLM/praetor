package contextopt

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// symlinkOrSkip creates link -> target, or skips: a host without symlinks cannot hold the
// symlinked component these tests refuse.
func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable on this host: %v", err)
	}
}

func readFileOrEmpty(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

// Positive: WriteSnapshotIn creates the absent directories below root, publishes the file and
// replaces it on the next call; ensureDirectoryIn on "." pins root itself.
func TestWriteSnapshotIn_Positive_CreatesAndReplaces(t *testing.T) {
	root := t.TempDir()
	rel := filepath.Join(".claude", "agents", "reviewer.md")
	for _, value := range []string{"original\n", "replacement\n"} {
		if err := WriteSnapshotIn(t.Context(), root, rel, []byte(value), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := readFileOrEmpty(t, filepath.Join(root, rel)); got != value {
			t.Fatalf("got %q, want %q", got, value)
		}
	}
	dir, err := ensureDirectoryIn(t.Context(), root, ".", 0o755)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := dir.Close(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := dir.Stat(rel); err != nil {
		t.Fatalf("ensureDirectoryIn(\".\") did not pin root: %v", err)
	}
}

// Negative: an existing directory component that is a symlink is refused rather than written
// through, whether it points out of root or at another directory inside it, and a symlinked
// file is refused too. WriteSnapshot resolved the existing ancestry, so a symlinked .claude
// whose target already held agents/ received the write.
func TestWriteSnapshotIn_Negative_RefusesSymlinkedComponents(t *testing.T) {
	cases := map[string]struct {
		outside bool
		leaf    bool // the file itself is the link, not its directory
	}{
		"directory out of root": {outside: true},
		"directory inside root": {},
		"file inside root":      {leaf: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			redirected := filepath.Join(root, "elsewhere")
			if tc.outside {
				redirected = t.TempDir()
			}
			victim := filepath.Join(redirected, "agents", "reviewer.md")
			if err := os.MkdirAll(filepath.Dir(victim), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(victim, []byte("victim\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if tc.leaf {
				if err := os.MkdirAll(filepath.Join(root, ".claude", "agents"), 0o700); err != nil {
					t.Fatal(err)
				}
				symlinkOrSkip(t, victim, filepath.Join(root, ".claude", "agents", "reviewer.md"))
			} else {
				symlinkOrSkip(t, redirected, filepath.Join(root, ".claude"))
			}
			rel := filepath.Join(".claude", "agents", "reviewer.md")
			if err := WriteSnapshotIn(t.Context(), root, rel, []byte("persona\n"), 0o644); err == nil {
				t.Fatal("wrote through a symlink below root")
			}
			if got := readFileOrEmpty(t, victim); got != "victim\n" {
				t.Fatalf("the link target changed: %q", got)
			}
		})
	}
}

// Negative: a path that leaves root, a root that does not exist, a directory mode above 0755
// and invalid content are refused, and none of them creates anything.
func TestWriteSnapshotIn_Negative_RefusesInvalidRequests(t *testing.T) {
	root := t.TempDir()
	absent := filepath.Join(t.TempDir(), "absent")
	for name, call := range map[string]func() error{
		"escaping rel": func() error {
			return WriteSnapshotIn(t.Context(), root, filepath.Join("..", "x.md"), []byte("x"), 0o644)
		},
		"absent root": func() error {
			return WriteSnapshotIn(t.Context(), absent, filepath.Join("a", "x.md"), []byte("x"), 0o644)
		},
		"invalid text": func() error {
			return WriteSnapshotIn(t.Context(), root, filepath.Join("a", "x.md"), []byte{0xff}, 0o644)
		},
		"wide mode": func() error {
			_, err := ensureDirectoryIn(t.Context(), root, "a", 0o777)
			return err
		},
	} {
		if err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, path := range []string{absent, filepath.Join(root, "a"), filepath.Join(filepath.Dir(root), "x.md")} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s was created: %v", path, err)
		}
	}
}

// Boundary: root's own ancestry is resolved, as on macOS where TMPDIR sits below the /var
// symlink. The directory of rel may be MaxPathDepth components deep, the bound OpenDirectoryIn
// reads with; one component more is refused rather than truncated, and creates nothing.
func TestWriteSnapshotIn_Boundary_SymlinkedRootAncestryAndDepth(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "private", "var")
	if err := os.MkdirAll(filepath.Join(real, "repo"), 0o700); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, real, filepath.Join(base, "var"))
	root := filepath.Join(base, "var", "repo")
	if err := WriteSnapshotIn(t.Context(), root, filepath.Join("a", "x.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatalf("a symlinked ancestor of root was refused: %v", err)
	}
	atDepth := strings.Repeat("d"+string(filepath.Separator), MaxPathDepth) + "x.md"
	if err := WriteSnapshotIn(t.Context(), root, atDepth, []byte("x\n"), 0o644); err != nil {
		t.Fatalf("a directory of exactly %d components was refused: %v", MaxPathDepth, err)
	}
	overDepth := strings.Repeat("e"+string(filepath.Separator), MaxPathDepth+1) + "x.md"
	if err := WriteSnapshotIn(t.Context(), root, overDepth, []byte("x\n"), 0o644); err == nil {
		t.Fatalf("a directory of %d components was accepted", MaxPathDepth+1)
	}
	if _, err := os.Lstat(filepath.Join(root, "e")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("an over-deep rel created directories: %v", err)
	}
}
