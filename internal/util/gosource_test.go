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

func TestIsGoTestSurfaceClaimsTestFilesAndTestdataAtAnyExtension(t *testing.T) {
	cases := []struct {
		name string
		path string
		want bool
	}{
		// Positive: test files and testdata members, Go or not, at the root and nested.
		{"test file", "internal/util/util_test.go", true},
		{"root testdata Go", "testdata/fixture.go", true},
		{"nested testdata asset", "tools/markdownlint/testdata/verify.mjs", true},
		{"host separators", filepath.Join("a", "testdata", "b.json"), true},
		{"bare suffix", "_test.go", true},
		// Negative: build source and non-Go assets outside testdata.
		{"source", "internal/util/util.go", false},
		{"asset", "tools/markdownlint/package.json", false},
		{"module file", "go.mod", false},
		{"empty", "", false},
		// Boundary: names that only resemble the test surface.
		{"test helper", "internal/x/x_test_helper.go", false},
		{"testdata file name", "internal/x/testdata.go", false},
		{"testdata directory without members", "testdata", false},
		{"testdata prefix directory", "internal/mytestdata/m.mjs", false},
		{"non-Go test suffix", "tools/markdownlint/verify_test.mjs", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := util.IsGoTestSurface(tc.path); got != tc.want {
				t.Errorf("IsGoTestSurface(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// The HISS alias resolver and the needs analyser used to carry one private copy each of this
// check; both now call the shared one, so its edges are pinned here once.
func TestIsGoMajorVersionElement(t *testing.T) {
	for s, want := range map[string]bool{
		// Positive: major-version elements.
		"v2": true, "v10": true, "v0": true,
		// Negative: anything else.
		"": false, "x2": false, "v2a": false, "V2": false, "2": false, "v-2": false,
		// Boundary: the bare prefix and a single digit.
		"v": false, "v9": true,
	} {
		if got := util.IsGoMajorVersionElement(s); got != want {
			t.Errorf("IsGoMajorVersionElement(%q) = %v, want %v", s, got, want)
		}
	}
}
