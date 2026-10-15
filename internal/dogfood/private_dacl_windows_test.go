// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package dogfood

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Schedule state is saved owner-only even in a directory that grants others (#101).
func TestSaveScheduleJSON_Positive_OwnerOnlyDACL(t *testing.T) {
	dir := testsupport.SharedDirectory(t)
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := saveScheduleJSON(t.Context(), root, "state.json", map[string]int{"version": 1}); err != nil {
		t.Fatal(err)
	}
	testsupport.RequireOwnerOnly(t, filepath.Join(dir, "state.json"))
}
