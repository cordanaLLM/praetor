// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a variable used only in an if block, which can be narrowed.
func First(b bool) {
	var x int
	if b {
		println(x)
	}
}
