// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// writeModule writes a Go module named example.com/m with the given files under a temporary
// root and returns the root and its sorted file inventory, as git would list it.
func writeModule(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.com/m\n\ngo 1.27\n"
	inventory := make([]string, 0, len(files))
	for rel, content := range files {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
		inventory = append(inventory, rel)
	}
	slices.Sort(inventory)
	return root, inventory
}
