// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a local used after a loop, which cannot move into the loop.
func First() {
	x := 0
	for i := 0; i < 2; i++ {
		x += i
	}
	println(x)
}
