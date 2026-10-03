// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"testing"
)

// boundCalls parses src and reports, for each bare call to g in source order, whether a
// binding of g is in scope at that call.
func boundCalls(t *testing.T, src string) []bool {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "scope.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var scope goScope
	var got []bool
	scope.inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if ident, isIdent := call.Fun.(*ast.Ident); isIdent && ident.Name == "g" {
				got = append(got, scope.binds("g"))
			}
		}
		return true
	})
	return got
}

func assertBoundCalls(t *testing.T, cases map[string]struct {
	src  string
	want []bool
},
) {
	t.Helper()
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := boundCalls(t, "package p\n\n"+tc.src+"\n"); !slices.Equal(got, tc.want) {
				t.Fatalf("bound at each call to g = %v, want %v\n%s", got, tc.want, tc.src)
			}
		})
	}
}

// Positive: every binding Go puts in scope at a call is seen there, signature, literal and
// block-local alike.
func TestGoScope_Positive_BindingsInScopeAtTheCall(t *testing.T) {
	assertBoundCalls(t, map[string]struct {
		src  string
		want []bool
	}{
		"parameter":           {"func F(g func()) { g() }", []bool{true}},
		"named result":        {"func F() (g func()) { g(); return }", []bool{true}},
		"receiver":            {"type T func()\n\nfunc (g T) F() { g() }", []bool{true}},
		"type parameter":      {"func F[g any](x g) { _ = g(x) }", []bool{true}},
		"short variable":      {"func F() { g := func() {}; g() }", []bool{true}},
		"var":                 {"func F() { var g func(); g() }", []bool{true}},
		"const":               {"func F() { const g = 1; _ = g() }", []bool{true}},
		"local type":          {"func F(x int) { type g int; _ = g(x) }", []bool{true}},
		"enclosing block":     {"func F(b bool) { g := func() {}; if b { { g() } } }", []bool{true}},
		"literal parameter":   {"func F() { _ = func(g func()) { g() } }", []bool{true}},
		"package literal":     {"var h = func(g func()) { g() }", []bool{true}},
		"range variable":      {"func F(xs []func()) { for _, g := range xs { g() } }", []bool{true}},
		"type switch binding": {"func F(x any) { switch g := x.(type) { case func(): g() } }", []bool{true}},
		"comm clause":         {"func F(ch chan func()) { select { case g := <-ch: g() } }", []bool{true}},
	})
}

// Negative: a binding that is not in scope at the call hides nothing. A declaration counts
// from its end, so its own initialiser still reaches the package function; a block, a clause,
// a statement header and a literal each end the scope of what they declare; a label and a
// package-level declaration bind nothing a call could be shadowed by.
func TestGoScope_Negative_BindingsOutOfScopeAtTheCall(t *testing.T) {
	assertBoundCalls(t, map[string]struct {
		src  string
		want []bool
	}{
		"own initialiser":     {"func F() { g := g(); _ = g }", []bool{false}},
		"own var initialiser": {"func F() { var g = g(); _ = g }", []bool{false}},
		"declared after":      {"func F() { g(); g := 1; _ = g }", []bool{false}},
		"sibling block":       {"func F(b bool) { if b { g := func() {}; g() }; g() }", []bool{true, false}},
		"sibling case":        {"func F(x int) { switch x { case 1: g := func() {}; g(); case 2: g() } }", []bool{true, false}},
		"sibling comm":        {"func F(ch chan func()) { select { case g := <-ch: g(); default: g() } }", []bool{true, false}},
		"if header":           {"func F() { if g := h; g() { g() } else { g() }; g() }", []bool{true, true, true, false}},
		"for header":          {"func F() { for g := h; g() > 0; g() { g() }; g() }", []bool{true, true, true, false}},
		"range expression":    {"func F() { for _, g := range g() { g() }; g() }", []bool{false, true, false}},
		"type switch":         {"func F(x any) { switch g := x.(type) { case func(): g() }; g() }", []bool{true, false}},
		"literal parameter":   {"func F() { _ = func(g func()) { g() }; g() }", []bool{true, false}},
		"literal local":       {"func F() { h := func() { g := func() {}; g() }; h(); g() }", []bool{true, false}},
		"label":               {"func F() {\ng:\n\tfor {\n\t\tbreak g\n\t}\n\tg()\n}", []bool{false}},
		"package declaration": {"var g = func() {}\n\nfunc F() { g() }", []bool{false}},
		"blank identifier":    {"func F() { _, x := 1, 2; _ = x; g() }", []bool{false}},
	})
}

// Boundary: bindings of one name nest and are counted. Closing an inner block releases only
// its own binding, so the parameter it hid is in scope again after it, and the next function
// starts with nothing bound.
func TestGoScope_Boundary_NestedBindingsAndFunctionEdges(t *testing.T) {
	assertBoundCalls(t, map[string]struct {
		src  string
		want []bool
	}{
		"inner rebinding":   {"func F(g func()) { { g := g; g() }; g() }", []bool{true, true}},
		"next function":     {"func F(g func()) { g() }\n\nfunc G() { g() }", []bool{true, false}},
		"redeclared in one": {"func F() { g, a := h(); g, b := h(); _, _ = a, b; g() }; func G() { g() }", []bool{true, false}},
		"literal in header": {"func F() { if f := func(g func()) { g() }; f != nil { g() } }", []bool{true, false}},
	})
	if boundCalls(t, "package p\n") != nil {
		t.Fatal("a file without calls must report none")
	}
}

// Positive, negative and boundary for bindsAt, the position query AbortsHTTPResponse uses: it
// answers for the scope in force at one position of fn, and a nil fn binds nothing.
func TestGoScope_BindsAt(t *testing.T) {
	src := "package p\n\nfunc F(b bool) {\n\tif b {\n\t\tg := 1\n\t\t_ = g\n\t}\n\tuse(g)\n\tg := 2\n\tuse(g)\n}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "at.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	fn, ok := file.Decls[0].(*ast.FuncDecl)
	if !ok {
		t.Fatal("fixture holds no function")
	}
	var uses []token.Pos
	ast.Inspect(fn, func(n ast.Node) bool {
		if call, isCall := n.(*ast.CallExpr); isCall {
			uses = append(uses, call.Pos())
		}
		return true
	})
	if len(uses) != 2 {
		t.Fatalf("fixture must hold two calls, got %d", len(uses))
	}
	if bindsAt(fn, "g", uses[0]) {
		t.Error("the local of the if block is out of scope at the first use, and the later one is not declared yet")
	}
	if !bindsAt(fn, "g", uses[1]) {
		t.Error("the local declared before the second use is in scope there")
	}
	if !bindsAt(fn, "b", uses[0]) || bindsAt(fn, "use", uses[1]) {
		t.Error("the parameter is in scope everywhere in the body, and nothing binds use")
	}
	if bindsAt(nil, "g", uses[1]) {
		t.Error("a nil function binds nothing")
	}
}
