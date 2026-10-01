// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "strings"

// ShortCommitLen is how many characters of a commit id identify it in report lines.
const ShortCommitLen = 12

// ShortCommit abbreviates a commit id to ShortCommitLen characters for output. Surrounding
// whitespace (a trailing newline from git) is trimmed first; a shorter id comes back whole.
func ShortCommit(commit string) string {
	trimmed := strings.TrimSpace(commit)
	if len(trimmed) > ShortCommitLen {
		return trimmed[:ShortCommitLen]
	}
	return trimmed
}
