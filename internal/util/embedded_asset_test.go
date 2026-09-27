// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func embeddedAssetFixture() fstest.MapFS {
	return fstest.MapFS{
		"listed.txt":   {Data: []byte("listed\n")},
		"unlisted.txt": {Data: []byte("unlisted\n")},
		"sub/deep.txt": {Data: []byte("deep\n")},
	}
}

// Positive: a listed file, nested or not, comes back as a private copy.
func TestReadEmbeddedAsset_Positive(t *testing.T) {
	fsys := embeddedAssetFixture()
	for _, name := range []string{"listed.txt", "sub/deep.txt"} {
		data, err := ReadEmbeddedAsset(fsys, "fixture", []string{"listed.txt", "sub/deep.txt"}, name)
		if err != nil || string(data) != string(fsys[name].Data) {
			t.Fatalf("ReadEmbeddedAsset(%q) = %q, %v", name, data, err)
		}
		data[0] = 'X'
		if fsys[name].Data[0] == 'X' {
			t.Fatalf("ReadEmbeddedAsset(%q) returned the stored bytes, not a copy", name)
		}
	}
}

// Negative: a file the tree holds but the inventory does not name is refused before any
// read, and a listed name the tree lacks reports the read failure with the label.
func TestReadEmbeddedAsset_Negative(t *testing.T) {
	fsys := embeddedAssetFixture()
	if _, err := ReadEmbeddedAsset(fsys, "fixture", []string{"listed.txt"}, "unlisted.txt"); err == nil ||
		err.Error() != `unknown fixture asset "unlisted.txt"` {
		t.Fatalf("unlisted file: %v", err)
	}
	_, err := ReadEmbeddedAsset(fsys, "fixture", []string{"missing.txt"}, "missing.txt")
	if err == nil || !errors.Is(err, fs.ErrNotExist) || !strings.HasPrefix(err.Error(), `read embedded fixture asset "missing.txt": `) {
		t.Fatalf("listed but missing file: %v", err)
	}
}

// Boundary: an empty inventory admits nothing, and a name past MaxEmbeddedAssets is never
// reached by the inventory walk.
func TestReadEmbeddedAsset_Boundary(t *testing.T) {
	fsys := embeddedAssetFixture()
	if _, err := ReadEmbeddedAsset(fsys, "fixture", nil, "listed.txt"); err == nil {
		t.Fatal("an empty inventory admitted a file")
	}
	names := make([]string, MaxEmbeddedAssets, MaxEmbeddedAssets+1)
	names[MaxEmbeddedAssets-1] = "listed.txt"
	if _, err := ReadEmbeddedAsset(fsys, "fixture", names, "listed.txt"); err != nil {
		t.Fatalf("the last name inside the bound was refused: %v", err)
	}
	names[MaxEmbeddedAssets-1] = ""
	names = append(names, "listed.txt")
	if _, err := ReadEmbeddedAsset(fsys, "fixture", names, "listed.txt"); err == nil {
		t.Fatal("a name past the inventory bound was admitted")
	}
}
