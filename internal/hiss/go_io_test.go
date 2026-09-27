package hiss

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The I/O half of HISS-02 for Go is pinned in three dimensions: the deadline-free contexts
// and context-less calls that must be reported, the bounded and exempt code that must stay
// silent, and the scope and entry-point edges between them.

// scanGoIO scans one Go file and returns its report.
func scanGoIO(t *testing.T, rel, src string) *ScanReport {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, rel, src)
	return scanFixture(t, root, ScanOptions{})
}

func TestGoIO_Positive_DeadlineFreeContextReachesACall(t *testing.T) {
	src := strings.Join([]string{
		"package p", // 1
		"",          // 2
		"import (",  // 3
		"\t\"context\"",
		"\t\"os/signal\"",
		")",                             // 6
		"",                              // 7
		"func F(ctx context.Context) {", // 8
		"\tuse(context.Background())",   // 9 root
		"\tuse(context.TODO())",         // 10 root
		"\tc, cancel := context.WithCancel(context.Background())", // 11 derivation, silent
		"\tdefer cancel()",                    // 12
		"\tuse(c)",                            // 13 inherits the root
		"\tv := context.WithValue(c, key, 1)", // 14
		"\tuse(v)",                            // 15 inherits through two steps
		"\ts, stop := signal.NotifyContext(context.TODO())", // 16
		"\tdefer stop()",                               // 17
		"\tuse(s)",                                     // 18 NotifyContext inherits
		"\tuse(context.WithoutCancel(ctx))",            // 19 strips the caller's deadline
		"\tvar w = context.Background()",               // 20
		"\tuse(w)",                                     // 21 var declaration
		"\tgo func() { use(w) }()",                     // 22 captured by a closure
		"\tuse2(context.Background(), context.TODO())", // 23 one finding per call
		"}", // 24
		"",
	}, "\n")
	rep := scanGoIO(t, "p.go", src)
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "p.go", 9}, {"HISS-02", "p.go", 10}, {"HISS-02", "p.go", 13},
		{"HISS-02", "p.go", 15}, {"HISS-02", "p.go", 18}, {"HISS-02", "p.go", 19},
		{"HISS-02", "p.go", 21}, {"HISS-02", "p.go", 22}, {"HISS-02", "p.go", 23},
	})
	for _, v := range rep.Violations {
		if !strings.Contains(v.Message, "context.WithTimeout or context.WithDeadline") {
			t.Errorf("line %d message names no fix: %q", v.LineNumber, v.Message)
		}
	}
}

func TestGoIO_Positive_ContextlessStandardLibraryCalls(t *testing.T) {
	src := strings.Join([]string{
		"package p", // 1
		"",          // 2
		"import (",  // 3
		"\t\"net\"",
		"\th \"net/http\"",
		"\t. \"os/exec\"",
		"\tstdexec \"os/exec\"",
		")",                                    // 8
		"",                                     // 9
		"func F() {",                           // 10
		"\tCommand(\"git\")",                   // 11 dot import
		"\tstdexec.Command(\"git\")",           // 12 alias
		"\tnet.Dial(\"tcp\", \"x\")",           // 13
		"\tnet.DialTCP(\"tcp\", nil, nil)",     // 14
		"\tnet.DialUDP(\"udp\", nil, nil)",     // 15
		"\tnet.DialIP(\"ip4:1\", nil, nil)",    // 16
		"\t(net.DialUnix)(\"unix\", nil, nil)", // 17 parenthesised callee
		"\th.Get(\"x\")",                       // 18 alias of net/http
		"\th.Head(\"x\")",                      // 19
		"\th.Post(\"x\", \"\", nil)",           // 20
		"\th.PostForm(\"x\", nil)",             // 21
		"\th.NewRequest(\"GET\", \"x\", nil)",  // 22
		"\tgo func() { _, _ = net.Dial(\"tcp\", \"y\") }()", // 23 inside a closure
		"}", // 24
		"",
	}, "\n")
	rep := scanGoIO(t, "p.go", src)
	want := []expectedViolation{{"HISS-07", "p.go", 23}}
	for line := 11; line <= 23; line++ {
		want = append(want, expectedViolation{"HISS-02", "p.go", line})
	}
	assertViolations(t, rep, want)
	messages := map[int]string{
		11: "exec.Command takes no context, so no deadline bounds it; use exec.CommandContext",
		13: "net.Dial takes no context, so no deadline bounds it; use net.Dialer.DialContext",
		18: "http.Get takes no context, so no deadline bounds it; use http.NewRequestWithContext",
	}
	for _, v := range rep.Violations {
		if msg, pinned := messages[v.LineNumber]; pinned && v.RuleID == "HISS-02" && v.Message != msg {
			t.Errorf("line %d message = %q, want %q", v.LineNumber, v.Message, msg)
		}
	}
}

