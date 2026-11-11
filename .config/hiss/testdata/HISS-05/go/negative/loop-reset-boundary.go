// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

// Conservatively exempt: no control-flow proof that reset dominates every read.
func Reset(xs []int) {
	n := 0
	for _, x := range xs {
		n = 0
		n += x
		println(n)
	}
}
