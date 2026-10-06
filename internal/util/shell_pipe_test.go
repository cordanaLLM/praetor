// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "testing"

// TestShellPipes covers a pipe (positive), commands without one (negative) and the edges where
// a | is text or another operator (boundary).
func TestShellPipes(t *testing.T) {
	for _, tc := range []struct {
		command string
		pipes   bool
	}{
		{"cat a | base64 -d > b", true},
		{"echo 'x' | sha256sum -c - && tar -xzf a", true},
		{"a|b", true},
		{"a || b | c", true},
		{"cat a > b && base64 -d b > c", false},
		{"a || b", false},
		{"echo 'a|b'", false},
		{`echo "a|b" "c\"|d"`, false},
		{"", false},
		{"|", true},
	} {
		if got := ShellPipes(tc.command); got != tc.pipes {
			t.Errorf("ShellPipes(%q) = %v; want %v", tc.command, got, tc.pipes)
		}
	}
}
