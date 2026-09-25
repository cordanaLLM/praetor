package difftest

import (
	"fmt"
	"go/format"
	"path"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// noPanicHelper is the helper every synthesized test reports a panic through. It is
// emitted once per generated file, only when at least one test is.
const noPanicHelper = `// difftestNoPanic runs call and reports a panic as a test failure naming the input.
func difftestNoPanic(t *testing.T, what string, call func()) {
	t.Helper()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("%s panicked: %v", what, r)
		}
	}()
	call()
}
`

// dimension selects the argument set one test passes.
type dimension int

const (
	dimPositive dimension = iota
	dimNegative
	dimLower
	dimUpper
)

func (p argPlan) arg(d dimension) string {
	switch d {
	case dimNegative:
		return p.negative
	case dimLower:
		return p.lower
	case dimUpper:
		return p.upper
	}
	return p.positive
}

// suiteWriter assembles one generated file: the imports the emitted tests use, test names
// that cannot collide, and the test bodies.
type suiteWriter struct {
	imports map[string]string
	names   map[string]bool
	body    strings.Builder
	emitted int
}

func generateSuites(pkgName, targetPkg string, funcs []funcMetadata) (*DiffTestResult, error) {
	w := &suiteWriter{imports: make(map[string]string), names: make(map[string]bool)}
	suites := make([]FuncTestSuite, 0, len(funcs))
	targetNames := make([]string, 0, len(funcs))

	for i := 0; i < len(funcs); i++ {
		suites = append(suites, w.writeSuite(funcs[i]))
		targetNames = append(targetNames, funcs[i].Name)
	}

	formatted, err := format.Source([]byte(w.file(targetPkg)))
	if err != nil {
		return nil, fmt.Errorf("failed formatting generated tests: %w", err)
	}

	return &DiffTestResult{
		PackageName:   pkgName,
		TargetPackage: targetPkg,
		GeneratedCode: string(formatted),
		TargetFuncs:   targetNames,
		Suites:        suites,
	}, nil
}

// writeSuite emits the three tests for fn, or records why none can be emitted.
func (w *suiteWriter) writeSuite(fn funcMetadata) FuncTestSuite {
	suite := FuncTestSuite{FuncName: fn.Name, Receiver: fn.Receiver, SkipReason: fn.SkipReason}
	if suite.SkipReason == "" {
		suite.SkipReason = w.addImports(fn)
	}
	if suite.SkipReason != "" {
		return suite
	}
	name := w.testName(fn)
	var pos, neg, bnd int
	suite.PositiveTest, pos = positiveTest(fn, name)
	suite.NegativeTest, neg = negativeTest(fn, name)
	suite.BoundaryTest, bnd = boundaryTest(fn, name)
	suite.CheckCount = pos + neg + bnd
	for _, test := range []string{suite.PositiveTest, suite.NegativeTest, suite.BoundaryTest} {
		w.body.WriteString(test)
		w.body.WriteString("\n\n")
	}
	w.emitted++
	return suite
}

// addImports records the packages fn's tests use, or refuses fn when one of its package
// names is already bound to a different path in this file.
func (w *suiteWriter) addImports(fn funcMetadata) string {
	needed := make(map[string]string, len(fn.Imports)+3)
	for name, importPath := range fn.Imports {
		needed[name] = importPath
	}
	needed["testing"] = "testing"
	if fn.UsesContext {
		needed["context"] = "context"
	}
	if fn.UsesStrings {
		needed["strings"] = "strings"
	}
	for name, importPath := range needed {
		if bound, ok := w.imports[name]; ok && bound != importPath {
			return fmt.Sprintf("package name %s is needed for both %s and %s", name, bound, importPath)
		}
	}
	for name, importPath := range needed {
		w.imports[name] = importPath
	}
	return ""
}

// testName returns the name the three tests share: the function name, qualified by the
// receiver type for a method, with a numeric suffix if an earlier suite took it. The first
// letter is upper-cased because go test ignores a TestXxx whose Xxx starts lower-case, so
// the suite of an unexported function would never run.
func (w *suiteWriter) testName(fn funcMetadata) string {
	base := fn.Name
	if fn.Receiver != "" {
		base = fn.Receiver + "_" + fn.Name
	}
	if first, size := utf8.DecodeRuneInString(base); unicode.IsLower(first) {
		base = string(unicode.ToUpper(first)) + base[size:]
	}
	name := base
	for n := 2; w.names[name] && n <= maxFunctionsToSynthesize+1; n++ {
		name = base + "_" + strconv.Itoa(n)
	}
	w.names[name] = true
	return name
}

// file renders the package clause, the imports the emitted tests use and the tests. A file
// with no emitted test is a bare package clause, which still compiles.
func (w *suiteWriter) file(targetPkg string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", targetPkg)
	if w.emitted == 0 {
		return b.String()
	}
	// One spec per bound name: a file may bind one path under two names, such as context
	// for the synthesized ctx and stdctx for a parameter type the source spells that way.
	names := make([]string, 0, len(w.imports))
	for name := range w.imports {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		pi, pj := w.imports[names[i]], w.imports[names[j]]
		return pi < pj || (pi == pj && names[i] < names[j])
	})
	b.WriteString("import (\n")
	for _, name := range names {
		importPath := w.imports[name]
		if name != path.Base(importPath) {
			fmt.Fprintf(&b, "\t%s %q\n", name, importPath)
			continue
		}
		fmt.Fprintf(&b, "\t%q\n", importPath)
	}
	b.WriteString(")\n\n")
	b.WriteString(w.body.String())
	b.WriteString(noPanicHelper)
	return b.String()
}

