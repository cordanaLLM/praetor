// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "testing"

func TestShortCommit(t *testing.T) {
	cases := []struct {
		name, in, want string
	}{
		{"positive: full sha is cut", "8d29c5e84a9d4f0b1c2d3e4f5a6b7c8d9e0f1a2b", "8d29c5e84a9d"},
		{"positive: git output newline is trimmed", "8d29c5e84a9d4f0b\n", "8d29c5e84a9d"},
		{"negative: empty stays empty", "", ""},
		{"negative: whitespace only is empty", " \n", ""},
		{"boundary: exactly the length is kept", "8d29c5e84a9d", "8d29c5e84a9d"},
		{"boundary: one over the length is cut", "8d29c5e84a9d4", "8d29c5e84a9d"},
		{"boundary: shorter id is kept", "8d29c5e", "8d29c5e"},
	}
	for _, tc := range cases {
		if got := ShortCommit(tc.in); got != tc.want {
			t.Errorf("%s: ShortCommit(%q) = %q, want %q", tc.name, tc.in, got, tc.want)
		}
	}
}
