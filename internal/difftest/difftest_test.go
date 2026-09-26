package difftest

import (
	"fmt"
	"os"
	"path/filepath"
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
	verifySuiteDimensions(t, suite)

	// The generated file must compile against the source, not merely parse.
	assertTypeChecks(t, src, res)
}

func verifySuiteDimensions(t *testing.T, suite FuncTestSuite) {
	if !strings.Contains(suite.PositiveTest, "TestProcessItem_Positive") {
		t.Errorf("missing positive test function")
	}
	if !strings.Contains(suite.NegativeTest, "TestProcessItem_Negative") {
		t.Errorf("missing negative test function")
	}
	if !strings.Contains(suite.BoundaryTest, "TestProcessItem_Boundary") {
		t.Errorf("missing boundary test function")
	}

	// ProcessItem returns an error: the positive test checks for a panic and a nil error,
	// the negative test for a panic and an empty error message, the boundary test for a
	// panic at each bound.
	if suite.CheckCount != 6 {
		t.Errorf("expected 6 counted checks, got %d", suite.CheckCount)
	}
	if !strings.Contains(suite.PositiveTest, "t.Fatalf") {
		t.Errorf("positive test must fail on an unexpected error:\n%s", suite.PositiveTest)
	}
	if strings.Contains(suite.NegativeTest, "t.Fatalf") {
		t.Errorf("negative test must not require an error the target never promised:\n%s", suite.NegativeTest)
	}
	if strings.Count(suite.BoundaryTest, "difftestNoPanic(") != 2 {
		t.Errorf("boundary test must guard both bounds:\n%s", suite.BoundaryTest)
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
	if !strings.Contains(suite.PositiveTest, "obj := new(Service)") {
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

	assertTypeChecks(t, src, res)
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

	assertTypeChecks(t, src, res)
}

// =========================================================================
// BUG-481: extractFunctions must cap the appended-function count, not the
// declaration index.
// =========================================================================

func TestDiffTest_Boundary_ManyTypesThenFunctions(t *testing.T) {
	var b strings.Builder
	b.WriteString("package manytypes\n\n")
	for i := 0; i < 120; i++ {
		fmt.Fprintf(&b, "type T%d struct{ X int }\n", i)
	}
	b.WriteString("func A() {}\nfunc B() {}\nfunc C() {}\n")

	res, err := Synthesize(Options{Source: b.String()})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(res.Suites) != 3 {
		t.Fatalf("expected 3 suites past 120 leading type decls, got %d: %v", len(res.Suites), res.TargetFuncs)
	}
}

func TestDiffTest_Boundary_ExactlyFunctionCap(t *testing.T) {
	src := manyFuncsSource(maxFunctionsToSynthesize)

	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(res.Suites) != maxFunctionsToSynthesize {
		t.Fatalf("expected exactly %d suites at the cap, got %d", maxFunctionsToSynthesize, len(res.Suites))
	}
}

func TestDiffTest_Boundary_OneOverFunctionCap(t *testing.T) {
	src := manyFuncsSource(maxFunctionsToSynthesize + 1)

	res, err := Synthesize(Options{Source: src})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if len(res.Suites) != maxFunctionsToSynthesize {
		t.Fatalf("expected the cap of %d suites for %d functions, got %d", maxFunctionsToSynthesize, maxFunctionsToSynthesize+1, len(res.Suites))
	}
}

func manyFuncsSource(count int) string {
	var b strings.Builder
	b.WriteString("package manyfuncs\n\n")
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "func F%d() {}\n", i)
	}
	return b.String()
}

// =========================================================================
// BUG-217: change detection and filtering must key on receiver plus name,
// not the bare function name.
// =========================================================================

func TestDiffTest_Positive_ReceiverQualifiedChangeDetection(t *testing.T) {
	base := `package svc

type A struct{}

func (a *A) Run() int { return 1 }

type B struct{}

func (b *B) Run() int { return 2 }
`
	newSrc := `package svc

type A struct{}

func (a *A) Run() int { return 1 }

type B struct{}

func (b *B) Run() int { return 999 }
`
	res, err := SynthesizeFromDiff(base, newSrc)
	if err != nil {
		t.Fatalf("SynthesizeFromDiff failed: %v", err)
	}
	if len(res.Suites) != 1 {
		t.Fatalf("expected exactly 1 changed method (B.Run), got %d: %v", len(res.Suites), res.TargetFuncs)
	}
	if res.Suites[0].Receiver != "B" {
		t.Errorf("expected the unchanged (*A).Run to be masked out and (*B).Run selected, got receiver %q", res.Suites[0].Receiver)
	}
}

