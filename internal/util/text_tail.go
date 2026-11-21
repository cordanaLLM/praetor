// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "unicode/utf8"

// UTF8Tail keeps a byte-bounded suffix, marking truncation with three dots when they fit.
// A valid UTF-8 input stays valid: at most UTFMax continuation bytes are discarded at the cut.
// A nonpositive limit returns empty; a limit below three omits the truncation marker.
func UTF8Tail(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(text) <= limit {
		return text
	}
	prefix := ""
	if limit >= 3 {
		prefix = "..."
	}
	start := len(text) - (limit - len(prefix))
	for skipped := 0; skipped < utf8.UTFMax && start < len(text) && !utf8.RuneStart(text[start]); skipped++ {
		start++
	}
	return prefix + text[start:]
}
