// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// scanSources writes each named source into one package directory and returns the HISS-01
// findings, so a test states the shape it means rather than the plumbing.
func scanSources(t *testing.T, sources map[string]string) []InvariantViolation {
	t.Helper()
	dir := t.TempDir()
	for name, body := range sources {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Scan(context.Background(), dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var found []InvariantViolation
	for _, v := range report.Violations {
		if v.RuleID == "HISS-01" {
			found = append(found, v)
		}
	}
	return found
}

func cycleMessages(found []InvariantViolation) string {
	var sb strings.Builder
	for _, v := range found {
		sb.WriteString(v.Message)
		sb.WriteString("\n")
	}
	return sb.String()
}

// Positive: a cycle through two functions in separate files. No single file's AST shows the
// loop closing, which is why the per-file scanner never saw it.
func TestCallGraphReportsMutualRecursionAcrossFiles(t *testing.T) {
	found := scanSources(t, map[string]string{
		"even.go": "package p\n\nfunc isEven(n int) bool {\n\tif n == 0 {\n\t\treturn true\n\t}\n\treturn isOdd(n - 1)\n}\n",
		"odd.go":  "package p\n\nfunc isOdd(n int) bool {\n\tif n == 0 {\n\t\treturn false\n\t}\n\treturn isEven(n - 1)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected exactly one cycle, got %d: %s", len(found), cycleMessages(found))
	}
	for _, want := range []string{"isEven", "isOdd", "acyclic DAG"} {
		if !strings.Contains(found[0].Message, want) {
			t.Errorf("message must name %q, got %q", want, found[0].Message)
		}
	}
}

// Positive: a three-function cycle is indirect recursion, and the message names the path.
func TestCallGraphReportsIndirectCycle(t *testing.T) {
	found := scanSources(t, map[string]string{
		"chain.go": "package p\n\nfunc alpha() int { return beta() }\n\nfunc beta() int { return gamma() }\n\nfunc gamma() int { return alpha() }\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected one cycle, got %d: %s", len(found), cycleMessages(found))
	}
	for _, want := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(found[0].Message, want) {
			t.Errorf("message must name %q, got %q", want, found[0].Message)
		}
	}
}

// Negative: a call chain that terminates is a DAG and must not be reported. Without this a
// cycle checker that flagged every edge would look like it worked.
func TestCallGraphAcceptsADirectedAcyclicChain(t *testing.T) {
	found := scanSources(t, map[string]string{
		"dag.go": "package p\n\nfunc top() int { return middle() + leaf() }\n\nfunc middle() int { return leaf() }\n\nfunc leaf() int { return 1 }\n",
	})
	if len(found) != 0 {
		t.Fatalf("an acyclic chain must not be reported, got: %s", cycleMessages(found))
	}
}

// Negative: direct recursion is one defect and must be reported once. The per-file scanner
// already names it, so the call-graph pass must skip the single-node component.
func TestCallGraphDoesNotDoubleReportDirectRecursion(t *testing.T) {
	found := scanSources(t, map[string]string{
		"fact.go": "package p\n\nfunc factorial(n int) int {\n\tif n <= 1 {\n\t\treturn 1\n\t}\n\treturn n * factorial(n-1)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("direct recursion must be reported exactly once, got %d: %s", len(found), cycleMessages(found))
	}
	if strings.Contains(found[0].Message, "Call cycle through") {
		t.Errorf("direct recursion must keep its own message, got %q", found[0].Message)
	}
}

// Negative: a local of the same name shadows the function, so the call does not reach it and
// no edge exists. An edge here would be a false cycle.
func TestCallGraphIgnoresShadowedNames(t *testing.T) {
	// helper calls caller, so an edge caller -> helper would close a cycle. It must not exist:
	// caller's local shadows the package function, and the call reaches the local.
	found := scanSources(t, map[string]string{
		"shadow.go": "package p\n\nfunc helper() int { return caller() }\n\nfunc caller() int {\n\thelper := func() int { return 1 }\n\treturn helper()\n}\n",
	})
	if len(found) != 0 {
		t.Fatalf("a shadowed name must not create an edge, got: %s", cycleMessages(found))
	}
}

// Negative: a parameter of the same name shadows the function just as a local does, so a
// call through it adds no edge and closes no cycle.
func TestCallGraphIgnoresParameterShadowedNames(t *testing.T) {
	found := scanSources(t, map[string]string{
		"param.go": "package p\n\nfunc helper() int { return caller(nil) }\n\n" +
			"func caller(helper func() int) int {\n\treturn helper()\n}\n",
	})
	if len(found) != 0 {
		t.Fatalf("a parameter-shadowed name must not create an edge, got: %s", cycleMessages(found))
	}
}

// Boundary: a method and a plain function may share a name. Conflating them would invent an
// edge between unrelated symbols and report a cycle that does not exist.
func TestCallGraphKeepsMethodsOutOfTheFunctionGraph(t *testing.T) {
	found := scanSources(t, map[string]string{
		"collide.go": "package p\n\ntype box struct{}\n\nfunc (b box) run() int { return b.step() }\n\n" +
			"func (b box) step() int { return b.run() }\n\nfunc run() int { return step() }\n\nfunc step() int { return 1 }\n",
	})
	if len(found) != 0 {
		t.Fatalf("methods must not be conflated with functions of the same name, got: %s",
			cycleMessages(found))
	}
}

// Boundary: an edge leaving the package cannot close a cycle, because a cycle back would
// require a mutual import and the compiler rejects that.
func TestCallGraphIgnoresCallsOutsideThePackage(t *testing.T) {
	found := scanSources(t, map[string]string{
		"out.go": "package p\n\nimport \"strings\"\n\nfunc trim(s string) string { return strings.TrimSpace(s) }\n\nfunc use() string { return trim(\" x \") }\n",
	})
	if len(found) != 0 {
		t.Fatalf("calls leaving the package must not be reported, got: %s", cycleMessages(found))
	}
}

// Boundary: a method cycle is a declared gap, not a silent miss. If this ever starts being
// reported the catalog entry must change with it.
func TestCallGraphHoldsMethodCyclesAsADeclaredGap(t *testing.T) {
	found := scanSources(t, map[string]string{
		"method.go": "package p\n\ntype w struct{ d int }\n\nfunc (x *w) down() int { return x.up() }\n\nfunc (x *w) up() int { return x.down() }\n",
	})
	if len(found) != 0 {
		t.Fatalf("method cycles are a declared gap; reporting one means the catalog is now wrong: %s",
			cycleMessages(found))
	}
}

// Boundary: a method declared before a function of the same name must not take over that
// name's recorded position. If it did, a reported cycle would point at the wrong file and
// line, sending the reader to a symbol that is not in the cycle.
func TestCallGraphAttributesACycleToTheFunctionNotAMethod(t *testing.T) {
	found := scanSources(t, map[string]string{
		"a_method.go": "package p\n\ntype box struct{}\n\nfunc (b box) helper() int { return 0 }\n",
		"b_funcs.go":  "package p\n\nfunc helper() int { return partner() }\n\nfunc partner() int { return helper() }\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected one cycle, got %d: %s", len(found), cycleMessages(found))
	}
	if found[0].FilePath != "b_funcs.go" {
		t.Errorf("the cycle must be attributed to the function's file, got %q", found[0].FilePath)
	}
}

// Boundary: two independent cycles in one package are two findings, not one.
func TestCallGraphReportsEachCycleSeparately(t *testing.T) {
	found := scanSources(t, map[string]string{
		"two.go": "package p\n\nfunc a1() int { return a2() }\n\nfunc a2() int { return a1() }\n\nfunc b1() int { return b2() }\n\nfunc b2() int { return b1() }\n",
	})
	if len(found) != 2 {
		t.Fatalf("expected two independent cycles, got %d: %s", len(found), cycleMessages(found))
	}
}

// Positive: a local of the callee's name that is not in scope at the call hides nothing. The
// whole-body check dropped each of these edges, so the cycle through it went unreported (#733).
func TestCallGraphSeesACycleBehindAnOutOfScopeLocal(t *testing.T) {
	for name, caller := range map[string]string{
		"inner block": "func f(b bool) {\n\tif b {\n\t\tg := 1\n\t\t_ = g\n\t}\n\tg(b)\n}\n",
		"after call":  "func f(b bool) {\n\tg(b)\n\tg := 1\n\t_ = g\n}\n",
		"sibling case": "func f(b bool) {\n\tswitch b {\n\tcase true:\n\t\tg := 1\n\t\t_ = g\n" +
			"\tcase false:\n\t\tg(b)\n\t}\n}\n",
		"literal local": "func f(b bool) {\n\th := func() {\n\t\tg := 1\n\t\t_ = g\n\t}\n\th()\n\tg(b)\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			found := scanSources(t, map[string]string{"a.go": "package p\n\n" + caller + "\nfunc g(b bool) { f(b) }\n"})
			if len(found) != 1 || !strings.Contains(found[0].Message, "f -> g -> f") {
				t.Fatalf("the cycle f -> g -> f must be reported once, got %d: %s", len(found), cycleMessages(found))
			}
		})
	}
}

