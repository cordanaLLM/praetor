package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// unsafeLines returns the lines of every HISS-09 finding in rep.
func unsafeLines(rep *ScanReport) []int {
	var lines []int
	for _, v := range rep.Violations {
		if v.RuleID == "HISS-09" {
			lines = append(lines, v.LineNumber)
		}
	}
	return lines
}

// assertUnsafeLines fails unless the HISS-09 findings sit exactly on want.
func assertUnsafeLines(t *testing.T, rep *ScanReport, want ...int) {
	t.Helper()
	got := unsafeLines(rep)
	if len(got) != len(want) {
		t.Fatalf("HISS-09 lines = %v, want %v: %+v", got, want, rep.Violations)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("HISS-09 lines = %v, want %v", got, want)
		}
	}
}

// TestScanGoUnsafePositiveImportNames covers the evasions the spelling match allowed: a
// renamed import and a dot import reach package unsafe without the identifier "unsafe"
// appearing at the use.
func TestScanGoUnsafePositiveImportNames(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []int
	}{
		{"aliased selector", "package p\n\nimport u \"unsafe\"\n\nfunc f(b []byte) *byte {\n\treturn (*byte)(u.Pointer(&b[0]))\n}\n", []int{6}},
		{"dot-import conversion", "package p\n\nimport . \"unsafe\"\n\nfunc f(b []byte) *byte {\n\treturn (*byte)(Pointer(&b[0]))\n}\n", []int{6}},
		{"dot-import parenthesised", "package p\n\nimport . \"unsafe\"\n\nfunc f(p *byte) *byte {\n\treturn (*byte)((Add)(Pointer(p), 1))\n}\n", []int{6, 6}},
		{"aliased beside plain", "package p\n\nimport (\n\tu \"unsafe\"\n)\n\nfunc f(n int) []byte {\n\tvar x byte\n\treturn u.Slice(&x, n)\n}\n", []int{9}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUnsafeLines(t, scanFixtureFile(t, "input.go", tc.src), tc.want...)
		})
	}
}

// TestScanGoUnsafeNegativeResolvedElsewhere keeps the rule on what an identifier resolves
// to: another package imported under the name unsafe, a local that shadows the alias, a
// blank import, and proven uses are all clean.
func TestScanGoUnsafeNegativeResolvedElsewhere(t *testing.T) {
	cases := []struct {
		name string
		src  string
	}{
		{"other package named unsafe", "package p\n\nimport unsafe \"example.com/shim\"\n\nfunc f(v int) int {\n\treturn unsafe.Pointer(v)\n}\n"},
		{"local shadows alias", "package p\n\nimport u \"unsafe\"\n\ntype shim struct{ Pointer int }\n\nfunc f() int {\n\tvar u shim\n\treturn u.Pointer\n}\n"},
		{"blank import binds nothing", "package p\n\nimport _ \"unsafe\"\n\nfunc Pointer(v int) int { return v }\n\nfunc f() int {\n\treturn Pointer(1)\n}\n"},
		{"dot import non-export call", "package p\n\nimport . \"unsafe\"\n\nvar _ Pointer\n\nfunc g() int { return 1 }\n\nfunc f() int {\n\treturn g()\n}\n"},
		{"dot import local shadow", "package p\n\nimport . \"unsafe\"\n\nvar _ Pointer\n\nfunc f() int {\n\tAdd := func(a, b int) int { return a + b }\n\treturn Add(1, 2)\n}\n"},
		{"proven alias", "package p\n\nimport u \"unsafe\"\n\nfunc f(b []byte) *byte {\n\t// SAFETY: every caller checks len(b) > 0.\n\treturn (*byte)(u.Pointer(&b[0]))\n}\n"},
		{"proven dot import", "package p\n\nimport . \"unsafe\"\n\nfunc f(b []byte) *byte {\n\t// SAFETY: every caller checks len(b) > 0.\n\treturn (*byte)(Pointer(&b[0]))\n}\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertUnsafeLines(t, scanFixtureFile(t, "input.go", tc.src))
		})
	}
}

