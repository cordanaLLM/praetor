// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package state

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The working directory is created owner-only even in a project that grants others (#101).
func TestOpenWorkingDirectory_Positive_OwnerOnlyDACL(t *testing.T) {
	dir := testsupport.SharedDirectory(t)
	project, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer project.Close()
	working, err := openWorkingDirectory(project)
	if err != nil {
		t.Fatal(err)
	}
	defer working.Close()
	testsupport.RequireOwnerOnly(t, filepath.Join(dir, WorkingDirName))
}
