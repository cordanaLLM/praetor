// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package p

func Seen(xs []string) {
	seen := map[string]bool{}
	for _, x := range xs {
		if seen[x] {
			continue
		}
		seen[x] = true
		println(x)
	}
}
