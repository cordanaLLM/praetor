// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package editor

import (
	"slices"
	"testing"
)

// detectFixtureLanguages writes each rel path under a fresh root and returns the languages the
// workspace scan reports for it.
func detectFixtureLanguages(t *testing.T, rels ...string) []string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range rels {
		writeScratchFixture(t, root, rel)
	}
	languages, err := DetectWorkspaceLanguages(root)
	if err != nil {
		t.Fatal(err)
	}
	return languages
}

// Positive: a meson or CMake build is C and C++, as before the native analyzer learned Zig; a Zig
// build beside one adds zig.
func TestDetectWorkspaceLanguages_Positive_NativeBuildLanguages(t *testing.T) {
	for _, tc := range []struct {
		rels []string
		want []string
	}{
		{[]string{"CMakeLists.txt"}, []string{"c", "cpp"}},
		{[]string{"meson.build"}, []string{"c", "cpp"}},
		{[]string{"CMakeLists.txt", "build.zig"}, []string{"c", "cpp", "zig"}},
	} {
		if got := detectFixtureLanguages(t, tc.rels...); !slices.Equal(got, tc.want) {
			t.Errorf("%v: languages = %v, want %v", tc.rels, got, tc.want)
		}
	}
}

// Negative: a pure-Zig repository is Zig only. The native analyzer detects build.zig, and its id
// must not stand for C/C++ tooling there; nor do the C sources and headers Zig fetched into
// zig-pkg/, installed into zig-out/ or cached in .zig-cache/.
func TestDetectWorkspaceLanguages_Negative_ZigBuildIsNotCFamily(t *testing.T) {
	for _, rels := range [][]string{
		{"build.zig", "src/main.zig"},
		{"build.zig.zon", "src/main.zig"},
		{"build.zig", "src/main.zig", "zig-pkg/dep-0.0.1-h/src/dep.c", "zig-out/include/dep.h", ".zig-cache/o/h/cimport.h"},
	} {
		if got := detectFixtureLanguages(t, rels...); !slices.Equal(got, []string{"zig"}) {
			t.Errorf("%v: languages = %v, want [zig]", rels, got)
		}
	}
}

// Boundary: a caller that names the native id itself still means C and C++, and a first-party
// directory whose name only resembles a toolchain tree is scanned.
func TestDetectWorkspaceLanguages_Boundary_ExplicitNativeAndLookalikeTrees(t *testing.T) {
	languages, err := normalizeLanguages([]string{"native"})
	if err != nil || !slices.Equal(languages, []string{"c", "cpp"}) {
		t.Errorf("normalizeLanguages(native) = %v, %v; want [c cpp]", languages, err)
	}
	if got, want := detectFixtureLanguages(t, "build.zig", "zig-pkg-tools/shim.c"), []string{"c", "zig"}; !slices.Equal(got, want) {
		t.Errorf("lookalike tree: languages = %v, want %v", got, want)
	}
}