// Negative: a binding of the callee's name in scope at the call is what the call reaches, so
// no edge exists and no cycle closes.
func TestCallGraphIgnoresBindingsInScopeAtTheCall(t *testing.T) {
	for name, caller := range map[string]string{
		"short variable":  "func f(b bool) {\n\tg := func(bool) {}\n\tg(b)\n}\n",
		"parameter":       "func f(b bool, g func(bool)) {\n\tg(b)\n}\n",
		"named result":    "func f(b bool) (g func(bool)) {\n\tg(b)\n\treturn\n}\n",
		"enclosing block": "func f(b bool) {\n\tg := func(bool) {}\n\tif b {\n\t\tfor i := 0; i < 1; i++ {\n\t\t\tg(b)\n\t\t}\n\t}\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			found := scanSources(t, map[string]string{"a.go": "package p\n\n" + caller + "\nfunc g(b bool) { f(b) }\n"})
			if len(found) != 0 {
				t.Fatalf("a call reaching a binding in scope adds no edge, got: %s", cycleMessages(found))
			}
		})
	}
}

// Boundary: a binding inside a function literal shadows only the calls inside that literal.
// The literal's own call to g reaches its parameter; the enclosing function's call after it
// reaches the function g and closes the cycle.
func TestCallGraphScopesALiteralsBindingsToTheLiteral(t *testing.T) {
	inside := "package p\n\nfunc f(b bool) {\n\th := func(g func(bool)) { g(b) }\n\th(nil)\n}\n\nfunc g(b bool) { f(b) }\n"
	if found := scanSources(t, map[string]string{"a.go": inside}); len(found) != 0 {
		t.Fatalf("a call inside the literal reaches its parameter, got: %s", cycleMessages(found))
	}
	after := "package p\n\nfunc f(b bool) {\n\th := func(g func(bool)) { g(b) }\n\th(nil)\n\tg(b)\n}\n\nfunc g(b bool) { f(b) }\n"
	if found := scanSources(t, map[string]string{"a.go": after}); len(found) != 1 {
		t.Fatalf("the call after the literal reaches the function g, got %d: %s", len(found), cycleMessages(found))
	}
}

