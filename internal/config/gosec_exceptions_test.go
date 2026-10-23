// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package config

import (
	"strings"
	"testing"
	"time"
)

func TestGosecExceptions_ManifestValidation(t *testing.T) {
	today := time.Now().UTC()
	good := "version: 1\nexceptions:\n  - rule: gosec\n    path: src/main.go\n    gosec_rules: [G204, G304]\n    reason: Pending redesign\n    category: C\n    entered: " + today.Format(ExceptionDateLayout) + "\n    expires: " + today.AddDate(0, 0, 10).Format(ExceptionDateLayout) + "\n"
	cases := []struct {
		name, from, to string
		valid          bool
	}{
		{"live", "", "", true},
		{"bad id", "G204", "G20", false},
		{"lowercase", "G204", "g204", false},
		{"non digit", "G204", "G2x4", false},
		{"duplicate id", "G304", "G204", false},
		{"no ids", "[G204, G304]", "[]", false},
		{"no path", "    path: src/main.go\n", "", false},
		{"glob", "path: src/main.go", "glob: src/*.go", false},
		{"no category", "    category: C\n", "", false},

		{"no entered", "    entered: " + today.Format(ExceptionDateLayout) + "\n", "", false},
		{"category cap", "category: C", "category: B", false},
		{"wrong owner", "rule: gosec", "rule: HISS-19", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			text := good
			if tc.from != "" {
				text = strings.Replace(text, tc.from, tc.to, 1)
			}
			_, err := LoadManifest(writeManifest(t, text))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}
