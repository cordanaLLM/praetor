package difftest

import (
	"context"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// goTestTimeout bounds one `go test` run of a synthesized file (HISS-02). A warm build
// takes a few seconds; the bound covers a cold build cache on a slow runner.
const goTestTimeout = 3 * time.Minute

// typeCheck holds one source importer for every check in this package. It type-checks the
// standard library from GOROOT sources once and caches the packages, so no subprocess runs
// and later checks are fast. The importer is not safe for concurrent use, hence the lock.
var typeCheck struct {
	sync.Mutex
	fset     *token.FileSet
	importer types.Importer
}

// assertTypeChecks type-checks the source and the generated test file as one package, the
// way `go vet` and the compiler see them. Parsing alone let unused imports, an undefined
// fmt, wrong call arity and wrong assignment counts all pass (BUG-486).
func assertTypeChecks(t *testing.T, src string, res *DiffTestResult) {
	t.Helper()
	typeCheck.Lock()
	defer typeCheck.Unlock()
	if typeCheck.importer == nil {
		typeCheck.fset = token.NewFileSet()
		typeCheck.importer = importer.ForCompiler(typeCheck.fset, "source", nil)
	}
	files := make([]*ast.File, 0, 2)
	for name, text := range map[string]string{"source.go": src, "source_test.go": res.GeneratedCode} {
		file, err := parser.ParseFile(typeCheck.fset, name, text, parser.AllErrors)
		if err != nil {
			t.Fatalf("parse %s: %v\n%s", name, err, text)
		}
		files = append(files, file)
	}
	var problems []string
	conf := types.Config{
		Importer: typeCheck.importer,
		Error:    func(err error) { problems = append(problems, err.Error()) },
	}
	// Check returns the first error; the Error hook collected every one of them.
	if _, err := conf.Check(files[0].Name.Name, typeCheck.fset, files, nil); err != nil {
		t.Fatalf("generated tests do not type-check against the source:\n%s\nCode:\n%s",
			strings.Join(problems, "\n"), res.GeneratedCode)
	}
}

// synthesizeChecked synthesizes tests for src and asserts they type-check.
func synthesizeChecked(t *testing.T, src string) *DiffTestResult {
	t.Helper()
	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	assertTypeChecks(t, src, res)
	return res
}

// TestDiffTest_Positive_TypeChecksPerParameterArguments is BUG-216: arguments were chosen
// one per kind, so (string, string, int) was called with two arguments.
func TestDiffTest_Positive_TypeChecksPerParameterArguments(t *testing.T) {
	src := "package multi\n\nfunc Join(a, b string, n int) string {\n\treturn a + b\n}\n"
	res := synthesizeChecked(t, src)
	if !strings.Contains(res.Suites[0].PositiveTest, `Join("test-positive", "test-positive", 42)`) {
		t.Errorf("expected one argument per declared parameter:\n%s", res.Suites[0].PositiveTest)
	}
}

// TestDiffTest_Positive_TypeChecksReturnArity is BUG-219: a (value, value, error) result
// was assigned to a fixed res, err pair.
func TestDiffTest_Positive_TypeChecksReturnArity(t *testing.T) {
	src := "package triple\n\nfunc Split(s string) (int, string, error) {\n\treturn len(s), s, nil\n}\n"
	res := synthesizeChecked(t, src)
	if !strings.Contains(res.Suites[0].PositiveTest, "_, _, err = Split(") {
		t.Errorf("expected one blank per non-error result:\n%s", res.Suites[0].PositiveTest)
	}
}

// TestDiffTest_Positive_ImportsOnlyWhatTestsUse is BUG-218: context and strings were always
// imported and fmt was used but never imported.
func TestDiffTest_Positive_ImportsOnlyWhatTestsUse(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		want    []string
		notWant []string
	}{
		{"no parameters", "package p\n\nfunc Ping() {}\n", []string{`"testing"`}, []string{`"context"`, `"strings"`, `"fmt"`}},
		{"context only", "package p\n\nimport \"context\"\n\nfunc Run(ctx context.Context) error { return ctx.Err() }\n", []string{`"context"`}, []string{`"strings"`, `"fmt"`}},
		{"string only", "package p\n\nfunc Echo(s string) string { return s }\n", []string{`"strings"`}, []string{`"context"`, `"fmt"`}},
		{"renamed context", "package p\n\nimport stdctx \"context\"\n\nfunc Run(c stdctx.Context) {}\n", []string{`"context"`}, []string{`stdctx`}},
		{"one path under two names", "package p\n\nimport stdctx \"context\"\n\nfunc Run(c stdctx.Context, f stdctx.CancelFunc) {}\n", []string{"\t\"context\"\n", `stdctx "context"`}, []string{`"fmt"`}},
		{"qualified zero value", "package p\n\nimport \"time\"\n\nfunc Wait(d time.Duration) {}\n", []string{`"time"`, "*new(time.Duration)"}, []string{`"fmt"`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := synthesizeChecked(t, tc.src)
			for _, want := range tc.want {
				if !strings.Contains(res.GeneratedCode, want) {
					t.Errorf("expected %s in:\n%s", want, res.GeneratedCode)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(res.GeneratedCode, notWant) {
					t.Errorf("did not expect %s in:\n%s", notWant, res.GeneratedCode)
				}
			}
		})
	}
}

// TestDiffTest_Boundary_TypeChecksParameterKinds covers every argument shape the planner
// knows: sized integers whose upper bound must fit, unsigned kinds with no negative value,
// nil-able types, arrays, variadics, named local types and methods on non-struct types.
func TestDiffTest_Boundary_TypeChecksParameterKinds(t *testing.T) {
	src := `package kinds

type Celsius float64

func (c Celsius) Scale(by int8) Celsius { return c * Celsius(by) }

type Options struct{ Verbose bool }

func All(a int8, b uint8, c int64, d uint64, e float32, f bool, g rune, h byte,
	p *Options, m map[string]int, ch chan int, fn func() error, e2 error, v any,
	arr [2]int, o Options, cel Celsius, xs []Options, rest ...string) {
}
`
	res := synthesizeChecked(t, src)
	if len(res.Suites) != 2 {
		t.Fatalf("expected 2 suites, got %d", len(res.Suites))
	}
	upper := res.Suites[1].BoundaryTest
	for _, want := range []string{"127", "255", "9223372036854775807", "18446744073709551615", "make([]Options, 100)"} {
		if !strings.Contains(upper, want) {
			t.Errorf("expected %s in the boundary test:\n%s", want, upper)
		}
	}
	if !strings.Contains(res.Suites[0].PositiveTest, "obj := new(Celsius)") {
		t.Errorf("a non-struct receiver must be constructed with new:\n%s", res.Suites[0].PositiveTest)
	}
}

// TestDiffTest_Boundary_ZeroArgsFileTypeChecks keeps the smallest suite compilable.
func TestDiffTest_Boundary_ZeroArgsFileTypeChecks(t *testing.T) {
	res := synthesizeChecked(t, "package lifecycle\n\nfunc Ping() {}\n")
	if res.Suites[0].CheckCount != 4 {
		t.Errorf("expected 4 counted checks (1 positive, 1 negative, 2 boundary), got %d", res.Suites[0].CheckCount)
	}
}

// TestDiffTest_Negative_NoDeadChecks is BUG-484: the no-error branches emitted `if false`
// and `&& false`, checks that can never fail.
func TestDiffTest_Negative_NoDeadChecks(t *testing.T) {
	for _, src := range []string{
		"package p\n\nfunc Ping() {}\n",
		"package p\n\nfunc Count(s string) int { return len(s) }\n",
		"package p\n\nfunc Check(s string) error { return nil }\n",
	} {
		res := synthesizeChecked(t, src)
		for _, dead := range []string{"if false", "&& false", "t.Failed()"} {
			if strings.Contains(res.GeneratedCode, dead) {
				t.Errorf("generated code carries the dead check %q:\n%s", dead, res.GeneratedCode)
			}
		}
	}
}

// TestDiffTest_Negative_UnsupportedSignaturesAreSkipped keeps the planner from guessing: a
// constraint no known type argument satisfies, an undeclared generic receiver, an init
// function and an unbound package qualifier each yield a reason and no test code.
func TestDiffTest_Negative_UnsupportedSignaturesAreSkipped(t *testing.T) {
	cases := []struct {
		name   string
		src    string
		reason string
	}{
		{"numeric constraint", "package g\n\ntype Number interface{ ~int | ~float64 }\n\nfunc Sum[T Number](xs []T) T { var z T; return z }\n", "constraint"},
		{"receiver declared elsewhere", "package g\n\nfunc (s *Remote[T]) Close() error { return nil }\n", "not declared in this source"},
		{"init", "package g\n\nfunc init() {}\n", "cannot be referenced"},
		{"unbound qualifier", "package g\n\nfunc Use(n ext.Node) {}\n", "not bound by an import"},
		{"parameter spells type parameter", "package g\n\nfunc Wrap[T any](box struct{ V T }) {}\n", "type parameter T"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Synthesize(Options{Source: tc.src})
			if err != nil {
				t.Fatalf("Synthesize failed: %v", err)
			}
			suite := res.Suites[0]
			if !strings.Contains(suite.SkipReason, tc.reason) {
				t.Fatalf("expected a skip reason containing %q, got %q", tc.reason, suite.SkipReason)
			}
			if suite.PositiveTest != "" || suite.CheckCount != 0 || strings.Contains(res.GeneratedCode, "func Test") {
				t.Errorf("a skipped function must emit no tests:\n%s", res.GeneratedCode)
			}
		})
	}
}

