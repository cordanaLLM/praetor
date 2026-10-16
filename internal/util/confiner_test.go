// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

// junction makes link a directory junction to target on Windows, and reports whether it could.
func junction(t *testing.T, link, target string) bool {
	t.Helper()
	if runtime.GOOS != "windows" {
		return false
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J %s: %v (%s)", link, err, out)
	}
	return true
}

// confinerFixture returns a root holding dir/file.txt and a directory outside it holding
// secret.txt.
func confinerFixture(t *testing.T) (root, outside string) {
	t.Helper()
	root, outside = t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "dir"), 0o750); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{filepath.Join(root, "dir", "file.txt"), filepath.Join(outside, "secret.txt")} {
		if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return root, outside
}

// A Confiner resolves its root once (#1382) and refuses what ConfinePath refuses. Positive: a file,
// a missing file below the root and the root itself confine to the lexical join. Negative, the
// planted escapes: a symbolic link to outside the root, a junction to outside the root (Windows),
// a ".." path, and the root swapped for a junction to elsewhere after the Confiner was built.
// Boundary: a link inside the root to inside the root stays allowed.
func TestConfiner_3D(t *testing.T) {
	root, outside := confinerFixture(t)
	confiner, err := NewConfiner(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"dir/file.txt", "dir/new/missing.txt", "."} {
		got, err := confiner.Path(rel)
		if want, wantErr := ConfinePath(root, rel); err != nil || got != want || wantErr != nil {
			t.Errorf("Path(%q) = %q, %v; ConfinePath %q, %v", rel, got, err, want, wantErr)
		}
	}
	for _, rel := range []string{"../escape.txt", "dir/../../escape.txt"} {
		if _, err := confiner.Path(rel); !errors.Is(err, ErrPathEscapesRoot) {
			t.Errorf("Path(%q): %v, want ErrPathEscapesRoot", rel, err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Logf("symbolic links unavailable here (%v); the symlink escape is not run", err)
	} else if _, err := confiner.Path("link/secret.txt"); err == nil {
		t.Error("a symbolic link to outside the root was confined")
	}
	if junction(t, filepath.Join(root, "junction"), outside) {
		if _, err := confiner.Path("junction/secret.txt"); err == nil {
			t.Error("a junction to outside the root was confined")
		}
	}
	if err := os.Symlink(filepath.Join(root, "dir"), filepath.Join(root, "inside")); err == nil {
		if got, err := confiner.Path("inside/file.txt"); err != nil || got != filepath.Join(root, "inside", "file.txt") {
			t.Errorf("a link inside the root to inside it: %q, %v", got, err)
		}
	}

	swapped, elsewhere := confinerFixture(t)
	swappedConfiner, err := NewConfiner(swapped)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(swapped, swapped+"-moved"); err != nil {
		t.Fatal(err)
	}
	if !junction(t, swapped, elsewhere) {
		if err := os.Symlink(elsewhere, swapped); err != nil {
			t.Skipf("no junction or symbolic link to swap the root for: %v", err)
		}
	}
	if _, err := swappedConfiner.Path("secret.txt"); !errors.Is(err, ErrConfinementRootChanged) {
		t.Errorf("a root swapped after the Confiner was built: %v, want ErrConfinementRootChanged", err)
	}
}

// ScopeConfiner routes ConfinePath for its root through one Confiner until released, nested
// scopes held by count: within the scope a swapped root is refused as changed, after the last
// release ConfinePath resolves the new root again.
func TestScopeConfiner_3D(t *testing.T) {
	root, elsewhere := confinerFixture(t)
	outer, err := ScopeConfiner(root)
	if err != nil {
		t.Fatal(err)
	}
	inner, err := ScopeConfiner(root + string(filepath.Separator) + ".")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := ConfinePath(root, "dir/file.txt"); err != nil || got != filepath.Join(root, "dir", "file.txt") {
		t.Fatalf("ConfinePath under a scope: %q, %v", got, err)
	}
	if err := os.Rename(root, root+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		t.Fatal(err)
	}
	inner()
	inner()
	if _, err := ConfinePath(root, "dir/file.txt"); !errors.Is(err, ErrConfinementRootChanged) {
		t.Errorf("a root replaced inside a held scope: %v, want ErrConfinementRootChanged", err)
	}
	outer()
	if got, err := ConfinePath(root, "dir/file.txt"); err != nil || got != filepath.Join(root, "dir", "file.txt") {
		t.Errorf("after the last release: %q, %v", got, err)
	}
	if scopedConfinerFor(filepath.Clean(root)) != nil {
		t.Error("the released scope still holds a Confiner")
	}
	_ = elsewhere
}
