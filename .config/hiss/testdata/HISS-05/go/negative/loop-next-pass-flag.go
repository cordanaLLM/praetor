// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

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
