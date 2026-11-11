// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func assertNarrowScope(t *testing.T, body string, want int) *ScanReport {
	t.Helper()
	rep := scanFixtureFile(t, "scope.go", "package p\n"+body+"\n")
	if got := rep.Breakdown["HISS-05"]; got != want {
		t.Fatalf("HISS-05 = %d, want %d; findings: %+v", got, want, rep.Violations)
	}
	return rep
}

func TestNarrowScopePositive(t *testing.T) {
	for name, body := range map[string]string{
		"var if":        "func F(b bool) { var x int; if b { println(x) } }",
		"short for":     "func F() { x := 1; for i := 0; i < 2; i++ { println(x) } }",
		"else":          "func F(b bool) { x := 1; if b {} else { println(x) } }",
		"if header":     "func F() { x := true; if x {} }",
		"if init":       "func F() { x := 1; if y := x; y > 0 {} }",
		"for header":    "func F() { x := 1; for i := x; i < x; i += x {} }",
		"range":         "func F() { x := []int{1}; for _, y := range x { println(y) } }",
		"switch":        "func F() { x := 1; switch x { case 1: println(x) } }",
		"type switch":   "func F() { var x any; switch x.(type) { case int: } }",
		"select":        "func F(ch chan int) { x := 1; select { case ch <- x: default: } }",
		"plain block":   "func F() { x := 1; { println(x) } }",
		"inner write":   "func F(b bool) { var x int; if b { x = 2; println(x) } }",
		"selector name": "func F(b bool, s struct{x int}) { x := 1; println(s.x); if b { println(x) } }",
		"label":         "func F(b bool) { x := 1; x: if b { println(x); break x } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 1) })
	}
}

func TestNarrowScopeNegative(t *testing.T) {
	for name, body := range map[string]string{
		"siblings":               "func F(b bool) { x := 1; if b { println(x) }; if !b { println(x) } }",
		"after loop":             "func F() { x := 0; for i := 0; i < 2; i++ { x += i }; println(x) }",
		"outer write":            "func F(b bool) { var x int; x = 2; if b { println(x) } }",
		"address taken":          "func F(b bool) { x := 1; if b { println(&x) } }",
		"parenthesised address":  "func F(b bool) { x := 1; if b { println(&(x)) } }",
		"outer address":          "func F(b bool) { x := 1; println(&x); if b { println(x) } }",
		"outer capture":          "func F(b bool) { x := 1; _ = func() { println(x) }; if b { println(x) } }",
		"inner capture gap":      "func F(b bool) { x := 1; if b { func() { println(x) }() } }",
		"blank use":              "func F(b bool) { x := sideEffect(); _ = x; if b {} }",
		"blank declaration":      "func F(b bool) { _ = sideEffect(); if b {} }",
		"unused":                 "func F(b bool) { var x int; if b {} }",
		"parameter":              "func F(x int, b bool) { if b { println(x) } }",
		"named result":           "func F(b bool) (x int) { if b { x = 1 }; return }",
		"parameter redeclared":   "func F(x int, b bool) { x, y := 1, 2; _ = y; if b { println(x) } }",
		"short redeclared write": "func F(b bool) { x := 1; x, y := 2, 3; _ = y; if b { println(x) } }",
		"inner shadow":           "func F(b bool) { x := 1; if b { x := 2; println(x) } }",
		"range shadow":           "func F(xs []int) { x := 1; for _, x := range xs { println(x) } }",
		"field name":             "func F(b bool) { x := 1; if b { _ = func(x int) { println(x) } } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 0) })
	}
}

func TestNarrowScopeBoundary(t *testing.T) {
	for name, body := range map[string]string{
		"empty": "func F() {}", "no body": "func F()", "package local": "var x int; func F(b bool) { if b { println(x) } }",
		"nested declaration gap": "func F(b bool) { if b { x := 1; { x := x + 1; { println(x) } } } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 0) })
	}
	rep := assertNarrowScope(t, "func F(b bool) {\n x := 1\n if b {\n  { println(x) }\n }\n}", 1)
	for _, v := range rep.Violations {
		if v.RuleID == "HISS-05" && (v.LineNumber != 3 || v.Symbol != "F" ||
			!strings.Contains(v.Message, "'x' line 3") || !strings.Contains(v.Message, "if block line 4")) {
			t.Errorf("finding must name declaration and outermost block: %+v", v)
		}
	}
	broken := scanFixtureFile(t, "broken.go", "package p\nfunc F( {\n")
	if !broken.Incomplete() || broken.Skips.Unparsed != 1 {
		t.Fatalf("parse error silently accepted: %+v", broken)
	}
}

func TestNarrowScopeDeclarationGroups(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       int
	}{
		{"grouped var", "func F(b bool) { var (x int; y int); if b { println(x, y) } }", 2},
		{"multiple short", "func F(b bool) { x, y := 1, 2; if b { println(x, y) } }", 2},
		{"one outside", "func F(b bool) { x, y := 1, 2; println(y); if b { println(x) } }", 1},
		{"shadow chain", "func F(b bool) { x := 1; if b { y := x; { x := y; println(x) } } }", 1},
	} {
		t.Run(tc.name, func(t *testing.T) { assertNarrowScope(t, tc.body, tc.want) })
	}
}

