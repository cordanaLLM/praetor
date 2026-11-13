// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

// Number demonstrates a counter carried across iterations, which must retain its scope.
func Number(xs []string) {
	n := 0
	for _, x := range xs {
		n++
		println(n, x)
	}
}