// TestScanGoUnsafeBoundarySafetyProof pins where a comment stops being a proof: the
// marker must open a line and carry a justification, on that line or the next ones in
// the same group. A bare marker, a placeholder, punctuation and prose naming the marker
// were each accepted by the substring match.
func TestScanGoUnsafeBoundarySafetyProof(t *testing.T) {
	cases := []struct {
		name    string
		comment string
		want    []int
	}{
		{"bare marker", "\t// SAFETY:\n", []int{7}},
		{"placeholder", "\t// SAFETY: TODO: prove the bound.\n", []int{7}},
		{"lowercase placeholder", "\t// SAFETY: fixme\n", []int{7}},
		{"punctuation only", "\t// SAFETY: ---\n", []int{7}},
		{"marker mid-sentence", "\t// This is not a SAFETY: proof.\n", []int{7}},
		{"marker without colon", "\t// SAFETY the bound holds.\n", []int{7}},
		{"single word", "\t// SAFETY: bounded\n", nil},
		{"justification on next line", "\t// SAFETY:\n\t// the caller checks len(b) > 0.\n", nil},
		{"real proof after placeholder", "\t// SAFETY: TODO\n\t// SAFETY: the caller checks len(b) > 0.\n", nil},
		{"block comment", "\t/* SAFETY: the caller checks len(b) > 0. */\n", nil},
		{"indented marker in block", "\t/*\n\t   SAFETY: the caller checks len(b) > 0.\n\t*/\n", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\n\nimport \"unsafe\"\n\nfunc f(b []byte) *byte {\n" + tc.comment +
				"\treturn (*byte)(unsafe.Pointer(&b[0]))\n}\n"
			assertUnsafeLines(t, scanFixtureFile(t, "input.go", src), tc.want...)
		})
	}
}

// TestFileImportsBindsResolvedNames covers the import table the rules resolve through.
func TestFileImportsBindsResolvedNames(t *testing.T) {
	src := "package p\n\nimport (\n\t\"net/http\"\n\tu \"unsafe\"\n\t. \"strings\"\n\t_ \"embed\"\n\texec \"os/exec\"\n)\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	im := FileImports(file)
	for _, bound := range [][2]string{{"http", "net/http"}, {"u", "unsafe"}, {"exec", "os/exec"}} {
		if !im.Binds(bound[0], bound[1]) {
			t.Errorf("%s must bind %s: %+v", bound[0], bound[1], im.byName)
		}
	}
	for _, unbound := range [][2]string{{"unsafe", "unsafe"}, {"_", "embed"}, {"embed", "embed"}, {"http", "unsafe"}} {
		if im.Binds(unbound[0], unbound[1]) {
			t.Errorf("%s must not bind %s: %+v", unbound[0], unbound[1], im.byName)
		}
	}
	if !im.DotImports("strings") || im.DotImports("unsafe") {
		t.Errorf("dot imports = %+v, want only strings", im.dot)
	}
}

// TestFileImportsBoundaryMalformedSpecs keeps a partial AST from binding anything: a nil
// file, a nil spec, a path that does not unquote and an empty path are all skipped.
func TestFileImportsBoundaryMalformedSpecs(t *testing.T) {
	if im := FileImports(nil); len(im.byName) != 0 || len(im.dot) != 0 {
		t.Fatalf("nil file bound names: %+v", im)
	}
	file := &ast.File{Imports: []*ast.ImportSpec{
		nil,
		{Path: nil},
		{Path: &ast.BasicLit{Kind: token.STRING, Value: `"unterminated`}},
		{Path: &ast.BasicLit{Kind: token.STRING, Value: `""`}},
		{Name: ast.NewIdent("u"), Path: &ast.BasicLit{Kind: token.STRING, Value: `"unsafe"`}},
	}}
	im := FileImports(file)
	if len(im.byName) != 1 || !im.Binds("u", "unsafe") {
		t.Fatalf("malformed specs must be skipped and the valid one kept: %+v", im.byName)
	}
}

// TestIsSafetyProofBoundaryText checks the proof recogniser on comment text directly,
// including the multi-line and empty cases the scan tests reach only through a file.
func TestIsSafetyProofBoundaryText(t *testing.T) {
	cases := []struct {
		text string
		want bool
	}{
		{"", false},
		{"SAFETY:", false},
		{"SAFETY:\n", false},
		{"SAFETY:x", true},
		{"  SAFETY: 1", true},
		{"SAFETY: TBD\n", false},
		{"SAFETY: XXX later\nmore", false},
		{"intro\nSAFETY:\nthe bound holds\n", true},
		{"see the SAFETY: section", false},
		{strings.Repeat("prose\n", 64) + "SAFETY: bounded\n", true},
	}
	for _, tc := range cases {
		if got := isSafetyProof(tc.text); got != tc.want {
			t.Errorf("isSafetyProof(%q) = %v, want %v", tc.text, got, tc.want)
		}
	}
}