func TestNarrowScopeMovableInitializers(t *testing.T) {
	for name, body := range map[string]string{
		"zero in if":        "func F(b bool) { n := 0; if b { println(n) } }",
		"empty buffer":      "func F() { buf := []byte{}; for i := 0; i < 2; i++ { println(buf) } }",
		"var literal":       "func F(b bool) { var n = 0; if b { println(n) } }",
		"typed var literal": "func F(b bool) { var n int = 0; if b { println(n) } }",
		"local constant":    "func F(b bool) { const c = 2; n := c; if b { println(n) } }",
		"package constant":  "const c = 2; func F(b bool) { n := c; if b { println(n) } }",
		"inherited const":   "func F(b bool) { const (a = iota; c); n := c; if b { println(n) } }",
		"constant elements": "func F(b bool) { const c = 2; n := []int{1, c}; if b { println(n) } }",
		"constant keys":     "func F(b bool) { const c = 2; n := map[int]int{c: 1}; if b { println(n) } }",
		"nested literals":   "func F(b bool) { n := [][]int{{1}, {2}}; if b { println(n) } }",
		"signed literal":    "func F(b bool) { n := -1; if b { println(n) } }",
		"parentheses":       "func F(b bool) { n := (1); if b { println(n) } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 1) })
	}
}

func TestNarrowScopeOrderedInitializers(t *testing.T) {
	for name, body := range map[string]string{
		"if error idiom": "func f() error { return nil }; func F() { err := f(); if err != nil { println(err) } }",
		"compute":        "func compute() int { return 1 }; func F(b bool) { x := compute(); if b { println(x) } }",
		"var call":       "func compute() int { return 1 }; func F(b bool) { var x = compute(); if b { println(x) } }",
		"receive":        "func F(b bool, ch chan int) { x := <-ch; if b { println(x) } }",
		"index":          "func F(b bool, xs []int) { x := xs[0]; if b { println(x) } }",
		"selector":       "func F(b bool, s struct{ n int }) { x := s.n; if b { println(x) } }",
		"method value":   "type S int; func (S) M() {}; func F(b bool, s S) { x := s.M; if b { x() } }",
		"closure":        "func F(b bool) { x := func() {}; if b { x() } }",
		"variable":       "func F(b bool, c int) { x := c; if b { println(x) } }",
		"shadow const":   "const c = 2; func F(b bool, c int) { x := c; if b { println(x) } }",
		"shadow bool":    "func F(b bool, true bool) { x := true; if b { println(x) } }",
		"package bool":   "var true = false; func F(b bool) { x := true; if b { println(x) } }",
		"literal call":   "func g() int { return 1 }; func F(b bool) { x := []int{g()}; if b { println(x) } }",
		"literal index":  "func F(b bool, xs []int) { x := []int{xs[0]}; if b { println(x) } }",
		"map key read":   "func F(b bool, k int) { x := map[int]int{k: 1}; if b { println(x) } }",
		"mixed short":    "func g() int { return 1 }; func F(b bool) { x, y := 1, g(); if b { println(x, y) } }",
		"mixed var":      "func g() int { return 1 }; func F(b bool) { var x, y = 1, g(); if b { println(x, y) } }",
		"conversion":     "func F(b bool) { x := int(1); if b { println(x) } }",
		"dereference":    "func F(b bool, p *int) { x := *p; if b { println(x) } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 0) })
	}
}

func TestNarrowScopeInitializerBoundaries(t *testing.T) {
	for _, tc := range []struct{ elements, want int }{
		{0, 1}, {maxNodeStack - 1, 1}, {maxNodeStack, 0},
	} {
		body := "func F(b bool) { x := []int{" + strings.Repeat("1,", tc.elements) + "}; if b { println(x) } }"
		assertNarrowScope(t, body, tc.want)
	}
}

func TestNarrowScopeFixtures(t *testing.T) {
	root := filepath.Join("..", "..", ".config", "hiss", "testdata", "HISS-05", "go")
	for _, bucket := range []struct {
		name string
		want int
	}{{"positive", 1}, {"negative", 0}, {"gap", 0}} {
		paths, err := filepath.Glob(filepath.Join(root, bucket.name, "*.go"))
		if err != nil || len(paths) == 0 {
			t.Fatalf("fixture bucket %s: %v, %v", bucket.name, paths, err)
		}
		for _, path := range paths {
			t.Run(bucket.name+"/"+filepath.Base(path), func(t *testing.T) {
				src, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				rep := scanFixtureFile(t, "input.go", string(src))
				if got := rep.Breakdown["HISS-05"]; got != bucket.want {
					t.Fatalf("findings = %d, want %d: %+v", got, bucket.want, rep.Violations)
				}
			})
		}
	}
}
