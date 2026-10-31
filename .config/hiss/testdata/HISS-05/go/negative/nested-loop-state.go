// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

func Nested(xs []int) {
	n := 0
	if len(xs) > 0 {
		for _, x := range xs {
			for j := 0; j < 2; j++ {
				n += x
				println(n)
			}
		}
	}
}
