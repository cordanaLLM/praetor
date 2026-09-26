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

// TestRustSelfRecursionShadowsLexically is the positive dimension of Rust shadowing: a local
// of the function's name hides it only inside its lexical scope, so a call before a let, in
// its own initializer, or after the block, loop, arm or closure that bound the name still
// re-enters the function. A name followed by a parenthesis is a call, not a pattern.
func TestRustSelfRecursionShadowsLexically(t *testing.T) {
	for name, tc := range map[string]struct {
		src  string
		want []int
	}{
		"call before let":      {"fn f(n: u32) {\n    if n > 0 {\n        f(n - 1);\n    }\n    let f = 1;\n}\n", []int{3}},
		"one-line body":        {"fn f(n: u32) -> u32 { if n > 0 { return f(n - 1); } let f = 3; f }\n", []int{1}},
		"let initializer":      {"fn f(n: u32) -> u32 {\n    let f = f(n - 1);\n    f\n}\n", []int{2}},
		"after closure param":  {"fn f(n: u32) -> u32 {\n    let g = |f: u32| f + 1;\n    f(n - 1)\n}\n", []int{3}},
		"after closure block":  {"fn f(n: u32) -> u32 {\n    let g = |f: u32| {\n        f + 1\n    };\n    f(n - 1)\n}\n", []int{5}},
		"after inner block":    {"fn f(n: u32) -> u32 {\n    {\n        let f = 1;\n    }\n    f(n - 1)\n}\n", []int{5}},
		"after match arm":      {"fn f(n: u32) -> u32 {\n    match n {\n        f => f + 1,\n    };\n    f(n - 1)\n}\n", []int{5}},
		"after for loop":       {"fn f(n: u32) -> u32 {\n    for f in 0..n {\n        let _ = f;\n    }\n    f(n - 1)\n}\n", []int{5}},
		"after if let":         {"fn f(n: u32) -> u32 {\n    if let Some(f) = g(n) {\n        return f;\n    }\n    f(n - 1)\n}\n", []int{5}},
		"else of if let":       {"fn f(o: Option<u32>) -> u32 {\n    if let Some(f) = o {\n        f\n    } else {\n        f(None)\n    }\n}\n", []int{5}},
		"for iterator":         {"fn f(n: u32) -> u32 {\n    for f in f(n - 1) {\n    }\n    0\n}\n", []int{2}},
		"match scrutinee":      {"fn f(n: u32) -> u32 {\n    let r = match f(n - 1) { 0 => 1, _ => 2 };\n    r\n}\n", []int{2}},
		"between pipes":        {"fn f(n: u32) -> u32 {\n    1 | f(n - 1) | 2\n}\n", []int{2}},
		"closure then call":    {"fn f(n: u32) {\n    f(n - 1);\n    let f = |x: u32| x;\n    f(n);\n}\n", []int{2}},
		"guard and arm body":   {"fn f(t: &T) -> bool {\n    match t {\n        T::A(x) if f(x) => true,\n        T::B(x) => f(x),\n        _ => false,\n    }\n}\n", []int{3, 4}},
		"std expression macro": {"fn f(n: u32) -> u32 {\n    assert!(f(n - 1) > 0);\n    println!(\"{}\", f(n - 2));\n    vec![f(n - 3)].len() as u32\n}\n", []int{2, 3, 4}},
		"after macro input":    {"fn f(n: u32) -> u32 {\n    let r = check!(n);\n    r + f(n - 1)\n}\n", []int{3}},
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "src/lib.rs", tc.src, tc.want...)
		})
	}
}

