// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

func First() {
	x := 0
	for i := 0; i < 2; i++ {
		x += i
	}
	println(x)
}
