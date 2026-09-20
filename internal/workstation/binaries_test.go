// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// Positive: swapInto on a non-Windows host is a single rename, first install and replace.
func TestSwapIntoPosixReplacesInOneRename(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "praetorctl")
	if err := os.WriteFile(destination, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapInto("linux", staged, destination); err != nil {
		t.Fatal(err)
	}
	assertContent(t, destination, "new")
	if _, err := os.Stat(staged); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staged file must be consumed by the rename, stat: %v", err)
	}
}

// Positive + boundary: swapInto on Windows renames an existing destination aside before
// placing the staged file, and needs no aside step at all on a first install (destination
// absent). goos is a parameter, so both branches run on every host.
func TestSwapIntoWindowsRenamesAsideFirst(t *testing.T) {
	dir := t.TempDir()
	destination := filepath.Join(dir, "praetorctl")
	if err := os.WriteFile(destination, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapInto(goosWindows, staged, destination); err != nil {
		t.Fatal(err)
	}
	assertContent(t, destination, "new")
	if _, err := os.Stat(destination + ".praetor-previous"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the aside file is scratch and must be removed, stat: %v", err)
	}

	// Boundary: a first install has no destination to rename aside.
	fresh := filepath.Join(dir, "praetor-mcp")
	stagedFresh := filepath.Join(dir, "staged-fresh")
	if err := os.WriteFile(stagedFresh, []byte("first"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := swapInto(goosWindows, stagedFresh, fresh); err != nil {
		t.Fatal(err)
	}
	assertContent(t, fresh, "first")
}

// Negative: a symlink this package did not create is refused, and the target is untouched.
func TestInspectTargetRefusesForeignSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "praetorctl")
	if err := os.Symlink("/somewhere/else", path); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectTarget(path, ""); !errors.Is(err, ErrForeignTarget) {
		t.Fatalf("foreign symlink accepted: %v", err)
	}
	// Windows stores the link target with native separators, so Readlink returns
	// `\somewhere\else` for the same link. The property under test is that the link
	// still points where it did, not which separator the platform writes.
	want := filepath.FromSlash("/somewhere/else")
	target, err := os.Readlink(path)
	if err != nil || target != want {
		t.Fatalf("foreign symlink must be left untouched, got %q, want %q, %v", target, want, err)
	}
}

// Positive: an alias symlink pointing at its expected primary name is accepted.
func TestInspectTargetAcceptsExpectedAlias(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "standardsctl")
	if err := os.Symlink("praetorctl", path); err != nil {
		t.Fatal(err)
	}
	state, err := inspectTarget(path, "praetorctl")
	if err != nil || state.kind != targetSymlink {
		t.Fatalf("expected alias refused: %+v, %v", state, err)
	}
}

// Negative: a non-regular target (a directory) is refused before anything is touched.
func TestInspectTargetRefusesNonRegular(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "praetor-lsp")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectTarget(path, ""); !errors.Is(err, ErrForeignTarget) {
		t.Fatalf("non-regular target accepted: %v", err)
	}
}

// Boundary: an absent target is not an error.
func TestInspectTargetAbsent(t *testing.T) {
	dir := t.TempDir()
	state, err := inspectTarget(filepath.Join(dir, "missing"), "")
	if err != nil || state.kind != targetAbsent {
		t.Fatalf("absent target: %+v, %v", state, err)
	}
}

func assertContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s content = %q, want %q", path, got, want)
	}
}
