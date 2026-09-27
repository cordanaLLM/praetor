package hiss

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// referenceSource holds one function per counting rule. Every expected value in
// TestMeasureFunc_MatchesReferenceTools was produced by gocyclo v0.6.0, gocognit v1.2.1 and
// funlen v0.2.0 run over this exact source; a value those tools leave unreported (a
// cognitive 0, or funlen at one statement or fewer) was read from the source.
const referenceSource = `package cases

import "fmt"

func a() {}
func b() {}
func c() {}

func Greet(name string) string {
	if name == "" {
		return "hello"
	}
	return fmt.Sprintf("hello %s", name)
}

func Pick(x int) int {
	if x < 0 {
		return -1
	} else if x == 0 {
		return 0
	} else {
		return 1
	}
}

func ElseNesting(p, q bool) int {
	if p {
		return 1
	} else {
		if q {
			return 2
		}
	}
	return 0
}

func Runs(p, q, r, s bool) bool {
	return p || q && r || s
}

func Enclosing(xs []int) func() int {
	return func() int {
		if len(xs) > 0 {
			return xs[0]
		}
		return 0
	}
}

func Deferred() {
	defer func() {
		a()
		b()
	}()
	go func() {
		c()
	}()
}

func Labelled(xs []int) {
outer:
	for _, x := range xs {
		for x > 0 {
			break outer
		}
	}
}

func Factorial(n int) int {
	if n == 0 {
		return 1
	}
	return n * Factorial(n-1)
}

func Shadowed(Shadowed func() int) int {
	return Shadowed()
}

func Selects(ch chan int, v int) int {
	select {
	case x := <-ch:
		return x
	default:
	}
	switch v {
	case 1:
		return 1
	default:
		return 0
	}
}

func InitClause(f func() bool) {
	if ok := f(); ok {
	}
}
`

func parseReference(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "reference.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse reference source: %v", err)
	}
	return fset, file
}

func unitsByName(t *testing.T, fset *token.FileSet, file *ast.File) map[string]FuncUnit {
	t.Helper()
	units := make(map[string]FuncUnit)
	for _, unit := range MeasureFile(fset, file) {
		units[unit.Name] = unit
	}
	return units
}

// Positive: each counting rule yields the value the reference linters report.
func TestMeasureFunc_MatchesReferenceTools(t *testing.T) {
	fset, file := parseReference(t, referenceSource)
	units := unitsByName(t, fset, file)
	for name, want := range map[string]FuncMetrics{
		"Greet":       {LOC: 6, Statements: 3, Cyclomatic: 2, Cognitive: 1},
		"Pick":        {LOC: 9, Statements: 2, Cyclomatic: 3, Cognitive: 3},  // else-if flat; else body not a statement for funlen
		"ElseNesting": {LOC: 10, Statements: 3, Cyclomatic: 3, Cognitive: 3}, // an else block does not deepen nesting
		"Runs":        {LOC: 3, Statements: 1, Cyclomatic: 4, Cognitive: 3},  // ||, &&, || are three runs
		"Enclosing":   {LOC: 8, Statements: 1, Cyclomatic: 2, Cognitive: 2},  // a nested literal counts inside its function
		"Deferred":    {LOC: 9, Statements: 5, Cyclomatic: 1, Cognitive: 0},  // go and defer literals add their statements
		"Labelled":    {LOC: 8, Statements: 1, Cyclomatic: 3, Cognitive: 4},  // a labelled jump adds one
		"Factorial":   {LOC: 6, Statements: 3, Cyclomatic: 2, Cognitive: 2},  // direct recursion adds one
		"Shadowed":    {LOC: 3, Statements: 1, Cyclomatic: 1, Cognitive: 0},  // a shadowing parameter is not recursion
		"Selects":     {LOC: 13, Statements: 8, Cyclomatic: 3, Cognitive: 2}, // default clauses add no path
		"InitClause":  {LOC: 4, Statements: 1, Cyclomatic: 2, Cognitive: 1},  // an if's init is not a statement for funlen
	} {
		if got := units[name].Metrics; got != want {
			t.Errorf("%s: metrics = %+v, want %+v", name, got, want)
		}
	}
}

// Boundary: a declaration without a body, a nil declaration and a node that is no function.
func TestMeasureFunc_BoundaryShapes(t *testing.T) {
	fset, file := parseReference(t, "package p\n\nfunc Asm(x int) int\n\nfunc Nop() {}\n")
	units := unitsByName(t, fset, file)
	if got := units["Asm"].Metrics; got != (FuncMetrics{LOC: 1, Cyclomatic: 1}) {
		t.Errorf("bodiless declaration = %+v", got)
	}
	if got := units["Nop"].Metrics; got != (FuncMetrics{LOC: 1, Cyclomatic: 1}) {
		t.Errorf("empty body = %+v", got)
	}
	var nilDecl *ast.FuncDecl
	var nilLit *ast.FuncLit
	for name, node := range map[string]ast.Node{"nil decl": nilDecl, "nil literal": nilLit, "identifier": ast.NewIdent("x"), "untyped nil": nil} {
		if got := MeasureFunc(fset, node); got != (FuncMetrics{}) {
			t.Errorf("%s measured %+v, want zero", name, got)
		}
	}
}

