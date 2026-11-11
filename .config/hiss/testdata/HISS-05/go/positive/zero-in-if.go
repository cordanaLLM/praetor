// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a zero-valued local used only in an if block, which can be narrowed.
func First(b bool) {
	n := 0
	if b {
		println(n)
	}
}
