package hiss

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// scanSource scans a single Go source file in a fresh root and returns the report.
func scanSource(t *testing.T, body string) *ScanReport {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	rep, err := Scan(ctx, dir, ScanOptions{})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return rep
}

// TestScanReportsDirectRecursion is the positive dimension. HISS-01 declares that the call
// graph must form a DAG and that a violation is an immediate build failure, but the scanner
// matched only `goto`, so every recursive function passed the gate.
func TestScanReportsDirectRecursion(t *testing.T) {
	rep := scanSource(t, "package p\n\nfunc Factorial(n int) int {\n\tif n <= 1 {\n\t\treturn 1\n\t}\n\treturn n * Factorial(n-1)\n}\n")

	if rep.Breakdown["HISS-01"] != 1 {
		t.Fatalf("direct recursion must be reported once, got %d: %+v", rep.Breakdown["HISS-01"], rep.Violations)
	}
	if rep.Violations[0].Symbol != "Factorial" {
		t.Errorf("the violation must name the recursive function, got %q", rep.Violations[0].Symbol)
	}
}

// TestScanReportsRecursiveMethod is the positive dimension for the method form, where the
// call is a selector on the bound receiver rather than a bare identifier.
func TestScanReportsRecursiveMethod(t *testing.T) {
	rep := scanSource(t, "package p\n\ntype T struct{ next *T }\n\nfunc (t *T) Walk() int {\n\tif t.next == nil {\n\t\treturn 0\n\t}\n\treturn t.Walk()\n}\n")

	if rep.Breakdown["HISS-01"] != 1 {
		t.Fatalf("a recursive method must be reported, got %d: %+v", rep.Breakdown["HISS-01"], rep.Violations)
	}
}

// TestScanAllowsDelegationToSameName is the negative dimension and the reason the check
// resolves the receiver rather than matching on name alone. Forwarding to a same-named
// method on a different value is the ordinary delegation idiom, not recursion; flagging it
// would make the rule unusable and push people to suppress it.
func TestScanAllowsDelegationToSameName(t *testing.T) {
	rep := scanSource(t, "package p\n\nimport \"io\"\n\ntype W struct{ inner io.Closer }\n\nfunc (w *W) Close() error {\n\treturn w.inner.Close()\n}\n\nfunc Close(c io.Closer) error {\n\treturn c.Close()\n}\n")

	if rep.Breakdown["HISS-01"] != 0 {
		t.Fatalf("delegation to a same-named method is not recursion: %+v", rep.Violations)
	}
}

// TestScanAllowsNonRecursiveCalls is the negative dimension for ordinary calls: a function
// calling a different function must never be reported, or the rule reports every program.
func TestScanAllowsNonRecursiveCalls(t *testing.T) {
	rep := scanSource(t, "package p\n\nfunc helper(n int) int { return n + 1 }\n\nfunc Caller(n int) int {\n\treturn helper(n)\n}\n")

	if rep.Breakdown["HISS-01"] != 0 {
		t.Fatalf("a call to another function is not recursion: %+v", rep.Violations)
	}
}

// TestScanRecursionInsideClosure is the boundary dimension: a call to the enclosing
// function from inside a closure it declares is still recursion, because invoking the
// closure re-enters the function.
func TestScanRecursionInsideClosure(t *testing.T) {
	rep := scanSource(t, "package p\n\nfunc Outer(n int) int {\n\tf := func() int {\n\t\treturn Outer(n - 1)\n\t}\n\tif n <= 0 {\n\t\treturn 0\n\t}\n\treturn f()\n}\n")

	if rep.Breakdown["HISS-01"] != 1 {
		t.Fatalf("recursion through a closure must be reported, got %d: %+v", rep.Breakdown["HISS-01"], rep.Violations)
	}
}

// TestScanMutualRecursionRemainsUncovered is the boundary dimension that records a known
// gap rather than hiding it. Deciding mutual recursion needs a whole-program call graph,
// which this single-file scanner does not build. The test exists so the limit is explicit
// and so that closing it later is a visible change rather than a silent one.
func TestScanMutualRecursionRemainsUncovered(t *testing.T) {
	rep := scanSource(t, "package p\n\nfunc IsEven(n int) bool {\n\tif n == 0 {\n\t\treturn true\n\t}\n\treturn IsOdd(n - 1)\n}\n\nfunc IsOdd(n int) bool {\n\tif n == 0 {\n\t\treturn false\n\t}\n\treturn IsEven(n - 1)\n}\n")

	if got := rep.Breakdown["HISS-01"]; got != 0 {
		t.Fatalf("mutual recursion is now detected (%d findings): close this gap in the matrix "+
			"and replace this test with a positive one", got)
	}
}
