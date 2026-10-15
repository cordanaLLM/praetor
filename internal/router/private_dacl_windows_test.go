// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package router

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The outcome log and its directory are created owner-only even under a directory that
// grants others (#101).
func TestWriteOutcomeRecord_Positive_OwnerOnlyDACL(t *testing.T) {
	path := filepath.Join(testsupport.SharedDirectory(t), "outcomes", "log.jsonl")
	if err := writeOutcomeRecord(path, []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	testsupport.RequireOwnerOnly(t, filepath.Dir(path))
	testsupport.RequireOwnerOnly(t, path)
}
