// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
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

// TestGoImportPathsReadsEveryImportForm covers the path projection the needs scan and
// replacement plan and the DevContainer bootstrap closure share: every import form is read
// and unquoted, raw strings included, and a file without imports or an absent file yields
// none.
func TestGoImportPathsReadsEveryImportForm(t *testing.T) {
	source := "package x\n\nimport (\n\t\"fmt\"\n\tal \"example.com/m/a\"\n\t_ \"example.com/m/b\"\n\t. \"example.com/m/c\"\n\t`example.com/m/raw`\n)\n"
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", source, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: plain, renamed, blank, dot and raw-string imports, in source order.
	want := []string{"fmt", "example.com/m/a", "example.com/m/b", "example.com/m/c", "example.com/m/raw"}
	if got := util.GoImportPaths(file); !slices.Equal(got, want) {
		t.Fatalf("GoImportPaths = %q, want %q", got, want)
	}
	// Negative: a path that does not unquote, or unquotes to nothing, comes from a partial
	// AST and contributes no path.
	file.Imports[0].Path.Value = `"unterminated`
	file.Imports[1].Path.Value = `""`
	if got := util.GoImportPaths(file); !slices.Equal(got, want[2:]) {
		t.Fatalf("GoImportPaths with broken specs = %q, want %q", got, want[2:])
	}
	// Boundary: no file, and a file without imports.
	if got := util.GoImportPaths(nil); len(got) != 0 {
		t.Fatalf("GoImportPaths(nil) = %q", got)
	}
	bare, err := parser.ParseFile(token.NewFileSet(), "y.go", "package y\n", parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	if got := util.GoImportPaths(bare); len(got) != 0 {
		t.Fatalf("GoImportPaths(no imports) = %q", got)
	}
}

// TestGoImportSpecsKeepsNamesAndBoundsTheScan covers the one import-spec reader the HISS
// import table, GoImportPaths, the docs vocabulary tree and the caveman MCP reader share:
// each spec keeps the name it is written with, a partial AST contributes nothing, and the
// scan stops at 4096 specs.
func TestGoImportSpecsKeepsNamesAndBoundsTheScan(t *testing.T) {
	source := "package x\n\nimport (\n\t\"fmt\"\n\tal \"example.com/m/a\"\n\t_ \"example.com/m/b\"\n\t. \"example.com/m/c\"\n)\n"
	file, err := parser.ParseFile(token.NewFileSet(), "x.go", source, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	// Positive: an unrenamed import has no name; alias, blank and dot names are kept.
	want := []util.GoImportSpec{
		{Name: "", Path: "fmt"},
		{Name: "al", Path: "example.com/m/a"},
		{Name: "_", Path: "example.com/m/b"},
		{Name: ".", Path: "example.com/m/c"},
	}
	if got := util.GoImportSpecs(file); !slices.Equal(got, want) {
		t.Fatalf("GoImportSpecs = %+v, want %+v", got, want)
	}
	// Negative: a nil spec, a spec without a path, and a path that does not unquote or
	// unquotes to nothing are skipped, even when the spec carries a name.
	partial := &ast.File{Imports: []*ast.ImportSpec{
		nil,
		{Name: ast.NewIdent("gone"), Path: nil},
		{Name: ast.NewIdent("bad"), Path: &ast.BasicLit{Kind: token.STRING, Value: `"unterminated`}},
		{Path: &ast.BasicLit{Kind: token.STRING, Value: `""`}},
		{Name: ast.NewIdent("u"), Path: &ast.BasicLit{Kind: token.STRING, Value: `"unsafe"`}},
	}}
	if got := util.GoImportSpecs(partial); !slices.Equal(got, []util.GoImportSpec{{Name: "u", Path: "unsafe"}}) {
		t.Fatalf("GoImportSpecs(partial AST) = %+v, want only u unsafe", got)
	}
	// Boundary: no file, then exactly 4096 specs and one more than that.
	if got := util.GoImportSpecs(nil); len(got) != 0 {
		t.Fatalf("GoImportSpecs(nil) = %+v", got)
	}
	for _, tc := range []struct{ specs, want int }{{4096, 4096}, {4097, 4096}} {
		wide := &ast.File{Imports: make([]*ast.ImportSpec, tc.specs)}
		for i := range wide.Imports {
			wide.Imports[i] = &ast.ImportSpec{Path: &ast.BasicLit{Kind: token.STRING, Value: `"example.com/m/p"`}}
		}
		if got := util.GoImportSpecs(wide); len(got) != tc.want {
			t.Fatalf("GoImportSpecs(%d specs) read %d, want %d", tc.specs, len(got), tc.want)
		}
		if got := util.GoImportPaths(wide); len(got) != tc.want {
			t.Fatalf("GoImportPaths(%d specs) read %d, want %d", tc.specs, len(got), tc.want)
		}
	}
}

// TestModuleImportDirDrawsTheModuleBoundary covers the one module-boundary rule the needs
// package, the docs vocabulary tree and the DevContainer bootstrap closure share.
func TestModuleImportDirDrawsTheModuleBoundary(t *testing.T) {
	const module = "github.com/acme/foo"
	cases := []struct {
		name, importPath, module, wantDir string
		wantInside                        bool
	}{
		// Positive: the module root and a nested package.
		{"module root", module, module, ".", true},
		{"nested package", module + "/internal/x", module, "internal/x", true},
		// Negative: a sibling sharing a textual prefix, the standard library, a parent path,
		// and an empty module, which owns nothing.
		{"sibling prefix", "github.com/acme/foo-plugins/auth", module, "", false},
		{"standard library", "strings", module, "", false},
		{"parent path", "github.com/acme", module, "", false},
		{"empty module", "go.uber.org/zap", "", "", false},
		// Boundary: a trailing separator names no package.
		{"trailing separator", module + "/", module, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, inside := util.ModuleImportDir(tc.importPath, tc.module)
			if dir != tc.wantDir || inside != tc.wantInside {
				t.Errorf("ModuleImportDir(%q, %q) = %q, %v, want %q, %v", tc.importPath, tc.module, dir, inside, tc.wantDir, tc.wantInside)
			}
		})
	}
}