// Positive and negative: a package-level literal is a unit named after its variable, a literal
// bound to nothing is named as one, and a literal inside another is never a unit of its own.
func TestMeasureFile_OutermostLiteralsAreUnits(t *testing.T) {
	src := `package p

type T struct{}

const Limit = 3

var Handler, Other = func(n int) func() int {
	return func() int {
		if n > 0 {
			return n
		}
		return 0
	}
}, 1

var table = map[string]func() int{
	"a": func() int { return 1 },
}

func Plain() {}
`
	fset, file := parseReference(t, src)
	units := MeasureFile(fset, file)
	var names []string
	for _, unit := range units {
		names = append(names, unit.Name)
	}
	if got := strings.Join(names, ","); got != "Handler,func literal,Plain" {
		t.Fatalf("units = %s, want Handler,func literal,Plain", got)
	}
	if got := units[0].Metrics; got != (FuncMetrics{LOC: 8, Statements: 1, Cyclomatic: 2, Cognitive: 2}) {
		t.Errorf("Handler = %+v; the nested literal must count inside it", got)
	}
	if MeasureFile(fset, nil) != nil {
		t.Error("a nil file measured units")
	}
	var nilGen *ast.GenDecl
	var nilFunc *ast.FuncDecl
	if MeasureDecl(fset, nilGen) != nil || MeasureDecl(fset, nilFunc) != nil || MeasureDecl(fset, &ast.BadDecl{}) != nil {
		t.Error("an empty or foreign declaration measured units")
	}
}

// Boundary: a value equal to its limit is within it, one more is measured; zero limits fall
// back to the HISS-04 defaults; custom limits replace them.
func TestExceeded_Boundaries(t *testing.T) {
	at := FuncMetrics{Cyclomatic: DefaultMaxCyclomatic, Cognitive: DefaultMaxCognitive, Statements: DefaultMaxStatements}
	if got := at.Exceeded(ComplexityLimits{}); len(got) != 0 {
		t.Errorf("values at the defaults measured %+v", got)
	}
	over := FuncMetrics{Cyclomatic: DefaultMaxCyclomatic + 1, Cognitive: DefaultMaxCognitive + 1, Statements: DefaultMaxStatements + 1}
	got := over.Exceeded(ComplexityLimits{})
	var kinds []string
	for _, m := range got {
		kinds = append(kinds, string(m.Kind))
		if m.Severity != SeverityReport || m.RuleID != "HISS-04" || m.Value != m.Limit+1 {
			t.Errorf("measurement %+v: want a HISS-04 report one over its limit", m)
		}
	}
	if strings.Join(kinds, ",") != "cyclomatic,cognitive,statements" {
		t.Errorf("kinds = %v", kinds)
	}
	custom := ComplexityLimits{MaxCyclomatic: 12, MaxCognitive: 8, MaxStatements: 60}
	if got := (FuncMetrics{Cyclomatic: 11, Cognitive: 9, Statements: 51}).Exceeded(custom); len(got) != 1 || got[0].Kind != KindCognitive || got[0].Limit != 8 {
		t.Errorf("custom limits: %+v, want the cognitive 9 > 8 alone", got)
	}
}

// Positive: the summary counts kinds and functions; the lines are bounded and state what was
// left unlisted; a truncated list says its counts are a lower bound. Negative: a nil report
// renders nothing.
func TestComplexityReport_Lines(t *testing.T) {
	report := &ComplexityReport{}
	for i := 0; i < MaxListedMeasurements+1; i++ {
		report.Measurements = append(report.Measurements, Measurement{
			RuleID: "HISS-04", FilePath: "a.go", LineNumber: i/2 + 1, Symbol: fmt.Sprintf("F%d", i/2),
			Kind: []MeasurementKind{KindCyclomatic, KindCognitive}[i%2], Value: 11, Limit: 10, Severity: SeverityReport,
		})
	}
	lines := report.Lines()
	if len(lines) != MaxListedMeasurements+2 {
		t.Fatalf("lines = %d, want summary + %d + remainder", len(lines), MaxListedMeasurements)
	}
	if want := "[REPORT] HISS-04 complexity measured, not enforced: 201 measurements over limit in 101 functions (cyclomatic 101, cognitive 100, statements 0)"; lines[0] != want {
		t.Errorf("summary = %q\nwant      %q", lines[0], want)
	}
	if want := "[REPORT] HISS-04 a.go:1 Function 'F0' cyclomatic complexity 11 exceeds 10 (report: measured, not enforced)"; lines[1] != want {
		t.Errorf("line = %q\nwant   %q", lines[1], want)
	}
	if !strings.Contains(lines[len(lines)-1], "1 more measurements are not listed") {
		t.Errorf("remainder line = %q", lines[len(lines)-1])
	}
	report.Truncated = true
	if !strings.Contains(report.Summary(), "lower bound") {
		t.Errorf("truncated summary = %q", report.Summary())
	}
	var none *ComplexityReport
	if none.Lines() != nil || none.Summary() != "" {
		t.Error("a nil report rendered lines")
	}
	if got := (&ComplexityReport{}).Lines(); len(got) != 1 || !strings.Contains(got[0], ": 0 measurements over limit in 0 functions") {
		t.Errorf("empty report = %q", got)
	}
}