// Positive: a cycle between generic functions with explicit type arguments is reported (#738).
func TestCallGraphReportsGenericCallCycle(t *testing.T) {
	found := scanSources(t, map[string]string{
		"generic.go": "package p\n\nfunc f[T any](x T) T {\n\treturn g[T](x)\n}\n\nfunc g[T any](x T) T {\n\treturn f[T](x)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected one generic cycle, got %d: %s", len(found), cycleMessages(found))
	}
	if !strings.Contains(found[0].Message, "f -> g -> f") {
		t.Errorf("message must name the cycle f -> g -> f, got %q", found[0].Message)
	}
}

// Positive: a cycle with two type parameters (an IndexListExpr) is reported (#738).
func TestCallGraphReportsTwoTypeParameterGenericCycle(t *testing.T) {
	found := scanSources(t, map[string]string{
		"two_params.go": "package p\n\nfunc alpha[A, B any](a A, b B) (A, B) {\n\treturn beta[A, B](a, b)\n}\n\n" +
			"func beta[A, B any](a A, b B) (A, B) {\n\treturn alpha[A, B](a, b)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected one two-type-parameter cycle, got %d: %s", len(found), cycleMessages(found))
	}
	if !strings.Contains(found[0].Message, "alpha -> beta -> alpha") {
		t.Errorf("message must name the cycle alpha -> beta -> alpha, got %q", found[0].Message)
	}
}

// Negative: an instantiated name that is a local function value in scope adds no edge (#738).
func TestCallGraphIgnoresInstantiatedLocalInScope(t *testing.T) {
	found := scanSources(t, map[string]string{
		"shadow_generic.go": "package p\n\nfunc helper[T any](x T) T {\n\treturn caller[T](x)\n}\n\n" +
			"func caller[T any](x T) T {\n\thelper := []func(T) T{func(v T) T { return v }}\n\treturn helper[0](x)\n}\n",
	})
	if len(found) != 0 {
		t.Fatalf("an instantiated local in scope must not add a call-graph edge, got: %s", cycleMessages(found))
	}
}

