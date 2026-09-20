// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "os"

// SameDirectory reports whether left and right name the same existing directory,
// deciding by filesystem identity (os.SameFile) rather than by string equality.
//
// One directory has many spellings, and which one a caller holds depends on who produced
// it. macOS reaches its own TMPDIR through /var -> /private/var, so a path a test handed in
// and the path git answered with differ by an ancestor symlink. Windows hands out 8.3 short
// names (C:\Users\RUNNER~1\...) where git's GetFinalPathNameByHandleW hands back the long
// one (C:\Users\runneradmin\...), and the drive letter's case is not fixed either. A string
// comparison rejects every one of those pairs while the two operands address the identical
// directory, which is what failed the macOS and Windows legs of the Platform Neutrality
// matrix (#135). Identity answers the question the callers actually ask.
//
// It fails closed: an empty spelling, a path that cannot be stat'd, or a path that does not
// exist is not the same directory as anything. Callers relying on that refusal are refusing
// an escape, so a missing path must never read as a match.
//
// This is the one implementation (HISS-19). It replaces internal/harvester.sameDirectory and
// internal/adopt.samePath, which answered the same question two ways: the latter compared
// filepath.EvalSymlinks output as strings and fell back to the unresolved spelling when
// resolution failed, so it could still report two different directories as one.
func SameDirectory(left, right string) bool {
	if left == "" || right == "" {
		return false
	}
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false
	}
	return os.SameFile(leftInfo, rightInfo)
}