// TestRustScopedBindingsShadow is the negative dimension: while a local of the name is in
// scope, a bare call reaches the local. A use covers the whole body, including a call above
// it, and a closure or arm pattern that binds the name inside brackets still shadows it.
func TestRustScopedBindingsShadow(t *testing.T) {
	for name, src := range map[string]string{
		"use above":         "fn f() {\n    f();\n    use other::f;\n}\n",
		"let closure":       "fn f(n: u32) -> u32 {\n    let f = |x: u32| x;\n    f(n)\n}\n",
		"let tuple":         "fn f(p: (u32, fn(u32) -> u32)) -> u32 {\n    let (n, f) = p;\n    f(n)\n}\n",
		"let array length":  "fn f(n: u32) -> u32 {\n    let [f, _] = [g; 2];\n    f(n)\n}\n",
		"closure tuple":     "fn f(v: &[(u32, fn(u32) -> u32)]) -> u32 {\n    v.iter().map(|(n, f)| f(*n)).sum()\n}\n",
		"closure block":     "fn f(v: &[fn()]) {\n    v.iter().for_each(|f| {\n        f();\n    });\n}\n",
		"for body":          "fn f(fs: &[fn()]) {\n    for f in fs {\n        f();\n    }\n}\n",
		"if let body":       "fn f(o: Option<fn()>) {\n    if let Some(f) = o {\n        f();\n    }\n}\n",
		"while let body":    "fn f(v: &mut Vec<fn()>) {\n    while let Some(f) = v.pop() {\n        f();\n    }\n}\n",
		"let chain":         "fn f(a: bool, o: Option<fn()>) {\n    if a && let Some(f) = o {\n        f();\n    }\n}\n",
		"arm block":         "fn f(o: Option<fn()>) {\n    match o {\n        Some(f) => {\n            f();\n        }\n        None => {}\n    }\n}\n",
		"arm on its line":   "fn f(o: Option<fn() -> u8>) -> u8 {\n    match o { Some(f) => f(), None => 0 }\n}\n",
		"let else":          "fn f(o: Option<fn()>) {\n    let Some(f) = o else { return; };\n    f();\n}\n",
		"shadow until end":  "fn f(n: u32) -> u32 {\n    let f = |x: u32| x;\n    {\n        f(n);\n    }\n    f(n)\n}\n",
		"iterator closure":  "fn f(fs: &[fn()]) {\n    for f in fs.iter().map(|x| { x }) {\n        f();\n    }\n}\n",
		"arm pattern macro": "fn recv() {\n    select! {\n        recv(r) -> v => assert_eq!(v, Ok(7)),\n    }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "src/lib.rs", src)
		})
	}
}

// TestRustMacroInputIsUndecided pins the macro rule: a macro may rewrite its input, so a call
// written inside the input of any macro but a standard expression macro is not decided. The
// input ends at the delimiter matching the one that opened it, across lines.
func TestRustMacroInputIsUndecided(t *testing.T) {
	for name, src := range map[string]string{
		"syscall":           "pub fn recv(fd: i32) -> i32 {\n    syscall!(recv(fd, 0))\n}\n",
		"wrapped syscall":   "pub fn send(fd: i32) -> i32 {\n    let r = syscall!(\n        send(\n            fd,\n        )\n    );\n    r\n}\n",
		"path macro":        "fn debug(d: &D) {\n    tracing::debug!(x = debug(&d));\n}\n",
		"bracket macro":     "fn f(n: u32) -> u32 {\n    wrap![f(n - 1)]\n}\n",
		"brace macro":       "fn f(n: u32) -> u32 {\n    wrap! {\n        f(n - 1)\n    }\n}\n",
		"method in macro":   "impl S {\n    fn m(&self) -> u8 {\n        ok!(self.m())\n    }\n}\n",
		"nested std inside": "fn f(n: u32) -> u32 {\n    wrap!(vec![f(n - 1)])\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "src/lib.rs", src)
		})
	}
	// Boundaries: a comparison or negation is not a macro, and a call after the input closes
	// on the same line is decided again.
	assertSelfCalls(t, "src/ne.rs", "fn f(n: u32) -> bool {\n    n != 0 && !f(n - 1)\n}\n", 2)
	assertSelfCalls(t, "src/close.rs", "fn f(n: u32) -> u32 {\n    wrap!(n); f(n - 1)\n}\n", 2)
	assertSelfCalls(t, "src/std.rs", "fn f(n: u32) -> u32 {\n    std::assert_eq!(f(n - 1), 0);\n    0\n}\n", 2)
}

