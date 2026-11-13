// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a captured local, which is conservatively exempt from narrowing.
func First(b bool) {
	x := 1
	_ = func() { println(x) }
	if b {
		println(x)
	}
}
