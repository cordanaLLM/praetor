// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package contextopt

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// A private candidate file is written owner-only even in a directory that grants others (#101).
func TestWritePrivate_Positive_OwnerOnlyDACL(t *testing.T) {
	dir := testsupport.SharedDirectory(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writePrivate(t.Context(), root, "candidate.md", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	testsupport.RequireOwnerOnly(t, filepath.Join(dir, "candidate.md"))
}
