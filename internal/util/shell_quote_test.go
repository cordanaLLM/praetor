// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import "testing"

// TestQuotePOSIXShellArg covers plain text, shell syntax and the empty argument boundary.
func TestQuotePOSIXShellArg(t *testing.T) {
	for _, tc := range []struct{ arg, want string }{
		{"plain", "'plain'"},
		{"with spaces", "'with spaces'"},
		{"O'Brien", "'O'\\''Brien'"},
		{"$(touch x)", "'$(touch x)'"},
		{"`touch x`", "'`touch x`'"},
		{"", "''"},
	} {
		t.Run(tc.arg, func(t *testing.T) {
			if got := QuotePOSIXShellArg(tc.arg); got != tc.want {
				t.Errorf("QuotePOSIXShellArg(%q) = %q; want %q", tc.arg, got, tc.want)
			}
		})
	}
}
