// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

func First() {
	err := f()
	if err != nil {
		println(err)
	}
}

func f() error { return nil }