// parseCallee parses expr as the callee of a call in a file with the given import block.
func parseCallee(t *testing.T, imports, expr string) (GoImports, ast.Expr) {
	t.Helper()
	src := "package p\n\n" + imports + "\n\nvar _ = " + expr + "()\n"
	file, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	decl, ok := file.Decls[len(file.Decls)-1].(*ast.GenDecl)
	if !ok {
		t.Fatalf("parse %q: no var declaration", expr)
	}
	spec, ok := decl.Specs[0].(*ast.ValueSpec)
	if !ok {
		t.Fatalf("parse %q: no value spec", expr)
	}
	call, ok := spec.Values[0].(*ast.CallExpr)
	if !ok {
		t.Fatalf("parse %q: no call", expr)
	}
	return FileImports(file), call.Fun
}

func TestResolveContextlessCall_Positive_NamesTheCallAndItsReplacement(t *testing.T) {
	cases := []struct {
		imports, callee string
		want            ContextlessCall
	}{
		{`import "os/exec"`, "exec.Command", ContextlessCall{"exec.Command", "exec.CommandContext", "exec"}},
		{`import run "os/exec"`, "run.Command", ContextlessCall{"exec.Command", "exec.CommandContext", "run"}},
		{`import . "os/exec"`, "Command", ContextlessCall{"exec.Command", "exec.CommandContext", "Command"}},
		{`import "net"`, "(net.DialUnix)", ContextlessCall{"net.DialUnix", "net.Dialer.DialContext", "net"}},
		{`import web "net/http"`, "web.PostForm", ContextlessCall{"http.PostForm", "http.NewRequestWithContext", "web"}},
	}
	for _, tc := range cases {
		im, fun := parseCallee(t, tc.imports, tc.callee)
		if got, ok := ResolveContextlessCall(im, fun); !ok || got != tc.want {
			t.Errorf("ResolveContextlessCall(%s; %s) = %+v, %v; want %+v, true", tc.imports, tc.callee, got, ok, tc.want)
		}
	}
}

func TestResolveContextlessCall_Negative_ContextAwareAndUnrelatedCallees(t *testing.T) {
	cases := [][2]string{
		{`import "os/exec"`, "exec.CommandContext"},
		{`import "net/http"`, "http.NewRequestWithContext"},
		{`import "example.com/exec"`, "exec.Command"},
		{`import "os/exec"`, "runner.Command"},
		{`import "net/http"`, "http.DefaultClient.Get"},
		{`import "os/exec"`, "Command"},
		{`import _ "os/exec"`, "exec.Command"},
	}
	for _, tc := range cases {
		im, fun := parseCallee(t, tc[0], tc[1])
		if got, ok := ResolveContextlessCall(im, fun); ok {
			t.Errorf("ResolveContextlessCall(%s; %s) = %+v; want no match", tc[0], tc[1], got)
		}
	}
}

func TestResolveContextlessCall_Boundary_TimeoutFormsAndEmptyImports(t *testing.T) {
	for _, callee := range []string{"net.DialTimeout", "net.Dialer"} {
		im, fun := parseCallee(t, `import "net"`, callee)
		if got, ok := ResolveContextlessCall(im, fun); ok {
			t.Errorf("%s is bounded without a context, got %+v", callee, got)
		}
	}
	_, fun := parseCallee(t, `import "os/exec"`, "exec.Command")
	if got, ok := ResolveContextlessCall(GoImports{}, fun); ok {
		t.Errorf("a file without imports binds no package, got %+v", got)
	}
	if got, ok := ResolveContextlessCall(GoImports{}, &ast.BadExpr{}); ok {
		t.Errorf("a malformed callee resolves to nothing, got %+v", got)
	}
}

func TestGoIO_Negative_BoundedContextsAndContextAwareCalls(t *testing.T) {
	src := strings.Join([]string{
		"package p",
		"",
		"import (",
		"\t\"context\"",
		"\t\"net\"",
		"\t\"os/exec\"",
		"\t\"time\"",
		"\tother \"example.com/exec\"",
		")",
		"",
		"func F(ctx context.Context) {",
		"\tuse(ctx)",
		"\tb, cancel := context.WithTimeout(context.Background(), time.Second)",
		"\tdefer cancel()",
		"\tuse(b)",
		"\td, cancel2 := context.WithDeadline(context.TODO(), time.Now())",
		"\tdefer cancel2()",
		"\tuse(context.WithValue(d, key, 1))",
		"\tc, cancel3 := context.WithCancel(b)",
		"\tdefer cancel3()",
		"\tuse(c)",
		"\tr := context.Background()",
		"\tr, cancel4 := context.WithTimeout(r, time.Second)",
		"\tdefer cancel4()",
		"\tuse(r)",
		"\tstop := context.AfterFunc(context.Background(), func() {})",
		"\tdefer stop()",
		"\tuse(context.Cause(context.TODO()))",
		"\texec.CommandContext(b, \"git\")",
		"\tnet.DialTimeout(\"tcp\", \"x\", time.Second)",
		"\tvar dialer net.Dialer",
		"\tdialer.DialContext(b, \"tcp\", \"x\")",
		"\tother.Command(\"x\")",
		"\tcontextFromHelper(helper())",
		"}",
		"",
		"func shadowed(exec runner, context lib) {",
		"\texec.Command(\"x\")",
		"\tuse(context.Background())",
		"}",
		"",
	}, "\n")
	if rep := scanGoIO(t, "p.go", src); len(rep.Violations) != 0 {
		t.Fatalf("bounded contexts, caller contexts and context-aware calls must stay silent: %+v", rep.Violations)
	}
}

