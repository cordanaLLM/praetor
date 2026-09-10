package difftest

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestDiffTest_Positive_FunctionSynthesis(t *testing.T) {
	src := `package worker

import "context"

func ProcessItem(ctx context.Context, key string) (string, error) {
	if key == "" {
		return "", nil
	}
	return "done:" + key, nil
}
`

	opts := Options{
		Source: src,
	}

	res, err := Synthesize(opts)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if len(res.TargetFuncs) != 1 || res.TargetFuncs[0] != "ProcessItem" {
		t.Fatalf("expected 1 target func ProcessItem, got: %v", res.TargetFuncs)
	}

	if len(res.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(res.Suites))
	}

	suite := res.Suites[0]
	if !strings.Contains(suite.PositiveTest, "TestProcessItem_Positive") {
		t.Errorf("missing positive test function")
	}
	if !strings.Contains(suite.NegativeTest, "TestProcessItem_Negative") {
		t.Errorf("missing negative test function")
	}
	if !strings.Contains(suite.BoundaryTest, "TestProcessItem_Boundary") {
		t.Errorf("missing boundary test function")
	}

	// Verify each dimension has >= 2 checks
	posChecks := strings.Count(suite.PositiveTest, "t.Fatalf") + strings.Count(suite.PositiveTest, "t.Errorf")
	negChecks := strings.Count(suite.NegativeTest, "t.Fatalf") + strings.Count(suite.NegativeTest, "t.Errorf")
	bndChecks := strings.Count(suite.BoundaryTest, "t.Fatalf") + strings.Count(suite.BoundaryTest, "t.Errorf")

	if posChecks < 2 {
		t.Errorf("positive test has %d checks, expected >= 2", posChecks)
	}
	if negChecks < 2 {
		t.Errorf("negative test has %d checks, expected >= 2", negChecks)
	}
	if bndChecks < 2 {
		t.Errorf("boundary test has %d checks, expected >= 2", bndChecks)
	}

	// Verify generated code parses as 100% valid Go
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "worker_test.go", res.GeneratedCode, parser.AllErrors); err != nil {
		t.Fatalf("generated code failed Go parser: %v\nCode:\n%s", err, res.GeneratedCode)
	}
}

func TestDiffTest_Positive_MethodReceiverSynthesis(t *testing.T) {
	src := `package client

type Service struct{}

func (s *Service) CallEndpoint(key string) (int, error) {
	return len(key), nil
}
`
	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}

	if len(res.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(res.Suites))
	}

	suite := res.Suites[0]
	if suite.Receiver != "Service" {
		t.Errorf("expected receiver Service, got: %q", suite.Receiver)
	}
	if !strings.Contains(suite.PositiveTest, "obj := &Service{}") {
		t.Errorf("expected receiver instantiation in positive test")
	}
	if !strings.Contains(suite.PositiveTest, "obj.CallEndpoint(") {
		t.Errorf("expected receiver method call in positive test")
	}
}

func TestDiffTest_Positive_SynthesizeFromDiff(t *testing.T) {
	base := `package api

func Stable() int {
	return 1
}

func OldFunc() string {
	return "v1"
}
`
	newSrc := `package api

func Stable() int {
	return 1
}

func OldFunc() string {
	return "v2-modified"
}

func NewFeature() error {
	return nil
}
`

	res, err := SynthesizeFromDiff(base, newSrc)
	if err != nil {
		t.Fatalf("SynthesizeFromDiff failed: %v", err)
	}

	if len(res.TargetFuncs) != 2 {
		t.Fatalf("expected 2 changed functions (OldFunc, NewFeature), got %d: %v", len(res.TargetFuncs), res.TargetFuncs)
	}

	targetMap := make(map[string]bool)
	for _, f := range res.TargetFuncs {
		targetMap[f] = true
	}
	if !targetMap["OldFunc"] || !targetMap["NewFeature"] {
		t.Errorf("expected OldFunc and NewFeature in targets, got: %v", res.TargetFuncs)
	}
	if targetMap["Stable"] {
		t.Errorf("Stable() was unchanged and should not be synthesized")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestDiffTest_Negative_MalformedSource(t *testing.T) {
	broken := `package api
func Broken( {`

	_, err := Synthesize(Options{Source: broken})
	if err == nil {
		t.Fatalf("expected error on malformed source")
	}
	if !strings.Contains(err.Error(), "syntax error") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestDiffTest_Negative_NonexistentChangedFunction(t *testing.T) {
	src := `package api

func RealFunc() {}
`
	_, err := Synthesize(Options{
		Source:       src,
		ChangedFuncs: []string{"ImaginaryFunc"},
	})
	if err == nil {
		t.Fatalf("expected error when changed func does not exist")
	}
	if !strings.Contains(err.Error(), "no matching functions") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiffTest_Negative_EmptySource(t *testing.T) {
	_, err := Synthesize(Options{Source: ""})
	if err == nil {
		t.Fatalf("expected error for empty source")
	}
	if !strings.Contains(err.Error(), "empty source") {
		t.Errorf("unexpected error: %v", err)
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestDiffTest_Boundary_ZeroArgsZeroReturns(t *testing.T) {
	src := `package lifecycle

func Ping() {}
`
	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("failed synthesizing zero-arg zero-return func: %v", err)
	}

	if len(res.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(res.Suites))
	}

	suite := res.Suites[0]
	if !strings.Contains(suite.PositiveTest, "Ping()") {
		t.Errorf("missing Ping() invocation")
	}

	// Verify code parses cleanly
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "test.go", res.GeneratedCode, parser.AllErrors); err != nil {
		t.Fatalf("generated code failed parsing: %v", err)
	}
}

func TestDiffTest_Boundary_DiffWithNoChanges(t *testing.T) {
	src := `package sample

func Same() {}
`
	res, err := SynthesizeFromDiff(src, src)
	if err != nil {
		t.Fatalf("expected nil error on identical source diff: %v", err)
	}
	if len(res.TargetFuncs) != 0 || len(res.Suites) != 0 {
		t.Errorf("expected 0 synthesized tests when no functions changed, got %d", len(res.TargetFuncs))
	}
}

func TestDiffTest_Boundary_ComplexSignature(t *testing.T) {
	src := `package complexpkg

import "context"

func ExecuteComplex(ctx context.Context, tag string, count int, items []string) (bool, error) {
	return true, nil
}
`
	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("failed synthesizing complex signature: %v", err)
	}

	if len(res.Suites) != 1 {
		t.Fatalf("expected 1 suite, got %d", len(res.Suites))
	}

	suite := res.Suites[0]
	if !strings.Contains(suite.PositiveTest, "ExecuteComplex(ctx,") {
		t.Errorf("missing ctx in positive invocation: %s", suite.PositiveTest)
	}

	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "complex_test.go", res.GeneratedCode, parser.AllErrors); err != nil {
		t.Fatalf("complex test code failed parsing: %v\nCode:\n%s", err, res.GeneratedCode)
	}
}
