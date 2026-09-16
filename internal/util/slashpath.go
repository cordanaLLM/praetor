// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// NormalizeSlashes rewrites a path to its slash form on every platform.
//
// filepath.ToSlash is the obvious choice and the wrong one: it replaces the *host's* separator,
// so on Linux it leaves a Windows-shaped path untouched. Two consequences follow, and the
// second is what keeps catching this repository out. A path recorded on Windows still compares
// unequal to its slash form when the comparison runs on Linux (BUG-948), and a fix written with
// ToSlash cannot be tested anywhere except Windows, because on every other host it does nothing
// at all.
//
// Replacing the backslash unconditionally is correct on Windows and observable on Linux, which
// is what makes the behaviour testable from any machine rather than only from the one that
// breaks.
func NormalizeSlashes(path string) string {
	return strings.ReplaceAll(path, "\\", "/")
}
