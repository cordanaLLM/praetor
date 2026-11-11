// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates an address-taken local, which must remain outside the child block.
func First(b bool) {
	x := 1
	if b {
		println(&x)
	}
}
