// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package contextopt

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestOverlaps(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	tests := []struct {
		name          string
		first, second string
		want          bool
	}{
		{name: "equal paths", first: parent, second: parent, want: true},
		{name: "second inside first", first: parent, second: filepath.Join(parent, "child", "leaf"), want: true},
		{name: "first inside second", first: filepath.Join(parent, "child"), second: parent, want: true},
		{name: "unclean spelling of the same path", first: parent + string(filepath.Separator) + ".", second: parent, want: true},
		{name: "sibling", first: parent, second: filepath.Join(root, "other"), want: false},
		{name: "name prefix is not containment", first: parent, second: parent + "2", want: false},
		{name: "dot-dot prefixed name inside parent", first: parent, second: filepath.Join(parent, "..child"), want: true},
		{name: "absolute against relative is incomparable", first: parent, second: "parent", want: false},
	}
	if runtime.GOOS == "windows" {
		tests = append(tests, struct {
			name          string
			first, second string
			want          bool
		}{name: "different volumes are incomparable", first: `C:\cfg\settings.json`, second: `D:\cfg`, want: false})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := Overlaps(test.first, test.second); got != test.want {
				t.Fatalf("Overlaps(%q, %q) = %v, want %v", test.first, test.second, got, test.want)
			}
			if got := Overlaps(test.second, test.first); got != test.want {
				t.Fatalf("Overlaps(%q, %q) = %v, want %v (not symmetric)", test.second, test.first, got, test.want)
			}
		})
	}
}