// TestRustTraitImplHeaderForms covers impl and trait headers the line-anchored match used to
// misread: an attribute on the header's line, a brace in a const generic argument, a
// semicolon in an array type, and a trait alias that ends in a semicolon and opens no body.
func TestRustTraitImplHeaderForms(t *testing.T) {
	for name, src := range map[string]string{
		"attribute":       "#[allow(unused)] impl Ones for W {\n    fn count(&self) -> u32 {\n        self.count()\n    }\n}\n",
		"const generic":   "impl Ones for W<{ N + 1 }> {\n    fn count(&self) -> u32 {\n        self.count()\n    }\n}\n",
		"array type":      "impl<const N: usize> Ones for [u8; N] {\n    fn count(&self) -> u32 {\n        self.count()\n    }\n}\n",
		"after alias":     "trait Alias = Foo + Bar;\nimpl Display for W {\n    fn fmt(&self) -> u32 {\n        self.fmt()\n    }\n}\n",
		"wrapped generic": "impl Ones\n    for W<{ N }>\n{\n    fn count(&self) -> u32 {\n        self.count()\n    }\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			assertSelfCalls(t, "src/lib.rs", src)
		})
	}
	// An inherent impl with the same forms stays decided, and so does the free function after
	// an alias.
	assertSelfCalls(t, "src/inherent.rs", "#[allow(unused)] impl W<{ N + 1 }> {\n    fn count(&self) -> u32 {\n        self.count()\n    }\n}\n", 3)
	assertSelfCalls(t, "src/alias.rs", "trait Alias = Foo;\nfn walk(n: u32) {\n    walk(n)\n}\n", 3)
}

// TestRustBindingAt pins how each occurrence of the name is classified, and which keyword its
// scope is measured from.
func TestRustBindingAt(t *testing.T) {
	for code, want := range map[string]struct {
		kind    rustBindKind
		keyword int
	}{
		"let f = 1;":              {rustLetBinding, 0},
		"let a = 1; let f = 2;":   {rustLetBinding, 11},
		"if let Some(f) = o {":    {rustHeadBinding, 3},
		"while let Some(f) = o {": {rustHeadBinding, 6},
		"a && let Some(f) = o {":  {rustHeadBinding, 5},
		"for (i, f) in v {":       {rustHeadBinding, 0},
		"let g = |a, f| a;":       {rustClosureBinding, -1},
		"Some(f) => 1,":           {rustArmBinding, -1},
		"x = f(1);":               {rustNoBinding, -1},
		"let r = match f(n) {":    {rustNoBinding, -1},
		"f!(x) => 1,":             {rustNoBinding, -1},
		"f::g() => 1,":            {rustNoBinding, -1},
		"a => f,":                 {rustNoBinding, -1},
		"return f;":               {rustNoBinding, -1},
		"let x = y; f":            {rustNoBinding, -1},
		"for x in v { f }":        {rustNoBinding, -1},
		"let g = |a| a; f":        {rustNoBinding, -1},
		"deliver(f) if f.ok() =>": {rustArmBinding, -1},
	} {
		at := nextIdent(code, "f", 0)
		kind, keyword := rustBindingAt(code, at)
		if kind != want.kind || keyword != want.keyword {
			t.Errorf("rustBindingAt(%q) = %d, %d; want %d, %d", code, kind, keyword, want.kind, want.keyword)
		}
	}
}

// TestRustWalkBounds pins the walk's bounds: pending bindings stop at maxRustPendingBindings,
// and a closure binding whose pipe was on an earlier line is dropped.
func TestRustWalkBounds(t *testing.T) {
	var w rustWalk
	for i := 0; i < maxRustPendingBindings+4; i++ {
		w.bind("let f = 1;", 4, rustLetBinding, 0)
	}
	if len(w.pending) != maxRustPendingBindings {
		t.Fatalf("pending bindings must be bounded at %d, got %d", maxRustPendingBindings, len(w.pending))
	}
	w = rustWalk{}
	w.bind("|f| f", 1, rustClosureBinding, -1)
	w.bind("let f", 4, rustNoBinding, -1)
	if len(w.pending) != 1 || w.pending[0].pipe != 2 {
		t.Fatalf("a closure binding must wait for its closing pipe, got %+v", w.pending)
	}
	w.startLine()
	if len(w.pending) != 0 {
		t.Fatalf("a closure binding must not outlive its line, got %+v", w.pending)
	}
}