// TestDiffTest_Positive_GenericsInstantiated covers #391: generic receivers keep their type
// identity, are constructed with a type argument, and generic functions are instantiated.
func TestDiffTest_Positive_GenericsInstantiated(t *testing.T) {
	src := `package generics

type Set[T comparable] struct{ items map[T]struct{} }

func (s *Set[T]) Add(v T) { _ = v }

type Pair[K comparable, V any] struct{}

func (p Pair[K, V]) Swap(k K, v V) (V, K) { return v, k }

func Map[T any, U interface{}](xs []T, f func(T) U) []U { return nil }
`
	res := synthesizeChecked(t, src)
	want := []string{"new(Set[int])", "new(Pair[int, int])", "Map[int, int](", "obj.Add(42)"}
	for _, w := range want {
		if !strings.Contains(res.GeneratedCode, w) {
			t.Errorf("expected %s in:\n%s", w, res.GeneratedCode)
		}
	}
	if strings.Contains(res.GeneratedCode, "Receiver") {
		t.Errorf("generic receivers must not collapse to a placeholder:\n%s", res.GeneratedCode)
	}
}

// TestDiffTest_Boundary_GenericReceiversShareMethodName is the #391 fixture: two generic
// receivers share a method name and only one changes. Exactly one suite must result, with
// no duplicate test declarations.
func TestDiffTest_Boundary_GenericReceiversShareMethodName(t *testing.T) {
	base := `package generics

import "errors"

var errClosed = errors.New("closed")

type Set[T any] struct{}

func (*Set[T]) Close() error { return nil }

type Bag[T any] struct{}

func (*Bag[T]) Close() error { return nil }
`
	changed := strings.Replace(base, "func (*Bag[T]) Close() error { return nil }", "func (*Bag[T]) Close() error { return errClosed }", 1)
	res, err := SynthesizeFromDiff(base, changed)
	if err != nil {
		t.Fatalf("SynthesizeFromDiff failed: %v", err)
	}
	if len(res.Suites) != 1 || res.Suites[0].Receiver != "Bag" {
		t.Fatalf("expected exactly Bag.Close, got %d suites: %+v", len(res.Suites), res.Suites)
	}
	assertTypeChecks(t, changed, res)
}

