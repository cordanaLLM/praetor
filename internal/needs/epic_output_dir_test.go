// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package needs

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// #297. Boundary: an existing output directory is the caller's and is never tightened, so
// WriteEpicMarkdown does not ask for it to be made (where MkdirSecure would chmod it to 0700).
// Positive: a missing nested directory is created owner-only and the epic is written. Negative:
// an output whose parent is a file fails and creates nothing.
func TestWriteEpicMarkdown_Boundary_ExistingParentIsLeftAlone(t *testing.T) {
	var made []string
	original := epicDirectoryMaker
	epicDirectoryMaker = func(dir string, perm os.FileMode) error {
		made = append(made, dir)
		return original(dir, perm)
	}
	t.Cleanup(func() { epicDirectoryMaker = original })

	existing := t.TempDir()
	before, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := WriteEpicMarkdown(context.Background(), &PreMigrationEpic{}, filepath.Join(existing, "epic.md")); err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(existing)
	if err != nil {
		t.Fatal(err)
	}
	if len(made) != 0 || after.Mode() != before.Mode() {
		t.Fatalf("existing parent: asked to make %v, mode %v -> %v; want it left alone", made, before.Mode(), after.Mode())
	}

	nested := filepath.Join(t.TempDir(), "new", "dir")
	if err := WriteEpicMarkdown(context.Background(), &PreMigrationEpic{}, filepath.Join(nested, "epic.md")); err != nil {
		t.Fatal(err)
	}
	if len(made) != 1 || made[0] != nested {
		t.Fatalf("missing parent: made %v, want %s", made, nested)
	}
	if info, err := os.Stat(filepath.Join(nested, "epic.md")); err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o600) {
		t.Fatalf("epic in the new directory: %v, %v", info, err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteEpicMarkdown(context.Background(), &PreMigrationEpic{}, filepath.Join(file, "epic.md")); err == nil {
		t.Fatal("an output below a file was written")
	}
}
