// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"fmt"
	"io/fs"
)

// MaxEmbeddedAssets bounds the inventory walk of ReadEmbeddedAsset (HISS-02).
const MaxEmbeddedAssets = 1024

// ReadEmbeddedAsset returns a private copy of one file of an embedded asset tree. names is
// the tree's complete inventory: a name outside it is refused before fsys is read, so an
// embed pattern that matches more than the inventory cannot hand out an unlisted file, and a
// caller cannot mutate the embedded bytes through the returned slice. label names the tree
// in errors. Every go:embed package whose files adoption emits reads through this one
// function, so a new asset family adds an inventory, not a reader (HISS-19).
func ReadEmbeddedAsset(fsys fs.FS, label string, names []string, name string) ([]byte, error) {
	known := false
	for index := 0; index < len(names) && index < MaxEmbeddedAssets; index++ {
		known = known || names[index] == name
	}
	if !known {
		return nil, fmt.Errorf("unknown %s asset %q", label, name)
	}
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, fmt.Errorf("read embedded %s asset %q: %w", label, name, err)
	}
	return append([]byte(nil), data...), nil
}
