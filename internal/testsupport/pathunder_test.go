// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"testing"
)

// Positive: below a root reached through a symlink, a path that does not exist yet is found
// under the root whichever spelling each side uses, as a config directory under macOS's /var
// (a symlink to /private/var) is before it is created.
func TestPathUnder_Positive_SymlinkedRootAndMissingPath(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	for _, tc := range [][2]string{
		{real, filepath.Join(link, "Library", "Application Support")},
		{link, filepath.Join(real, "Library", "Application Support")},
		{link, link},
	} {
		rel, under, err := PathUnder(tc[0], tc[1])
		if err != nil || !under {
			t.Fatalf("PathUnder(%s, %s) = %q, %v, %v; want under", tc[0], tc[1], rel, under, err)
		}
	}
}

// Negative: a sibling of the root, and a path that leaves the root through "..", are not under it.
func TestPathUnder_Negative_OutsideRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "root")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(parent, "other", "x"), filepath.Join(root, "..", "elsewhere")} {
		if rel, under, err := PathUnder(root, path); err != nil || under {
			t.Fatalf("PathUnder(%s, %s) = %q, %v, %v; want not under", root, path, rel, under, err)
		}
	}
}

// Boundary: a name that only starts with ".." stays under the root.
func TestPathUnder_Boundary_DotDotPrefixedName(t *testing.T) {
	root := t.TempDir()
	if rel, under, err := PathUnder(root, filepath.Join(root, "..cache")); err != nil || !under || rel != "..cache" {
		t.Fatalf("PathUnder over a ..cache child = %q, %v, %v; want under", rel, under, err)
	}
}
