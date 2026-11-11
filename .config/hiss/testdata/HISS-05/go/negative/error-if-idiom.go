// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package p

// First demonstrates an error-handling initializer whose call must remain in order.
func First() {
	err := f()
	if err != nil {
		println(err)
	}
}

// f supplies the error-producing call used by the fixture.
func f() error { return nil }