func TestGoIO_Negative_EntryPointAndTestFilesAreExempt(t *testing.T) {
	body := strings.Join([]string{
		"import (",
		"\t\"context\"",
		"\t\"net/http\"",
		"\t\"os/exec\"",
		")",
		"",
		"func main() {",
		"\tctx := context.Background()",
		"\trun(ctx)",
		"\tgo func() { serve(ctx) }()",
		"\texec.Command(\"git\")",
		"\thttp.Get(\"x\")",
		"}",
		"",
	}, "\n")
	root := t.TempDir()
	writeFixture(t, root, "cmd/tool/main.go", "package main\n\n"+body)
	writeFixture(t, root, "lib/lib_test.go", "package lib\n\n"+strings.Replace(body, "func main()", "func TestX(t *testing.T)", 1))
	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("main.main and test files own their contexts: %+v", rep.Violations)
	}
}

func TestGoIO_Boundary_EntryPointIsOnlyMainMainOfPackageMain(t *testing.T) {
	src := func(pkg string) string {
		return strings.Join([]string{
			"package " + pkg, // 1
			"",               // 2
			"import \"context\"",
			"", // 4
			"type S struct{}",
			"", // 6
			"func (S) main() { use(context.Background()) }", // 7 a method is not the entry point
			"",                                    // 8
			"func run() { use(context.TODO()) }",  // 9 a helper main calls is not either
			"",                                    // 10
			"func main() { use(context.TODO()) }", // 11 exempt only in package main
			"",
		}, "\n")
	}
	root := t.TempDir()
	writeFixture(t, root, "cmd/tool/main.go", src("main"))
	writeFixture(t, root, "lib/main.go", src("lib"))
	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "cmd/tool/main.go", 7}, {"HISS-02", "cmd/tool/main.go", 9},
		{"HISS-02", "lib/main.go", 7}, {"HISS-02", "lib/main.go", 9}, {"HISS-02", "lib/main.go", 11},
	})
}

func TestGoIO_Boundary_BindingsFollowGoScopes(t *testing.T) {
	src := strings.Join([]string{
		"package p", // 1
		"",          // 2
		"import (",  // 3
		"\t\"context\"",
		"\t\"time\"",
		")", // 6
		"",  // 7
		"func F(ctx context.Context, cond bool) {", // 8
		"\tif cond {",                      // 9
		"\t\tctx := context.Background()",  // 10
		"\t\tuse(ctx)",                     // 11 the inner binding
		"\t}",                              // 12
		"\tuse(ctx)",                       // 13 the parameter again, silent
		"\tif c := context.TODO(); cond {", // 14
		"\t\tuse(c)",                       // 15 an if header declares into its body
		"\t}",                              // 16
		"\tvar late context.Context",       // 17
		"\tif cond {",                      // 18
		"\t\tlate = context.Background()",  // 19 assigns the function's variable
		"\t}",                              // 20
		"\tuse(late)",                      // 21 still deadline-free here
		"\tlate, cancel := context.WithTimeout(late, time.Second)", // 22 rebound, bounded
		"\tdefer cancel()",                            // 23
		"\tuse(late)",                                 // 24 silent
		"\tbg := context.Background()",                // 25
		"\tf := func(bg context.Context) { use(bg) }", // 26 the literal's parameter, silent
		"\tswitch {",                                  // 27
		"\tcase cond:",                                // 28
		"\t\tt := context.TODO()",                     // 29
		"\t\tuse(t)",                                  // 30 a case clause scopes its body
		"\t}",                                         // 31
		"\tuse(t)",                                    // 32 another t, silent
		"\t_ = f",                                     // 33
		"}",                                           // 34
		"",
		"func G() { use(bg) }", // 36 a new function starts with nothing tracked
		"",
	}, "\n")
	rep := scanGoIO(t, "p.go", src)
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "p.go", 11}, {"HISS-02", "p.go", 15}, {"HISS-02", "p.go", 21},
		{"HISS-02", "p.go", 30},
	})
}

func TestGoIO_Boundary_DerivationChainAndPackageLevel(t *testing.T) {
	chain := "context.Background()"
	for i := 0; i < 64; i++ {
		chain = "context.WithValue(" + chain + ", k, v)"
	}
	src := strings.Join([]string{
		"package p", // 1
		"",          // 2
		"import \"context\"",
		"",                                  // 4
		"var root = context.Background()",   // 5 package variables are not followed
		"",                                  // 6
		"var client = dial(context.TODO())", // 7 a call at package level is still a call
		"",                                  // 8
		"func F() {",                        // 9
		"\tuse(root)",                       // 10 silent: a package variable
		"\tuse(" + chain + ")",              // 11 64 inheriting steps down to the root
		"}",                                 // 12
		"",
	}, "\n")
	rep := scanGoIO(t, "p.go", src)
	assertViolations(t, rep, []expectedViolation{{"HISS-02", "p.go", 7}, {"HISS-02", "p.go", 11}})
}
