// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package hiss

import "testing"

func TestNarrowScopeLoopMutations(t *testing.T) {
	for name, body := range map[string]string{
		"assignment":           "func F() { n := 0; for i := 0; i < 2; i++ { println(n); n = i } }",
		"compound":             "func F() { n := 0; for i := 0; i < 2; i++ { n += i; println(n) } }",
		"decrement":            "func F() { n := 2; for i := 0; i < 2; i++ { n--; println(n) } }",
		"field":                "func F() { var n struct{ x int }; for i := 0; i < 2; i++ { n.x = i; println(n.x) } }",
		"element":              "func F() { n := []int{0}; for i := 0; i < 2; i++ { n[0]++; println(n[0]) } }",
		"delete":               "func F() { n := map[int]int{0: 1}; for i := 0; i < 2; i++ { println(n[i]); delete(n, i) } }",
		"clear":                "func F() { n := []int{1}; for i := 0; i < 2; i++ { println(n[0]); clear(n) } }",
		"address":              "func F() { n := 0; for i := 0; i < 2; i++ { f(&n) } }",
		"field address":        "func F() { var n struct{ x int }; for i := 0; i < 2; i++ { f(&n.x) } }",
		"method":               "func F() { var n S; for i := 0; i < 2; i++ { n.M() } }",
		"parenthesised method": "func F() { var n S; for i := 0; i < 2; i++ { (n.M)() } }",
		"post":                 "func F() { n := 0; for ; n < 2; n++ { println(n) } }",
		"range assignment":     "func F(xs []int) { n := 0; for _, n = range xs { println(n) } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 0) })
	}
}

func TestNarrowScopeLoopReadOnly(t *testing.T) {
	for name, body := range map[string]string{
		"literal":            "func F() { n := 1; for i := 0; i < 2; i++ { println(n) } }",
		"shadow write":       "func F() { n := 1; for i := 0; i < 2; i++ { println(n); n := 2; n++ } }",
		"index read":         "func F() { n := []int{1}; for i := 0; i < 2; i++ { println(n[0]) } }",
		"index on lhs":       "func F() { n := 0; for i := 0; i < 2; i++ { xs[n] = i } }",
		"plain if write":     "func F(b bool) { n := 0; if b { n++; println(n) } }",
		"plain switch write": "func F(b bool) { n := 0; switch b { case true: n++; println(n) } }",
	} {
		t.Run(name, func(t *testing.T) { assertNarrowScope(t, body, 1) })
	}
}
