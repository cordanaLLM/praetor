// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a local shared by sibling blocks, which cannot move into either one.
func First(b bool) {
	x := 1
	if b {
		println(x)
	}
	if !b {
		println(x)
	}
}