// flatBranches returns a function of cyclomatic complexity n built from n-1 flat ifs, so its
// cognitive complexity is n-1 and its statement count 2n-1.
func flatBranches(name string, n int) string {
	var sb strings.Builder
	sb.WriteString("func " + name + "(x int) int {\n")
	for i := 1; i < n; i++ {
		fmt.Fprintf(&sb, "\tif x > %d {\n\t\tx++\n\t}\n", i)
	}
	sb.WriteString("\treturn x\n}\n")
	return sb.String()
}

// cyclomaticMeasurements returns the cyclomatic values a report measured, by symbol.
func cyclomaticMeasurements(rep *ScanReport) map[string]int {
	got := make(map[string]int)
	for _, m := range rep.Complexity.Measurements {
		if m.Kind == KindCyclomatic {
			got[m.Symbol] = m.Value
		}
	}
	return got
}

// Positive and negative: the scanner measures production functions, including a literal bound
// to a package variable, and reports them without a single violation, infraction or breakdown
// entry, so no baseline, ratchet or gate can move because of them.
func TestScan_ComplexityIsReportedNeverEnforced(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "p.go", "package p\n\n"+flatBranches("Eleven", 11)+
		"\nvar Literal = func(x int) int {\n"+strings.TrimPrefix(flatBranches("", 12), "func (x int) int {\n"))
	writeFixture(t, root, "p_test.go", "package p\n\n"+flatBranches("TestHelper", 20))
	rep := scanFixture(t, root, ScanOptions{})
	if rep.TotalInfractions != 0 || len(rep.Violations) != 0 || len(rep.Breakdown) != 0 {
		t.Fatalf("complexity entered the infraction report: %+v", rep.Violations)
	}
	got := cyclomaticMeasurements(rep)
	if len(got) != 2 || got["Eleven"] != 11 || got["Literal"] != 12 {
		t.Fatalf("cyclomatic measurements = %v, want Eleven 11 and Literal 12 (test file exempt)", got)
	}
	for _, m := range rep.Complexity.Measurements {
		if m.Severity != SeverityReport || m.FilePath != "p.go" {
			t.Errorf("measurement %+v: want a report-only entry in p.go", m)
		}
	}
}

// Boundary: the scan honours custom limits (12 admits 11, 8 measures 9) and falls back to the
// HISS-04 defaults for zero limits (10 admits 10, measures 11).
func TestScan_ComplexityLimitsFollowOptions(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "p.go", "package p\n\n"+flatBranches("Nine", 9)+flatBranches("Ten", 10)+flatBranches("Eleven", 11))
	for name, tc := range map[string]struct {
		limits ComplexityLimits
		want   map[string]int
	}{
		"defaults": {ComplexityLimits{}, map[string]int{"Eleven": 11}},
		"loose":    {ComplexityLimits{MaxCyclomatic: 12, MaxCognitive: 100, MaxStatements: 100}, map[string]int{}},
		"tight":    {ComplexityLimits{MaxCyclomatic: 8, MaxCognitive: 100, MaxStatements: 100}, map[string]int{"Nine": 9, "Ten": 10, "Eleven": 11}},
	} {
		got := cyclomaticMeasurements(scanFixture(t, root, ScanOptions{Complexity: tc.limits}))
		if fmt.Sprint(got) != fmt.Sprint(tc.want) {
			t.Errorf("%s: measured %v, want %v", name, got, tc.want)
		}
	}
}

// Boundary: the measurement list stops at the scan cap and says so, without marking the
// infraction report truncated.
func TestScan_ComplexityListStopsAtTheCap(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "p.go", "package p\n\n"+flatBranches("A", 11)+flatBranches("B", 11))
	rep := scanFixture(t, root, ScanOptions{Cap: 1})
	if len(rep.Complexity.Measurements) != 1 || !rep.Complexity.Truncated {
		t.Errorf("complexity = %+v, want one measurement and a truncation mark", rep.Complexity)
	}
	if rep.Truncated || rep.Incomplete() {
		t.Error("a capped measurement list marked the infraction report incomplete")
	}
}
