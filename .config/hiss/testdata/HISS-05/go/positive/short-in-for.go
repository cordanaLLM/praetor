// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a short declaration used only in a loop, which can be narrowed.
func First() {
	x := 1
	for i := 0; i < 2; i++ {
		println(x)
	}
}
