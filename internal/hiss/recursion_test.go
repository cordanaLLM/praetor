package hiss

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// TestScanMutualRecursionIsReported replaces the gap test that used to sit here. That test
// asserted mutual recursion went undetected and instructed whoever closed the gap to swap it
// for a positive one, which is what happened: the call-graph pass in go_callgraph.go now
// builds each package's graph after the walk and reports its cycles.
//
// The gap it recorded has moved rather than vanished. A cycle through methods is still
// undecided, and .config/hiss/testdata/HISS-01/go/gap/method-cycle.go records that.
func TestScanMutualRecursionIsReported(t *testing.T) {
	dir := t.TempDir()
	source := "package p\n\nfunc IsEven(n int) bool {\n\tif n == 0 {\n\t\treturn true\n\t}\n" +
		"\treturn IsOdd(n - 1)\n}\n\nfunc IsOdd(n int) bool {\n\tif n == 0 {\n\t\treturn false\n\t}\n" +
		"\treturn IsEven(n - 1)\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "mutual.go"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Scan(context.Background(), dir, ScanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := rep.Breakdown["HISS-01"]; got != 1 {
		t.Fatalf("mutual recursion must be reported exactly once, got %d: %+v", got, rep.Violations)
	}
	if !strings.Contains(rep.Violations[0].Message, "IsOdd") {
		t.Errorf("the finding must name the cycle path, got %q", rep.Violations[0].Message)
	}
}

// TestScanSignatureBindingShadowsTheFunction is a negative dimension: a parameter or named
// result of the function's own name is what a bare call reaches, so the call is not
// recursion. Checking only body locals reported both.
func TestScanSignatureBindingShadowsTheFunction(t *testing.T) {
	rep := scanSource(t, "package p\n\nfunc walk(walk func()) {\n\twalk()\n}\n\n"+
		"func next() (next func() int) {\n\tnext = func() int { return 1 }\n\tnext()\n\treturn\n}\n")

	if rep.Breakdown["HISS-01"] != 0 {
		t.Fatalf("a parameter or named result of the same name is not the function: %+v", rep.Violations)
	}
}

// TestScanRecursiveMethodDespiteASameNamedLocal is the boundary: a method is called through
// its receiver, so a local named like the method cannot shadow the call and the recursion is
// still reported.
func TestScanRecursiveMethodDespiteASameNamedLocal(t *testing.T) {
	rep := scanSource(t, "package p\n\ntype T struct{ next *T }\n\nfunc (t *T) Walk() int {\n\tWalk := 1\n"+
		"\tif t.next == nil {\n\t\treturn Walk\n\t}\n\treturn t.Walk()\n}\n")

	if rep.Breakdown["HISS-01"] != 1 {
		t.Fatalf("a recursive method must be reported despite a same-named local, got %d: %+v",
			rep.Breakdown["HISS-01"], rep.Violations)
	}
}

// selfCallLines scans one Rust or Python source and returns the lines HISS-01 reported, with
// the symbols they named.
func selfCallLines(t *testing.T, rel, src string) ([]int, []string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, rel, src)
	rep := scanFixture(t, root, ScanOptions{})
	var lines []int
	var symbols []string
	for _, v := range rep.Violations {
		if v.RuleID == "HISS-01" {
			lines = append(lines, v.LineNumber)
			symbols = append(symbols, v.Symbol)
		}
	}
	return lines, symbols
}

func assertSelfCalls(t *testing.T, rel, src string, want ...int) {
	t.Helper()
	got, _ := selfCallLines(t, rel, src)
	if len(got) != len(want) {
		t.Fatalf("%s: HISS-01 lines = %v, want %v\n%s", rel, got, want, src)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: HISS-01 lines = %v, want %v\n%s", rel, got, want, src)
		}
	}
}

// TestRustSelfRecursionIsReported is the positive dimension for Rust, which had no recursion
// check at all: a free function by its bare name, a method through self, an associated
// function through Self, a one-line body, and a receiver on a wrapped signature line.
func TestRustSelfRecursionIsReported(t *testing.T) {
	lines, symbols := selfCallLines(t, "src/lib.rs", "pub fn factorial(n: u64) -> u64 {\n    if n <= 1 {\n        return 1;\n    }\n    n * factorial(n - 1)\n}\n")
	if len(lines) != 1 || lines[0] != 5 || symbols[0] != "factorial" {
		t.Fatalf("free-function recursion must be reported once on its call line, got %v %v", lines, symbols)
	}
	assertSelfCalls(t, "src/m.rs", "impl C {\n    pub fn drain(&mut self) -> u32 {\n        1 + self.drain()\n    }\n}\n", 3)
	assertSelfCalls(t, "src/a.rs", "impl T {\n    fn count(n: u32) -> u32 {\n        1 + Self::count(n - 1)\n    }\n}\n", 3)
	assertSelfCalls(t, "src/s.rs", "fn f(n: u32) -> u32 { if n == 0 { 0 } else { f(n - 1) } }\n", 1)
	assertSelfCalls(t, "src/w.rs", "impl N {\n    pub fn walk(\n        &self,\n        n: u32,\n    ) -> u32 {\n        Self::walk(self, n)\n    }\n}\n", 6)
}

