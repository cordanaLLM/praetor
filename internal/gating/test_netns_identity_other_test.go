// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package gating

import "testing"

// setGateIdentity skips the real planted case where the loopback wrapper does not exist: only
// Linux runs test binaries in a user namespace, so elsewhere there is no wrapper to opt out of
// (HISS-21: a stated skip, never a pass).
func setGateIdentity(t *testing.T) {
	t.Helper()
	t.Skip("the loopback wrapper exists on Linux only: other hosts run no wrapper to opt out of")
}