// TestRustCallTextDropsArmPatterns pins which part of a line may hold a call.
func TestRustCallTextDropsArmPatterns(t *testing.T) {
	for code, want := range map[string]string{
		"recv(r) -> v => go(v),":         "=> go(v),",
		"A(x) if f(x) => 1,":             "if f(x) => 1,",
		"let r = match f(n) { 0 => 1 };": "let r = match f(n) { 0 => 1 };",
		"match f(n) { A => 1, _ => 2 }":  "match f(n) { A => 1, _ => 2 }",
		"x = f(n); y => z":               "x = f(n); y => z",
		"S { a, .. } => f(a),":           "S { a, .. } => f(a),",
		"f(n)":                           "f(n)",
		"":                               "",
	} {
		if got := rustCallText(code); got != want {
			t.Errorf("rustCallText(%q) = %q, want %q", code, got, want)
		}
	}
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
		// Inside `impl Trait for T` an inherent T::f outranks the trait method, and Self::f
		// picks among T's impls by argument type; rustc compiles both of these clean with
		// -D unconditional_recursion.
		"trait impl to inherent": "impl W {\n    fn count_ones(&self) -> u32 {\n        self.0.count_ones()\n    }\n}\nimpl Ones for W {\n    fn count_ones(&self) -> u32 {\n        self.count_ones()\n    }\n}\n",
		"trait impl cross From":  "impl From<A> for E {\n    fn from(_a: A) -> Self {\n        Self::from(B)\n    }\n}\n",
		"trait impl wrapped":     "impl<T: Copy> Ones\n    for W<T>\nwhere\n    T: Default,\n{\n    fn count_ones(&self) -> u32 {\n        self.count_ones()\n    }\n}\n",
		"unsafe trait impl":      "unsafe impl<T> Sync for W<T> {\n    fn sync(&self) {\n        self.sync()\n    }\n}\n",
		"nested in trait impl":   "impl Drop for W {\n    fn drop(&mut self) {\n        if self.0 {\n            Self::drop(self);\n        }\n    }\n}\n",
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
	// A for inside the generic list or a where clause is a higher-ranked bound, not a trait
	// impl, so these inherent impls stay decided.
	assertSelfCalls(t, "src/h.rs", "impl<F: for<'a> Fn(&'a u8)> Wrap<F> {\n    fn run(&self) {\n        self.run()\n    }\n}\n", 3)
	assertSelfCalls(t, "src/wh.rs", "impl<F> Wrap<F>\nwhere\n    F: for<'a> Fn(&'a u8),\n{\n    fn run(&self) {\n        self.run()\n    }\n}\n", 6)
	// A trait impl's scope ends at its closing brace: the inherent impl after it is decided.
	assertSelfCalls(t, "src/after.rs", "impl Default for S {\n    fn default() -> Self {\n        Self::default()\n    }\n}\nimpl S {\n    fn walk(&self) {\n        self.walk()\n    }\n}\n", 8)
	// The recorded gap: real recursion inside a trait impl is not reported, because only the
	// absence of an inherent next anywhere in the crate makes it recursion.
	assertSelfCalls(t, "src/gap.rs", "impl Iterator for C {\n    type Item = u32;\n    fn next(&mut self) -> Option<u32> {\n        self.next()\n    }\n}\n")

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
		// The string continues past its backslash, so load closes after it and the call in
		// check is not load's.
		"backslash string": "def load(path):\n    raise ValueError('bad \\\n        \"{0}\"'.format(path))\n\ndef check():\n    return load(\"x\")\n",
		// A misread bracket resets at the next statement-only line, so g still ends before
		// the module-level call.
		"statement resets depth": "def g(d):\n    x = f\"{d[\"(\"]}\"\n    return x\ny = g(1)\n",
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

// TestPythonSelfRecursionScopesBindings pins Python's scope rule across nested defs: a
// parameter, assignment or def in a nested def is that def's local, so it shadows the name for
// the nested def's calls and never for the enclosing function's own. A class body binds in
// its namespace, which no function reads, and a method's receiver call is not a bare name.
func TestPythonSelfRecursionScopesBindings(t *testing.T) {
	assertSelfCalls(t, "param.py", "def f(n):\n    def g(f):\n        return f(1)\n    return f(n - 1)\n", 4)
	assertSelfCalls(t, "assign.py", "def f(n):\n    def g():\n        f = 1\n        return f()\n    return f(n - 1)\n", 5)
	assertSelfCalls(t, "inner.py", "def f(n):\n    def g():\n        def f():\n            return 1\n        return f()\n    return f(n - 1)\n", 6)
	assertSelfCalls(t, "late.py", "def f(n):\n    def g():\n        return f()\n        f = 2\n    return g\n")
	assertSelfCalls(t, "deep.py", "def f(n):\n    def g():\n        def h():\n            return f(n)\n        return h\n    return g\n", 4)
	assertSelfCalls(t, "between.py", "def f(n):\n    def g(f):\n        def h():\n            return f(n)\n        return h\n    return g\n")
	assertSelfCalls(t, "method.py", "def f():\n    class C:\n        def m(self):\n            return f()\n    return C\n", 4)
	assertSelfCalls(t, "classbody.py", "def f(n):\n    class C:\n        f = 1\n    return f(n)\n", 4)
	assertSelfCalls(t, "receiver.py", "class K:\n    def m(self):\n        def g(m):\n            return self.m()\n        return g\n", 4)
}

// TestPythonRelayedCallsAreBounded pins the relay of nested-scope calls at its bound.
func TestPythonRelayedCallsAreBounded(t *testing.T) {
	var src strings.Builder
	src.WriteString("def f(n):\n    def g():\n")
	for i := 0; i < maxRelayedCalls+8; i++ {
		src.WriteString("        f(n)\n")
	}
	src.WriteString("    return g\n")
	got, _ := selfCallLines(t, "relay.py", src.String())
	if len(got) != maxSelfCallSites {
		t.Fatalf("relayed call sites must be bounded at %d, got %d", maxSelfCallSites, len(got))
	}
	if relayed := appendRelayed(make([]relayedCall, maxRelayedCalls), relayedCall{}); len(relayed) != maxRelayedCalls {
		t.Fatalf("a full relay must not grow past %d, got %d", maxRelayedCalls, len(relayed))
	}
}

// TestPythonStringAndBracketRecovery covers the edges of continuation tracking: an escaped
// quote does not close a triple-quoted string, a def resets a misread bracket depth, and a
// line that an expression may legitimately start with (for, async for) is not a reset.
func TestPythonStringAndBracketRecovery(t *testing.T) {
	assertSelfCalls(t, "tq.py", "def pattern():\n    return r\"\"\"say \\\"\"\" twice\"\"\"\n\ndef walk(n):\n    return walk(n - 1)\n", 5)
	assertSelfCalls(t, "fs.py", "def label(d):\n    return f\"{d[\"(\"]}\"\n\ndef walk(n):\n    return walk(n - 1)\n", 5)
	assertSelfCalls(t, "crlf.py", "def load(p):\r\n    raise E('a \\\r\n        b'.format(p))\r\n\r\ndef check():\r\n    return load(1)\r\n")
	assertSelfCalls(t, "comp.py", "def f(xs):\n    ys = [x\nfor x in xs]\n    return f(ys)\n", 4)
	assertSelfCalls(t, "acomp.py", "async def f(xs):\n    ys = [x\nasync for x in xs]\n    return await f(ys)\n", 4)
}

// TestLiteralStripperCarriesEscapedLineBreaks pins the stripper at the line break: a trailing
// backslash carries a quoted string onto the next line in Python and C alike, a single-quoted
// string without one ends with its line, and a block comment honours no escapes.
func TestLiteralStripperCarriesEscapedLineBreaks(t *testing.T) {
	for name, tc := range map[string]struct {
		syn   literalSyntax
		lines []string
		want  []string
	}{
		"python carried":       {pythonSyntax, []string{"x = 'a \\", "b' + f(", ")"}, []string{"x = ", " + f(", ")"}},
		"python crlf carried":  {pythonSyntax, []string{"x = 'a \\\r", "b' + f(\r"}, []string{"x = ", " + f(\r"}},
		"python unterminated":  {pythonSyntax, []string{"x = 'abc", "y = f("}, []string{"x = ", "y = f("}},
		"python escaped fence": {pythonSyntax, []string{"s = \"\"\"a \\\"\"\" b\"\"\"", "f("}, []string{"s = ", "f("}},
		"triple spans lines":   {pythonSyntax, []string{"s = '''a \\", "b''' + g("}, []string{"s = ", " + g("}},
		"c carried":            {cLikeSyntax, []string{"char *s = \"a\\", "b\"; f();"}, []string{"char *s = ", "; f();"}},
		"c block no escape":    {cLikeSyntax, []string{"/* a \\*/ b"}, []string{" b"}},
		"empty line":           {pythonSyntax, []string{""}, []string{""}},
	} {
		t.Run(name, func(t *testing.T) {
			s := &literalStripper{syn: tc.syn}
			for i, line := range tc.lines {
				if got := s.strip(line); got != tc.want[i] {
					t.Fatalf("line %d: strip(%q) = %q, want %q", i, line, got, tc.want[i])
				}
			}
			if s.fence != "" {
				t.Fatalf("fence %q left open after the last line", s.fence)
			}
		})
	}
}

// TestRustHeaderKind pins the classification that decides whether a method's self-call is
// judged: only a top-level for outside the generic list and before any where clause makes an
// impl a trait impl.
func TestRustHeaderKind(t *testing.T) {
	for header, want := range map[string]rustBodyKind{
		"impl W":                           rustInherentBody,
		"impl<T> W<T>":                     rustInherentBody,
		"impl<F: for<'a> Fn(&'a u8)> W<F>": rustInherentBody,
		"impl<F> W<F> where F: for<'a> Fn(&'a u8)": rustInherentBody,
		"impl dyn Any":                             rustInherentBody,
		"impl Ones for W":                          rustTraitImplBody,
		"impl<T> From<T> for W":                    rustTraitImplBody,
		"unsafe impl Send for W":                   rustTraitImplBody,
		"impl !Send for W":                         rustTraitImplBody,
		"impl<F: Fn() -> u8> Tr for W<F>":          rustTraitImplBody,
		"impl Foo for for<'a> fn(&'a u8)":          rustTraitImplBody,
		"impl<T> Tr<T>     for W<T> where T: Copy": rustTraitImplBody,
		"pub trait T":                              rustTraitBody,
		"pub(crate) unsafe trait T: Send":          rustTraitBody,
		"fn f()":                                   rustFreeBody,
		"":                                         rustFreeBody,
	} {
		if got := rustHeaderKind(header); got != want {
			t.Errorf("rustHeaderKind(%q) = %d, want %d", header, got, want)
		}
	}
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

// TestIdentifierHelpersBoundaries pins the position-level helpers the Rust walk places calls
// and bindings with: a call at an exact byte, a whole identifier at a byte, the last and
// trailing identifier, and a word ending a text.
func TestIdentifierHelpersBoundaries(t *testing.T) {
	for at, want := range map[int]bool{0: true, 5: false, 11: false, 16: false, 17: true} {
		if got := callAt("f(1) ff(2) f_x() f (3)", at, "f", isPathByte); got != want {
			t.Errorf("callAt(%d) = %v, want %v", at, got, want)
		}
	}
	if callAt("a::f()", 3, "f", isPathByte) || !callAt("a::f()", 3, "f", isSelectorByte) {
		t.Error("callAt must honour the excluded preceding byte")
	}
	if identAt("abc", 0, "") || !identAt("f", 0, "f") || identAt("xf", 1, "f") || identAt("fx", 0, "f") {
		t.Error("identAt must require a whole identifier")
	}
	if endsWithWord("elif", "if") || !endsWithWord("} else if", "if") || endsWithWord("", "if") {
		t.Error("endsWithWord must require a whole trailing word")
	}
	if lastIdent("let a; let b", "let") != 7 || lastIdent("outlet", "let") != -1 || trailingIdent("a::b_c") != "b_c" || trailingIdent("") != "" {
		t.Error("lastIdent and trailingIdent disagree with their contract")
	}
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
