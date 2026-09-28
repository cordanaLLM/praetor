// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: the trees Zig writes inside a checkout (fetched packages, local cache, default install
// prefix) are toolchain trees. Negative: a first-party directory whose name only resembles one is
// not. Boundary: the match is exact and case-sensitive, and a path is no base name.
func TestIsToolchainTreeDir(t *testing.T) {
	for _, tc := range []struct {
		name string
		want bool
	}{
		{"zig-pkg", true},
		{"zig-out", true},
		{".zig-cache", true},
		{"zig", false},
		{"src", false},
		{"zig-pkgs", false},
		{"zig-pkg-tools", false},
		{"Zig-Pkg", false},
		{"ZIG-OUT", false},
		{"", false},
		{"zig-pkg/", false},
		{"app/zig-pkg", false},
	} {
		if got := util.IsToolchainTreeDir(tc.name); got != tc.want {
			t.Errorf("IsToolchainTreeDir(%q) = %v, want %v", tc.name, got, tc.want)
		}
	}
}
