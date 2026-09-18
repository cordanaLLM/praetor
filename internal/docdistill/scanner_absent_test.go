// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package docdistill

import (
	"os"
	"path/filepath"
	"testing"
)

// TestScanWorkflowActionsDistinguishesAbsentFromMisplaced: an absent workflow directory yields
// no references, while a regular file where the directory belongs is an error on every host.
func TestScanWorkflowActionsDistinguishesAbsentFromMisplaced(t *testing.T) {
	if refs, err := scanWorkflowActions(t.Context(), t.TempDir()); err != nil || len(refs) != 0 {
		t.Fatalf("absent workflow directory was not an empty scan: %v %v", refs, err)
	}
	for _, file := range []string{".github", filepath.Join(".github", "workflows")} {
		repo := t.TempDir()
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo, file)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo, file), []byte("not a directory"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := scanWorkflowActions(t.Context(), repo); err == nil {
			t.Fatalf("a regular file at %s was read as an empty workflow directory", file)
		}
	}
}
