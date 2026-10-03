package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// net/http documents panic(http.ErrAbortHandler) as the way a handler aborts its response
// (issue #331). The abort policy accepts exactly that panic, with the sentinel resolved
// through the file's imports, and keeps reporting every other panic.

func TestAbortPolicy_Negative_GoHTTPAbortSentinelThroughEveryImportForm(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lib/guard.go", strings.Join([]string{
		"package lib", "",
		"import (", "\t\"errors\"", "\t\"net/http\"", ")", "",
		"func Guard(next http.Handler) http.Handler {",
		"\treturn http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {",
		"\t\tdefer func() {",
		"\t\t\tv := recover()",
		"\t\t\tif err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {",
		"\t\t\t\tpanic(http.ErrAbortHandler)",
		"\t\t\t}",
		"\t\t}()",
		"\t\tnext.ServeHTTP(w, r)",
		"\t})",
		"}", "",
		"func Abort() { panic(http.ErrAbortHandler) }",
		"func Parens() { (panic)((http.ErrAbortHandler)) }",
		"",
		"var sentinel = http.ErrAbortHandler",
		"",
	}, "\n"))
	writeFixture(t, root, "lib/alias.go", "package lib\n\nimport web \"net/http\"\n\nfunc Alias() {\n\tpanic(web.ErrAbortHandler)\n}\n")
	writeFixture(t, root, "lib/dot.go", "package lib\n\nimport . \"net/http\"\n\nfunc Dot() {\n\tpanic(ErrAbortHandler)\n}\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("panic(http.ErrAbortHandler) through a plain, aliased or dot import must stay silent: %+v", rep.Violations)
	}
}

func TestAbortPolicy_Positive_GoPanicsThatAreNotTheHTTPSentinel(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lib/values.go", strings.Join([]string{
		"package lib",    // 1
		"",               // 2
		"import (",       // 3
		"\t\"errors\"",   // 4
		"\t\"fmt\"",      // 5
		"\t\"net/http\"", // 6
		")",              // 7
		"",               // 8
		"var ErrAbortHandler = errors.New(\"package lookalike\")", // 9
		"",                                     // 10
		"func Literal() { panic(\"boom\") }",   // 11
		"func Value(err error) { panic(err) }", // 12
		"func Package() { panic(ErrAbortHandler) }",                             // 13
		"func Unrelated() { panic(http.ErrServerClosed) }",                      // 14
		"func Wrapped() { panic(fmt.Errorf(\"x: %w\", http.ErrAbortHandler)) }", // 15
		"func Local() {", // 16
		"\tErrAbortHandler := errors.New(\"local lookalike\")", // 17
		"\tpanic(ErrAbortHandler)",                             // 18
		"}",                                                    // 19
		"func Rethrow() {",                                     // 20
		"\tif v := recover(); v != nil {",                      // 21
		"\t\tpanic(v)",                                         // 22
		"\t}",                                                  // 23
		"}",                                                    // 24
		"",
	}, "\n"))
	writeFixture(t, root, "lib/foreign.go", "package lib\n\nimport http \"example.com/fake/http\"\n\nfunc Foreign() {\n\tpanic(http.ErrAbortHandler)\n}\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "lib/values.go", 11}, {"HISS-07", "lib/values.go", 12}, {"HISS-07", "lib/values.go", 13},
		{"HISS-07", "lib/values.go", 14}, {"HISS-07", "lib/values.go", 15}, {"HISS-07", "lib/values.go", 18},
		{"HISS-07", "lib/values.go", 22}, {"HISS-07", "lib/foreign.go", 6},
	})
}

func TestAbortPolicy_Boundary_GoHTTPSentinelShadowsAndArity(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "lib/edges.go", strings.Join([]string{
		"package lib",         // 1
		"",                    // 2
		"import \"net/http\"", // 3
		"",                    // 4
		"type carrier struct{ ErrAbortHandler error }", // 5
		"", // 6
		"func Shadowed(http carrier) { panic(http.ErrAbortHandler) }", // 7
		"func Unshadowed(c carrier) { panic(http.ErrAbortHandler) }",  // 8
		"func Two() { panic(http.ErrAbortHandler, nil) }",             // 9
		"func Spread() { panic(http.ErrAbortHandler...) }",            // 10
		"func Address() { panic(&http.ErrAbortHandler) }",             // 11
		"func Empty() { panic() }",                                    // 12
		"",
	}, "\n"))
	writeFixture(t, root, "lib/dotshadow.go", strings.Join([]string{
		"package lib",           // 1
		"",                      // 2
		"import . \"net/http\"", // 3
		"",                      // 4
		"func DotShadowed(ErrAbortHandler error) {", // 5
		"\tpanic(ErrAbortHandler)",                  // 6
		"}",                                         // 7
		"",
	}, "\n"))
	writeFixture(t, root, "lib/sentinel_test.go", "package lib\n\nfunc helper() { panic(\"tests may abort\") }\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-07", "lib/edges.go", 7}, {"HISS-07", "lib/edges.go", 9}, {"HISS-07", "lib/edges.go", 10},
		{"HISS-07", "lib/edges.go", 11}, {"HISS-07", "lib/edges.go", 12}, {"HISS-07", "lib/dotshadow.go", 6},
	})
}

