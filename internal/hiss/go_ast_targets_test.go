// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"go/ast"
	"go/parser"
	"testing"
)

// CallTargetsEnclosing is shared with standards-lsp, so its parenthesised and instantiated
// callee shapes are pinned here directly: positive for a method called through a parenthesised
// receiver and for a parenthesised plain or instantiated self-call, negative for delegation to
// another value and for a different function.
func TestCallTargetsEnclosingShapes(t *testing.T) {
	for _, tc := range []struct {
		call, fn, recv string
		want           bool
	}{
		{"(r).M()", "M", "r", true},
		{"((r)).M()", "M", "r", true},
		{"(f)(x)", "f", "", true},
		{"(f[int])(x)", "f", "", true},
		{"f[int, string](x)", "f", "", true},
		{"x.inner.M()", "M", "r", false},
		{"(r.other).M()", "M", "r", false},
		{"g(x)", "f", "", false},
		{"(r).M()", "M", "", false},
	} {
		expr, err := parser.ParseExpr(tc.call)
		if err != nil {
			t.Fatalf("parse %q: %v", tc.call, err)
		}
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			t.Fatalf("%q is no call", tc.call)
		}
		if got := CallTargetsEnclosing(call.Fun, tc.fn, tc.recv); got != tc.want {
			t.Errorf("CallTargetsEnclosing(%q, fn=%q, recv=%q) = %v, want %v", tc.call, tc.fn, tc.recv, got, tc.want)
		}
	}
}
