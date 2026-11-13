// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// Nested declarations remain outside the function-top check.
func First(b bool) {
	if b {
		x := 1
		{
			x := x + 1
			{
				println(x)
			}
		}
	}
}
