// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

// Nested demonstrates a read-only local confined to nested loops, which can be narrowed.
func Nested(xs []int) {
	n := 1
	for _, x := range xs {
		for j := 0; j < 2; j++ {
			println(n, x)
		}
	}
}
