// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

// Nested demonstrates state mutated by nested loops, which must retain its scope.
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
