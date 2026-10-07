// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package gomanifest

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: the module line, single-line and block requires, and single-line and block replaces
// are each read once, in file order, with comments and the indirect marker dropped.
func TestParseManifest_Positive_ReadsModuleRequiresAndReplaces(t *testing.T) {
	manifest := strings.Join([]string{
		"\xef\xbb\xbfmodule example.com/app // the module",
		"",
		"go 1.27",
		"",
		"require example.com/a v1.0.0",
		"require (",
		"\t// a comment line",
		"\texample.com/b v2.0.0 // indirect",
		"\t\"example.com/c\" v0.1.0",
		")",
		"replace example.com/a => ../a",
		"replace (",
		"\texample.com/b v2.0.0 => example.com/fork v2.0.1",
		")",
		"",
	}, "\n")
	got := ParseManifest([]byte(manifest))
	want := Manifest{
		Module: "example.com/app",
		Requires: []Requirement{
			{Path: "example.com/a", Version: "v1.0.0"},
			{Path: "example.com/b", Version: "v2.0.0"},
			{Path: "example.com/c", Version: "v0.1.0"},
		},
		Replaces: []ReplaceDirective{
			{OldPath: "example.com/a", NewPath: "../a"},
			{OldPath: "example.com/b", OldVersion: "v2.0.0", NewPath: "example.com/fork", NewVersion: "v2.0.1"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseManifest = %+v, want %+v", got, want)
	}
}

// Negative: a manifest without directives, a commented-out module line and malformed directives
// yield nothing, and a replace arrow inside a require block is not a replace.
func TestParseManifest_Negative_MalformedAndCommentedDirectives(t *testing.T) {
	manifest := strings.Join([]string{
		"// module example.com/commented",
		"require example.com/noversion",
		"replace example.com/x",
	}, "\n")
	got := ParseManifest([]byte(manifest))
	if got.Module != "" || len(got.Requires) != 0 || len(got.Replaces) != 0 {
		t.Fatalf("malformed directives must yield nothing, got %+v", got)
	}
	inRequire := ParseManifest([]byte("require (\n\texample.com/a => example.com/b\n)\n"))
	if len(inRequire.Replaces) != 0 {
		t.Fatalf("a replace arrow inside a require block is not a replace, got %+v", inRequire.Replaces)
	}
	if empty := ParseManifest(nil); !reflect.DeepEqual(empty, Manifest{}) {
		t.Fatalf("an empty manifest yields the zero Manifest, got %+v", empty)
	}
}

// Boundary: the first module line wins, a second is not read as a requirement, and a line past
// maxManifestLines is not read while the last line inside the bound is.
func TestParseManifest_Boundary_FirstModuleAndLineBound(t *testing.T) {
	got := ParseManifest([]byte("module example.com/first\nmodule example.com/second\n"))
	if got.Module != "example.com/first" || len(got.Requires) != 0 {
		t.Fatalf("the first module line wins, got %+v", got)
	}
	last := "require example.com/last v1.0.0\n"
	inside := ParseManifest([]byte("module example.com/app" + strings.Repeat("\n", maxManifestLines-1) + last))
	if len(inside.Requires) != 1 {
		t.Fatalf("a requirement on the last line inside the bound must be read, got %+v", inside.Requires)
	}
	past := ParseManifest([]byte("module example.com/app" + strings.Repeat("\n", maxManifestLines) + last))
	if len(past.Requires) != 0 {
		t.Fatalf("a requirement past the line bound must not be read, got %+v", past.Requires)
	}
}
