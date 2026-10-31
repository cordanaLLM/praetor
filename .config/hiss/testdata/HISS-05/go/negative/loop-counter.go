// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

func Number(xs []string) {
	n := 0
	for _, x := range xs {
		n++
		println(n, x)
	}
}
