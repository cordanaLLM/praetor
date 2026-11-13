// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a call initializer whose evaluation order must be preserved.
func First(b bool) {
	x := compute()
	if b {
		println(x)
	}
}

// compute supplies an initializer call whose ordering the fixture must preserve.
func compute() int { return 1 }