// TestRustSelfRecursionFollowsResolution is the negative dimension: every one of these calls
// the function's name without reaching the function, so reporting it would punish delegation.
func TestRustSelfRecursionFollowsResolution(t *testing.T) {
	for name, src := range map[string]string{
		"delegation":   "impl<W: Write> Wrapper<W> {\n    pub fn flush(&mut self) -> Result<()> {\n        self.inner.flush()\n    }\n}\n",
		"path":         "pub fn parse(s: &str) -> usize {\n    codec::parse(s)\n}\n",
		"impl bare":    "impl View {\n    pub fn render(&self) -> String {\n        render(self.0)\n    }\n}\n",
		"assoc bare":   "impl View {\n    fn build(v: u32) -> String {\n        build(v)\n    }\n}\n",
		"let closure":  "fn step(n: u32) -> u32 {\n    let step = |x: u32| x + 1;\n    step(n)\n}\n",
		"parameter":    "fn apply(apply: fn(u32) -> u32, n: u32) -> u32 {\n    apply(n)\n}\n",
		"match arm":    "fn run(cb: Option<fn()>) {\n    match cb {\n        Some(run) => run(),\n        None => {}\n    }\n}\n",
		"nested fn":    "fn outer() {\n    fn outer() {}\n    outer()\n}\n",
		"method bare":  "impl S {\n    fn close(self) {\n        drop(self);\n    }\n}\n",
		"declaration":  "trait T {\n    fn f(&self);\n}\nfn g() {\n    f()\n}\n",
		"string":       "fn f() {\n    let s = \"f()\";\n}\n",
		"macro":        "fn vec() -> Vec<u8> {\n    vec![1]\n}\n",
		"other method": "fn len(v: &[u8]) -> usize {\n    v.len()\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "src/lib.rs", src)
		})
	}
}

// TestRustSelfRecursionBoundaries covers the edges of the decision: a call from a closure the
// function declares still re-enters it, an impl body's scope ends at its closing brace, a
// trait's default method is a method, and the recorded call sites are bounded.
func TestRustSelfRecursionBoundaries(t *testing.T) {
	assertSelfCalls(t, "src/c.rs", "fn outer(n: u32) -> u32 {\n    let f = || outer(n - 1);\n    f()\n}\n", 2)
	assertSelfCalls(t, "src/i.rs", "impl S {\n    fn m(&self) {}\n}\n\nfn walk(n: u32) {\n    walk(n)\n}\n", 6)
	assertSelfCalls(t, "src/t.rs", "trait T {\n    fn f(&self) -> u32 {\n        self.f()\n    }\n}\n", 3)

	var src strings.Builder
	src.WriteString("fn f(n: u32) -> u32 {\n")
	for i := 0; i < maxSelfCallSites+8; i++ {
		src.WriteString("    f(n);\n")
	}
	src.WriteString("    0\n}\n")
	got, _ := selfCallLines(t, "src/cap.rs", src.String())
	if len(got) != maxSelfCallSites {
		t.Fatalf("call sites must be bounded at %d, got %d", maxSelfCallSites, len(got))
	}
}

// TestPythonSelfRecursionIsReported is the positive dimension for Python, which had no
// recursion check at all.
func TestPythonSelfRecursionIsReported(t *testing.T) {
	lines, symbols := selfCallLines(t, "m.py", "def factorial(n):\n    if n <= 1:\n        return 1\n    return n * factorial(n - 1)\n")
	if len(lines) != 1 || lines[0] != 4 || symbols[0] != "factorial" {
		t.Fatalf("plain recursion must be reported once on its call line, got %v %v", lines, symbols)
	}
	assertSelfCalls(t, "a.py", "class C:\n    def drain(self):\n        return 1 + self.drain()\n", 3)
	assertSelfCalls(t, "b.py", "class C:\n    @classmethod\n    def build(cls, n):\n        return cls.build(n - 1)\n", 4)
	assertSelfCalls(t, "c.py", "class Tree:\n    @staticmethod\n    def count(n):\n        return Tree.count(n - 1)\n", 4)
	assertSelfCalls(t, "d.py", "def visit(t):\n    def descend(c):\n        return visit(c)\n    return descend(t)\n", 3)
	assertSelfCalls(t, "e.py", "def f(n): return f(n - 1)\n", 1)
	assertSelfCalls(t, "f.py", "async def poll(n):\n    await poll(n - 1)\n", 2)
}

