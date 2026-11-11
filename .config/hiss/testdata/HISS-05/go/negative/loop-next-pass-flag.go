// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

// Skip demonstrates a flag carried to the next iteration, which must retain its scope.
func Skip(xs []string) {
	skip := false
	for _, x := range xs {
		if skip {
			skip = false
			continue
		}
		skip = x == "skip"
		println(x)
	}
}
