// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package workstation

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// The installation lock is created owner-only even in a directory that grants others (#101).
func TestAcquireLock_Positive_OwnerOnlyDACL(t *testing.T) {
	bin := testsupport.SharedDirectory(t)
	release, err := acquireLock(bin)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := release(); err != nil {
			t.Error(err)
		}
	}()
	testsupport.RequireOwnerOnly(t, lockPath(bin))
}
