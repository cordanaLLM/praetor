// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

func First(b bool) {
	x := compute()
	if b {
		println(x)
	}
}

func compute() int { return 1 }
