// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestUTF8TailBounds covers empty, exact, truncated, and sub-rune byte budgets.
func TestUTF8TailBounds(t *testing.T) {
	for _, tc := range []struct {
		text  string
		limit int
		want  string
	}{
		{"", 2, ""}, {"abc", -1, ""}, {"abc", 0, ""}, {"abc", 3, "abc"},
		{"🙂🙂", 1, ""}, {"🙂🙂", 6, "..."}, {"🙂🙂", 7, "...🙂"}, {"🙂🙂", 8, "🙂🙂"},
		{"abcdef", 5, "...ef"}, {"éé", 1, ""}, {"éé", 2, "é"}, {"éé", 3, "..."},
		{strings.Repeat("é", 40000), 2048, "..." + strings.Repeat("é", 1022)},
	} {
		got := UTF8Tail(tc.text, tc.limit)
		if got != tc.want || !utf8.ValidString(got) || len(got) > max(0, tc.limit) {
			t.Errorf("tail bytes=%d limit=%d: got bytes=%d valid=%v; want bytes=%d", len(tc.text), tc.limit, len(got), utf8.ValidString(got), len(tc.want))
		}
	}
}