// label names fn in failure messages.
func label(fn funcMetadata) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}

// invocation renders the call with one argument per declared parameter.
func invocation(fn funcMetadata, d dimension) string {
	args := make([]string, 0, len(fn.Params))
	for i := 0; i < len(fn.Params); i++ {
		if !fn.Params[i].omit {
			args = append(args, fn.Params[i].arg(d))
		}
	}
	return fn.Callee + "(" + strings.Join(args, ", ") + ")"
}

// errorCall assigns the call's final error result to err and discards the others, one
// blank identifier per value so the assignment matches the declared arity.
func errorCall(fn funcMetadata, d dimension) string {
	return strings.Repeat("_, ", fn.ResultCount-1) + "err = " + invocation(fn, d)
}

// setup renders the receiver and context a test needs. canceled selects the negative
// dimension's already-canceled context.
func setup(b *strings.Builder, fn funcMetadata, canceled bool) {
	if fn.RecvInit != "" {
		fmt.Fprintf(b, "\tobj := %s\n", fn.RecvInit)
	}
	switch {
	case fn.UsesContext && canceled:
		b.WriteString("\tctx, cancel := context.WithCancel(context.Background())\n\tcancel()\n")
	case fn.UsesContext:
		b.WriteString("\tctx := context.Background()\n")
	}
}

// guardedCall renders one call wrapped in the panic helper. With capture the final error
// result lands in err, which the caller declared.
func guardedCall(b *strings.Builder, fn funcMetadata, d dimension, what string, capture bool) {
	call := invocation(fn, d)
	if capture {
		call = errorCall(fn, d)
	}
	fmt.Fprintf(b, "\tdifftestNoPanic(t, %q, func() {\n\t\t%s\n\t})\n", label(fn)+" "+what, call)
}

// positiveTest calls fn with representative input: it must not panic and, when fn returns
// an error, must return nil.
func positiveTest(fn funcMetadata, name string) (string, int) {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s_Positive(t *testing.T) {\n", name)
	setup(&b, fn, false)
	checks := 1
	if fn.ErrorLast {
		b.WriteString("\tvar err error\n")
	}
	guardedCall(&b, fn, dimPositive, "on positive input", fn.ErrorLast)
	if fn.ErrorLast {
		fmt.Fprintf(&b, "\tif err != nil {\n\t\tt.Fatalf(%q, err)\n\t}\n", label(fn)+" on positive input: %v")
		checks++
	}
	b.WriteString("}")
	return b.String(), checks
}

// negativeTest calls fn with empty, negative or nil input and a canceled context. Nothing
// requires fn to reject that input, so an accepted input is logged rather than failed; a
// panic, or an error with an empty message, fails.
func negativeTest(fn funcMetadata, name string) (string, int) {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s_Negative(t *testing.T) {\n", name)
	setup(&b, fn, true)
	checks := 1
	if fn.ErrorLast {
		b.WriteString("\tvar err error\n")
	}
	guardedCall(&b, fn, dimNegative, "on invalid input", fn.ErrorLast)
	if fn.ErrorLast {
		fmt.Fprintf(&b, "\tif err == nil {\n\t\tt.Logf(%q)\n\t} else if err.Error() == \"\" {\n\t\tt.Errorf(%q)\n\t}\n",
			label(fn)+" accepted invalid input without an error",
			label(fn)+" returned an error with an empty message on invalid input")
		checks++
	}
	b.WriteString("}")
	return b.String(), checks
}

// boundaryTest calls fn at the zero or empty lower bound and at a large upper bound; neither
// may panic.
func boundaryTest(fn funcMetadata, name string) (string, int) {
	var b strings.Builder
	fmt.Fprintf(&b, "func Test%s_Boundary(t *testing.T) {\n", name)
	setup(&b, fn, false)
	guardedCall(&b, fn, dimLower, "at the lower boundary", false)
	guardedCall(&b, fn, dimUpper, "at the upper boundary", false)
	b.WriteString("}")
	return b.String(), 2
}