// firstPanic parses src and returns its imports, the first function declaration and the
// first call expression in it, the inputs AbortsHTTPResponse takes.
func firstPanic(t *testing.T, src string) (GoImports, *ast.FuncDecl, *ast.CallExpr) {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "f.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var fn *ast.FuncDecl
	var call *ast.CallExpr
	ast.Inspect(file, func(n ast.Node) bool {
		if decl, ok := n.(*ast.FuncDecl); ok && fn == nil {
			fn = decl
		}
		if expr, ok := n.(*ast.CallExpr); ok && call == nil {
			call = expr
		}
		return call == nil
	})
	if fn == nil || call == nil {
		t.Fatalf("fixture holds no function or call: %q", src)
	}
	return FileImports(file), fn, call
}

// Positive, negative and boundary: a binding shadows the package name only where it is in
// scope at the panic. A local of another block, one declared after the panic and a function
// literal's parameter leave http.ErrAbortHandler the sentinel; a local declared before the
// panic in an enclosing block is what the selector reads (#733).
func TestAbortsHTTPResponse_ShadowFollowsScope(t *testing.T) {
	for src, want := range map[string]bool{
		"func F(b bool) { if b { http := 1; _ = http }; panic(http.ErrAbortHandler) }":                       true,
		"func F() { panic(http.ErrAbortHandler); http := 1; _ = http }":                                      true,
		"func F() { _ = func(http int) {}; panic(http.ErrAbortHandler) }":                                    true,
		"func F(b bool) { http := struct{ ErrAbortHandler error }{}; if b { panic(http.ErrAbortHandler) } }": false,
	} {
		im, fn, call := firstPanic(t, "package p\n\nimport \"net/http\"\n\n"+src+"\n")
		if got := AbortsHTTPResponse(im, fn, call); got != want {
			t.Errorf("AbortsHTTPResponse = %v, want %v for %s", got, want, src)
		}
	}
}

func TestAbortsHTTPResponse_Positive_ExactSentinel(t *testing.T) {
	im, fn, call := firstPanic(t, "package p\n\nimport h \"net/http\"\n\nfunc F() { panic(h.ErrAbortHandler) }\n")
	if !AbortsHTTPResponse(im, fn, call) {
		t.Fatal("panic(h.ErrAbortHandler) with h bound to net/http must be the sentinel")
	}
	if !AbortsHTTPResponse(im, nil, call) {
		t.Fatal("a call outside any function declaration has no binding to shadow the package")
	}
}

func TestAbortsHTTPResponse_Negative_NotAPanicOrNotTheSentinel(t *testing.T) {
	cases := map[string]string{
		"other callee":   "package p\n\nimport \"net/http\"\n\nfunc F() { println(http.ErrAbortHandler) }\n",
		"other package":  "package p\n\nimport http \"example.com/fake/http\"\n\nfunc F() { panic(http.ErrAbortHandler) }\n",
		"no dot import":  "package p\n\nfunc F() { panic(ErrAbortHandler) }\n",
		"blank import":   "package p\n\nimport _ \"net/http\"\n\nfunc F() { panic(ErrAbortHandler) }\n",
		"other sentinel": "package p\n\nimport \"net/http\"\n\nfunc F() { panic(http.ErrHandlerTimeout) }\n",
	}
	for name, src := range cases {
		im, fn, call := firstPanic(t, src)
		if AbortsHTTPResponse(im, fn, call) {
			t.Errorf("%s: must not be accepted as the net/http abort sentinel", name)
		}
	}
	if AbortsHTTPResponse(GoImports{}, nil, nil) {
		t.Error("a nil call is not a panic")
	}
}

func TestAbortsHTTPResponse_Boundary_ShadowAndArity(t *testing.T) {
	im, fn, call := firstPanic(t, "package p\n\nimport \"net/http\"\n\nfunc F() { http := struct{ ErrAbortHandler error }{}; panic(http.ErrAbortHandler) }\n")
	if AbortsHTTPResponse(im, fn, call) {
		t.Error("a local named http shadows the package, so its field is not the sentinel")
	}
	if !AbortsHTTPResponse(im, nil, call) {
		t.Error("without the enclosing function no shadow is visible and the import decides")
	}
	im, fn, call = firstPanic(t, "package p\n\nimport \"net/http\"\n\nfunc F() { panic(http.ErrAbortHandler, http.ErrAbortHandler) }\n")
	if AbortsHTTPResponse(im, fn, call) {
		t.Error("a second argument means the call is not the single-argument sentinel panic")
	}
}