// TestPythonSelfRecursionFollowsResolution is the negative dimension: a bare name inside a
// method reaches the module-level function, a call through another object is delegation, and
// a local binding of the name captures the call wherever in the body it appears.
func TestPythonSelfRecursionFollowsResolution(t *testing.T) {
	for name, src := range map[string]string{
		"delegation":     "class W:\n    def close(self):\n        return self.inner.close()\n",
		"other object":   "def close(r):\n    return r.close()\n",
		"method bare":    "class V:\n    def render(self):\n        return render(self.v)\n",
		"static bare":    "class V:\n    @staticmethod\n    def build(v):\n        return build(v)\n",
		"parameter":      "def apply(apply, v):\n    return apply(v)\n",
		"assignment":     "def step(v):\n    step = int\n    return step(v)\n",
		"late binding":   "def step(v):\n    if v:\n        return step(v)\n    step = int\n",
		"import":         "def load(p):\n    from json import load\n    return load(p)\n",
		"loop target":    "def run(fs):\n    for run in fs:\n        run()\n",
		"with target":    "def f(c):\n    with c as f:\n        f()\n",
		"nested def":     "def f():\n    def f():\n        return 1\n    return f()\n",
		"super":          "class B(A):\n    def save(self):\n        return super().save()\n",
		"longer name":    "class C:\n    def run(self):\n        return self.runner.run()\n",
		"string":         "def f():\n    return \"f()\"\n",
		"comment":        "def f():\n    # f()\n    return 1\n",
		"default at def": "def f(n=f):\n    return n\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "m.py", src)
		})
	}
}

// TestPythonSelfRecursionBoundaries covers the edges: a comparison or attribute assignment is
// not a binding, a header wrapped the way black formats it keeps its body, a column-0 string
// does not end the function, and a method's owner is the class it sits directly in.
func TestPythonSelfRecursionBoundaries(t *testing.T) {
	assertSelfCalls(t, "cmp.py", "def f(n):\n    if f == n:\n        return 0\n    return f(n)\n", 4)
	assertSelfCalls(t, "attr.py", "def f(n):\n    f.calls = 1\n    return f(n)\n", 3)
	assertSelfCalls(t, "wrap.py", "def walk(\n    node,\n) -> int:\n    return walk(node.next)\n", 4)
	assertSelfCalls(t, "doc.py", "def f(n):\n    s = \"\"\"\ncolumn zero\n\"\"\"\n    return f(n)\n", 5)
	assertSelfCalls(t, "own.py", "class Outer:\n    class Inner:\n        @staticmethod\n        def g():\n            return Outer.g()\n        @staticmethod\n        def h():\n            return Inner.h()\n", 8)
}

// TestPythonContinuationLinesStayInTheirFunction pins the HISS-04 half of the continuation
// fix: a function whose list literal runs at column 0 used to end at the first element, so its
// length was never measured.
func TestPythonContinuationLinesStayInTheirFunction(t *testing.T) {
	root := t.TempDir()
	src := "def build():\n    data = [\n" + strings.Repeat("1,\n", 70) + "]\n    return data\n"
	writeFixture(t, root, "c.py", src)
	rep := scanFixture(t, root, ScanOptions{MaxFuncLOC: 60})
	assertViolations(t, rep, []expectedViolation{{"HISS-04", "c.py", 1}})
}

// TestCallSiteHelpersBoundaries pins the shared matcher at its edges, since the banned-call
// rules and the recursion rule both depend on it.
func TestCallSiteHelpersBoundaries(t *testing.T) {
	if nextIdent("abc", "", 0) != -1 || containsIdent("", "f") || hasCall("", "f", isSelectorByte) {
		t.Fatal("empty inputs must never match")
	}
	for line, want := range map[string]bool{"f()": true, "f ()": true, "x.f()": false, "ff()": false, "f_x()": false, "a::f()": true, "f": false} {
		if got := hasCall(line, "f", isSelectorByte); got != want {
			t.Errorf("hasCall(%q) = %v, want %v", line, got, want)
		}
	}
	if hasCall("a::f()", "f", isPathByte) {
		t.Error("a Rust path must not reach the bare name")
	}
	for params, want := range map[string]bool{"self": true, "&self, x: u8": true, "&mut self": true, "&'a mut self": true, "mut self": true, "self: Box<Self>": true, "selfish: u8": false, "x: u8": false, "": false} {
		if got := rustHasReceiver(params); got != want {
			t.Errorf("rustHasReceiver(%q) = %v, want %v", params, got, want)
		}
	}
	if got := rustParams("pub fn f<F: Fn(u8) -> (), G>(g: F) -> u8"); got != "g: F" {
		t.Errorf("rustParams skipped the generic list wrongly: %q", got)
	}
	if got := appendSignature(strings.Repeat("x", maxSignatureBytes), "more"); len(got) != maxSignatureBytes {
		t.Errorf("signature text must be bounded at %d bytes, got %d", maxSignatureBytes, len(got))
	}
}
