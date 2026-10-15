// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package repairrun

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// A workspace file is created owner-only even in a directory that grants others (#101).
func TestWriteNew_Positive_OwnerOnlyDACL(t *testing.T) {
	dir := testsupport.SharedDirectory(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := writeNew(root, "input.json", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	testsupport.RequireOwnerOnly(t, filepath.Join(dir, "input.json"))
}