// TestDiffTest_Boundary_TestNamesNeverCollide covers #391's collision acceptance and the
// unexported case: go test ignores TestxYz, and a method and a function may share a name.
func TestDiffTest_Boundary_TestNamesNeverCollide(t *testing.T) {
	src := `package names

type Set struct{}

func (*Set) Close() {}

type Bag struct{}

func (*Bag) Close() {}

func Set_Close() {}

func close2() {}
`
	res := synthesizeChecked(t, src)
	for _, want := range []string{"TestSet_Close_Positive", "TestBag_Close_Positive", "TestSet_Close_2_Positive", "TestClose2_Positive"} {
		if !strings.Contains(res.GeneratedCode, want) {
			t.Errorf("expected %s in:\n%s", want, res.GeneratedCode)
		}
	}
}

// TestDiffTest_Negative_GeneratedTestsRunAgainstTheirTarget is BUG-482: the negative test
// required an error the target never promised. It builds the source and the generated file
// as a module and runs them: every generated test must pass against correct code, and the
// accepted invalid input is logged, not failed.
func TestDiffTest_Negative_GeneratedTestsRunAgainstTheirTarget(t *testing.T) {
	goCommand, err := exec.LookPath("go")
	if err != nil {
		t.Fatalf("running synthesized tests needs the go command on PATH: %v", err)
	}
	src := `package worker

import "context"

func ProcessItem(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	return "done:" + key, nil
}
`
	res := synthesizeChecked(t, src)
	dir := t.TempDir()
	files := map[string]string{"go.mod": "module example.com/worker\n\ngo 1.22\n", "worker.go": src, "worker_test.go": res.GeneratedCode}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), goTestTimeout)
	defer cancel()
	out, err := util.RunCommand(ctx, dir, goCommand, "test", "-count=1", "-v", ".")
	if err != nil {
		t.Fatalf("synthesized tests failed against a correct target: %v\n%s", err, out)
	}
	for _, want := range []string{"--- PASS: TestProcessItem_Negative", "accepted invalid input without an error"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in go test output:\n%s", want, out)
		}
	}
}
