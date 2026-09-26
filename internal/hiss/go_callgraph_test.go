// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
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