// Boundary: a direct self-call through an explicit instantiation is reported once by the
// per-file scanner, not duplicated by the call-graph cycle pass (#738).
func TestCallGraphDoesNotDoubleReportGenericDirectRecursion(t *testing.T) {
	found := scanSources(t, map[string]string{
		"self_generic.go": "package p\n\nfunc loop[T any](x T) T {\n\tif true {\n\t\treturn x\n\t}\n\treturn loop[T](x)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("direct generic recursion must be reported exactly once, got %d: %s", len(found), cycleMessages(found))
	}
	if strings.Contains(found[0].Message, "Call cycle through") {
		t.Errorf("direct generic recursion must keep its own message, got %q", found[0].Message)
	}
	if !strings.Contains(found[0].Message, "Direct recursion in loop") {
		t.Errorf("message must be direct recursion, got %q", found[0].Message)
	}
}

// Positive: a cycle with a parenthesised generic call (e.g. (g[T])(x)) is reported (#738).
func TestCallGraphReportsParenthesisedGenericCallCycle(t *testing.T) {
	found := scanSources(t, map[string]string{
		"paren_generic.go": "package p\n\nfunc f[T any](x T) T {\n\treturn (g[T])(x)\n}\n\nfunc g[T any](x T) T {\n\treturn f[T](x)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("expected one generic cycle, got %d: %s", len(found), cycleMessages(found))
	}
	if !strings.Contains(found[0].Message, "f -> g -> f") {
		t.Errorf("message must name the cycle f -> g -> f, got %q", found[0].Message)
	}
}

// Negative: a parenthesised instantiated local function value in scope adds no edge (#738).
func TestCallGraphIgnoresParenthesisedInstantiatedLocalInScope(t *testing.T) {
	found := scanSources(t, map[string]string{
		"shadow_paren_generic.go": "package p\n\nfunc helper[T any](x T) T {\n\treturn caller[T](x)\n}\n\n" +
			"func caller[T any](x T) T {\n\thelper := []func(T) T{func(v T) T { return v }}\n\treturn (helper[0])(x)\n}\n",
	})
	if len(found) != 0 {
		t.Fatalf("a parenthesised instantiated local in scope must not add a call-graph edge, got: %s", cycleMessages(found))
	}
}

// Boundary: a direct self-call through a parenthesised explicit instantiation is reported once by the
// per-file scanner, not duplicated by the call-graph cycle pass (#738).
func TestCallGraphDoesNotDoubleReportParenthesisedGenericDirectRecursion(t *testing.T) {
	found := scanSources(t, map[string]string{
		"self_paren_generic.go": "package p\n\nfunc loop[T any](x T) T {\n\tif true {\n\t\treturn x\n\t}\n\treturn (loop[T])(x)\n}\n",
	})
	if len(found) != 1 {
		t.Fatalf("direct generic recursion must be reported exactly once, got %d: %s", len(found), cycleMessages(found))
	}
	if strings.Contains(found[0].Message, "Call cycle through") {
		t.Errorf("direct generic recursion must keep its own message, got %q", found[0].Message)
	}
	if !strings.Contains(found[0].Message, "Direct recursion in loop") {
		t.Errorf("message must be direct recursion, got %q", found[0].Message)
	}
}

// Boundary: a function of 10001 calls costs one walk. Checking every call by walking the whole
// body again made the cost calls times nodes. The scope walk counts the nodes it visits
// (goScope.steps), so the test holds the build to a bounded number of visits per node instead
// of timing it: a wall-clock ratio flaked on a loaded host (45x against a 24x bound) with no
// defect present, while the visit count is the same on every machine.
func TestCallGraphScansCallsInLinearTime(t *testing.T) {
	for _, calls := range []int{1250, 10001} {
		file := parseCallsFile(t, calls)
		nodes := 0
		ast.Inspect(file, func(n ast.Node) bool {
			if n != nil {
				nodes++
			}
			return true
		})
		graph := newCallGraph()
		graph.addFile(file, "big.go")
		if _, edge := graph.edges["big"]["leaf"]; !edge || graph.count != 1 {
			t.Fatalf("one function calling leaf %d times is one edge, got %d", calls, graph.count)
		}
		if graph.steps == 0 || graph.steps > nodes {
			t.Fatalf("%d calls: the scope walk visited %d nodes of a %d-node file; one walk per function visits each node at most once",
				calls, graph.steps, nodes)
		}
	}
}

// parseCallsFile parses one function making calls bare calls to leaf.
func parseCallsFile(t *testing.T, calls int) *ast.File {
	t.Helper()
	src := "package p\n\nfunc leaf() {}\n\nfunc big() {\n" + strings.Repeat("\tleaf()\n", calls) + "}\n"
	file, err := parser.ParseFile(token.NewFileSet(), "big.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	return file
}
