// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Positive: table and array-of-tables headers, padded or commented, normalize to their names.
func TestTOMLTableNamePositive(t *testing.T) {
	for header, want := range map[string]string{
		"[extend]":                 "extend",
		"[ extend ]":               "extend",
		"[[annotations]]":          "[annotations]",
		"  [[ annotations ]]  # x": "[annotations]",
		`["quoted"]`:               "quoted",
	} {
		if got := util.TOMLTableName(header); got != want {
			t.Errorf("TOMLTableName(%q) = %q, want %q", header, got, want)
		}
	}
}

// Negative: a key line or a comment is not a header of the table it mentions.
func TestTOMLTableNameNegative(t *testing.T) {
	for _, line := range []string{`path = ["[[annotations]]"]`, "# [[annotations]]"} {
		if got := util.TOMLTableName(line); got == "[annotations]" {
			t.Errorf("TOMLTableName(%q) read a header", line)
		}
	}
}

// Boundary: an empty line and an empty header yield the empty name.
func TestTOMLTableNameBoundary(t *testing.T) {
	for _, line := range []string{"", "[]", "  # only a comment"} {
		if got := util.TOMLTableName(line); got != "" {
			t.Errorf("TOMLTableName(%q) = %q, want empty", line, got)
		}
	}
}
