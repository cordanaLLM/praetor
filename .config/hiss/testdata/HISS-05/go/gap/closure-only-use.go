// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates a local used only by a closure, which remains a known scope-analysis gap.
func First(b bool) {
	x := 1
	if b {
		func() { println(x) }()
	}
}
