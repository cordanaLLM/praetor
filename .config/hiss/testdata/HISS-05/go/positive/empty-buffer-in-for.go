// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

func First() {
	buf := []byte{}
	for i := 0; i < 2; i++ {
		println(buf)
	}
}