func TestDiffTest_Boundary_GenericReceiverChangeDetection(t *testing.T) {
	base := `package generics

type Set[T any] struct{}

func (s *Set[T]) Close() error { return nil }
`
	newSrc := `package generics

type Set[T any] struct{}

func (s *Set[T]) Close() error { return errClosed }
`
	res, err := SynthesizeFromDiff(base, newSrc)
	if err != nil {
		t.Fatalf("SynthesizeFromDiff on a generic receiver failed: %v", err)
	}
	if len(res.Suites) != 1 {
		t.Fatalf("expected the generic receiver's Close to be detected as changed, got %d suites", len(res.Suites))
	}
}

// =========================================================================
// BUG-485: Options.FilePath, Options.TargetPackage and SynthesizeFromDiff's
// error paths had zero coverage.
// =========================================================================

func TestDiffTest_Positive_OptionsFilePath(t *testing.T) {
	src := "package fromfile\n\nfunc Loaded() {}\n"
	path := filepath.Join(t.TempDir(), "source.go")
	if err := os.WriteFile(path, []byte(src), 0o600); err != nil {
		t.Fatalf("failed writing fixture file: %v", err)
	}

	res, err := Synthesize(Options{FilePath: path})
	if err != nil {
		t.Fatalf("Synthesize with FilePath failed: %v", err)
	}
	if len(res.Suites) != 1 || res.Suites[0].FuncName != "Loaded" {
		t.Fatalf("expected the FilePath source to be read and synthesized, got: %v", res.TargetFuncs)
	}
}

func TestDiffTest_Positive_OptionsTargetPackage(t *testing.T) {
	src := "package original\n\nfunc F() {}\n"

	res, err := Synthesize(Options{Source: src, TargetPackage: "overridden"})
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if res.TargetPackage != "overridden" {
		t.Errorf("expected TargetPackage to override the source package, got %q", res.TargetPackage)
	}
	if !strings.Contains(res.GeneratedCode, "package overridden") {
		t.Errorf("expected generated code to declare the overridden package, got:\n%s", res.GeneratedCode)
	}
}

func TestDiffTest_Negative_OptionsFilePathMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.go")

	_, err := Synthesize(Options{FilePath: path})
	if err == nil {
		t.Fatalf("expected an error reading a missing FilePath")
	}
	if !strings.Contains(err.Error(), "failed reading source file") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestDiffTest_Negative_SynthesizeFromDiffMalformedNewSource(t *testing.T) {
	base := "package api\n\nfunc F() {}\n"
	broken := "package api\nfunc Broken( {"

	_, err := SynthesizeFromDiff(base, broken)
	if err == nil {
		t.Fatalf("expected an error when the new-source side of the diff fails to parse")
	}
	if !strings.Contains(err.Error(), "new source parse error") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestDiffTest_Boundary_SynthesizeFromDiffUnparseableBase locks in the current, tolerant
// treatment of an unparseable base: SynthesizeFromDiff cannot know whether the change is
// genuinely new or an artifact of a base it could not read, so it degrades to treating
// every function in newSrc as changed rather than failing the whole diff.
func TestDiffTest_Boundary_SynthesizeFromDiffUnparseableBase(t *testing.T) {
	broken := "package api\nfunc Broken( {"
	newSrc := "package api\n\nfunc F() {}\n"

	res, err := SynthesizeFromDiff(broken, newSrc)
	if err != nil {
		t.Fatalf("expected SynthesizeFromDiff to tolerate an unparseable base, got: %v", err)
	}
	if len(res.Suites) != 1 || res.Suites[0].FuncName != "F" {
		t.Fatalf("expected F to be treated as changed when the base could not be read, got: %v", res.TargetFuncs)
	}
}
