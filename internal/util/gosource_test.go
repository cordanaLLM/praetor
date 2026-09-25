// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

func TestIsGoNonTestSourceDrawsGoTestBoundary(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// Positive: ordinary package source at the root and nested.
		{"root source", "main.go", true},
		{"nested source", "internal/util/util.go", true},
		// Negative: Go's test surface and non-Go files.
		{"test file", "internal/util/util_test.go", false},
		{"root test file", "main_test.go", false},
		{"root testdata", "testdata/fixture.go", false},
		{"nested testdata", "internal/hiss/testdata/go/deep/fixture.go", false},
		{"not Go", "go.mod", false},
		{"Go-like suffix", "notes.gox", false},
		// Boundary: names that only resemble the test surface stay source.
		{"test helper", "internal/x/x_test_helper.go", true},
		{"testdata file name", "internal/x/testdata.go", true},
		{"testdata prefix directory", "internal/mytestdata/m.go", true},
		{"testdata suffix directory", "internal/testdatas/m.go", true},
		{"bare suffix", "_test.go", false},
		{"empty", "", false},
		{"host separators", filepath.Join("a", "testdata", "b.go"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.IsGoNonTestSource(tc.path); got != tc.want {
				t.Errorf("IsGoNonTestSource(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}
