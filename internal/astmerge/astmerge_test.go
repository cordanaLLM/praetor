package astmerge

import (
	"context"
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// canceledContext returns a context that is already canceled, for testing that an I/O
// path actually observes cancellation rather than only checking it once up front.
func canceledContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// =========================================================================
// Positive 3D Tests
// =========================================================================

func TestMerge_Positive_OrthogonalAdditions(t *testing.T) {
	base := `package mathutil

func Add(a, b int) int {
	return a + b
}
`
	ours := `package mathutil

func Add(a, b int) int {
	return a + b
}

func Multiply(a, b int) int {
	return a * b
}
`
	theirs := `package mathutil

func Add(a, b int) int {
	return a + b
}

func Divide(a, b int) int {
	return a / b
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge for orthogonal additions, got conflicts: %+v", res.Conflicts)
	}

	if len(res.Conflicts) != 0 {
		t.Errorf("expected 0 conflicts, got %d", len(res.Conflicts))
	}

	if !strings.Contains(res.MergedCode, "func Add") {
		t.Errorf("merged code missing Add")
	}
	if !strings.Contains(res.MergedCode, "func Multiply") {
		t.Errorf("merged code missing Multiply")
	}
	if !strings.Contains(res.MergedCode, "func Divide") {
		t.Errorf("merged code missing Divide")
	}

	// Verify merged code is 100% valid Go AST
	fset := token.NewFileSet()
	if _, err := parser.ParseFile(fset, "merged.go", res.MergedCode, parser.AllErrors); err != nil {
		t.Fatalf("merged code failed Go parser verification: %v\nCode:\n%s", err, res.MergedCode)
	}
}

func orthogonalTestSources() (string, string, string) {
	base := "package service\n\nimport \"fmt\"\n\nfunc ProcessA() string {\n\treturn fmt.Sprintf(\"A\")\n}\n\nfunc ProcessB() string {\n\treturn fmt.Sprintf(\"B\")\n}\n"
	ours := "package service\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\nfunc ProcessA() string {\n\treturn strings.ToUpper(\"A-v2\")\n}\n\nfunc ProcessB() string {\n\treturn fmt.Sprintf(\"B\")\n}\n"
	theirs := "package service\n\nimport (\n\t\"fmt\"\n\t\"time\"\n)\n\nfunc ProcessA() string {\n\treturn fmt.Sprintf(\"A\")\n}\n\nfunc ProcessB() string {\n\treturn time.Now().String()\n}\n"
	return base, ours, theirs
}

func TestMerge_Positive_OrthogonalModificationsAndImports(t *testing.T) {
	base, ours, theirs := orthogonalTestSources()

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge, got conflicts: %+v", res.Conflicts)
	}

	if !strings.Contains(res.MergedCode, `"strings"`) || !strings.Contains(res.MergedCode, `"time"`) || !strings.Contains(res.MergedCode, `"fmt"`) {
		t.Errorf("missing merged imports in code:\n%s", res.MergedCode)
	}

	if !strings.Contains(res.MergedCode, "strings.ToUpper(\"A-v2\")") {
		t.Errorf("missing ours ProcessA update")
	}
	if !strings.Contains(res.MergedCode, "time.Now().String()") {
		t.Errorf("missing theirs ProcessB update")
	}
}

func TestMerge_Positive_MethodAndTypeDeclarations(t *testing.T) {
	base := `package data

type Config struct {
	Timeout int
}

func (c *Config) GetTimeout() int {
	return c.Timeout
}
`
	ours := `package data

type Config struct {
	Timeout int
}

func (c *Config) GetTimeout() int {
	return c.Timeout
}

func (c *Config) IsValid() bool {
	return c.Timeout > 0
}
`
	theirs := `package data

type Config struct {
	Timeout int
}

type Metrics struct {
	Count int
}

func (c *Config) GetTimeout() int {
	return c.Timeout
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge for disjoint method and type additions")
	}

	if !strings.Contains(res.MergedCode, "func (c *Config) IsValid()") {
		t.Errorf("missing method IsValid in merged code")
	}
	if !strings.Contains(res.MergedCode, "type Metrics struct") {
		t.Errorf("missing type Metrics in merged code")
	}
}

// =========================================================================
// Negative 3D Tests
// =========================================================================

func TestMerge_Negative_DirectSymbolCollision(t *testing.T) {
	base := `package calc

func Compute(x int) int {
	return x
}
`
	ours := `package calc

func Compute(x int) int {
	return x * 10
}
`
	theirs := `package calc

func Compute(x int) int {
	return x + 99
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("unexpected Merge error: %v", err)
	}

	if res.Clean {
		t.Fatalf("expected collision conflict on Compute(), but merge was marked clean")
	}

	if len(res.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(res.Conflicts))
	}

	c := res.Conflicts[0]
	if c.Symbol != "func:Compute" {
		t.Errorf("expected conflict on func:Compute, got %s", c.Symbol)
	}
	if !strings.Contains(c.Ours, "x * 10") || !strings.Contains(c.Theirs, "x + 99") {
		t.Errorf("conflict details missing conflicting bodies: %+v", c)
	}
}

func TestMerge_Negative_ConflictingSymbolAddition(t *testing.T) {
	base := `package store

func Exists() bool {
	return true
}
`
	ours := `package store

func Exists() bool {
	return true
}

func Fetch() string {
	return "ours-impl"
}
`
	theirs := `package store

func Exists() bool {
	return true
}

func Fetch() string {
	return "theirs-impl"
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Clean {
		t.Fatalf("expected collision when both branches add same symbol with different bodies")
	}

	if len(res.Conflicts) != 1 || res.Conflicts[0].Symbol != "func:Fetch" {
		t.Errorf("unexpected conflicts: %+v", res.Conflicts)
	}
}

func TestMerge_Negative_ModifyVsDeleteCollision(t *testing.T) {
	base := `package cache

func Invalidate() {
	// legacy
}
`
	ours := `package cache

func Invalidate() {
	// modified implementation
	println("invalidating")
}
`
	theirs := `package cache
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if res.Clean {
		t.Fatalf("expected conflict when symbol is modified in ours and deleted in theirs")
	}

	if len(res.Conflicts) != 1 || res.Conflicts[0].Symbol != "func:Invalidate" {
		t.Errorf("expected Invalidate conflict, got: %+v", res.Conflicts)
	}
}

func TestMerge_Negative_MalformedGoSyntax(t *testing.T) {
	base := `package valid`
	brokenOurs := `package valid
func Incomplete( {`
	theirs := `package valid`

	_, err := Merge(base, brokenOurs, theirs)
	if err == nil {
		t.Fatalf("expected error on malformed Go syntax in ours")
	}
	if !strings.Contains(err.Error(), "failed to parse ours source") {
		t.Errorf("unexpected error message: %v", err)
	}
}

// =========================================================================
// Boundary 3D Tests
// =========================================================================

func TestMerge_Boundary_IdenticalInputs(t *testing.T) {
	src := `package same

func DoNothing() {}
`
	res, err := Merge(src, src, src)
	if err != nil {
		t.Fatalf("identical merge failed: %v", err)
	}
	if !res.Clean {
		t.Errorf("identical merge should be clean")
	}
	if res.MergedCode != src {
		t.Errorf("identical merge changed code")
	}
}

func TestMerge_Boundary_EmptyBaseConcurrentFileCreation(t *testing.T) {
	base := ""
	ours := `package newpkg

func FromOurs() string {
	return "ours"
}
`
	theirs := `package newpkg

func FromTheirs() string {
	return "theirs"
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("empty base merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected clean merge when creating new file concurrently with disjoint funcs")
	}
	if !strings.Contains(res.MergedCode, "FromOurs") || !strings.Contains(res.MergedCode, "FromTheirs") {
		t.Errorf("merged code missing symbols: %s", res.MergedCode)
	}
}

func TestMerge_Boundary_OneBranchUnchanged(t *testing.T) {
	base := `package mod

func Helper() int { return 1 }
`
	ours := base
	theirs := `package mod

func Helper() int { return 1 }

func Extra() int { return 2 }
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("merge failed: %v", err)
	}
	if !res.Clean {
		t.Errorf("expected clean merge when ours is unchanged from base")
	}
	if !strings.Contains(res.MergedCode, "Extra") {
		t.Errorf("expected theirs change to be adopted cleanly")
	}
}

func TestMerge_Boundary_MergeFilesWithContext(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "base.go")
	oursPath := filepath.Join(tmpDir, "ours.go")
	theirsPath := filepath.Join(tmpDir, "theirs.go")

	baseCode := "package f\nfunc A() {}\n"
	oursCode := "package f\nfunc A() {}\nfunc B() {}\n"
	theirsCode := "package f\nfunc A() {}\nfunc C() {}\n"

	if err := os.WriteFile(basePath, []byte(baseCode), 0644); err != nil {
		t.Fatalf("failed to write base: %v", err)
	}
	if err := os.WriteFile(oursPath, []byte(oursCode), 0644); err != nil {
		t.Fatalf("failed to write ours: %v", err)
	}
	if err := os.WriteFile(theirsPath, []byte(theirsCode), 0644); err != nil {
		t.Fatalf("failed to write theirs: %v", err)
	}

	res, err := MergeFiles(basePath, oursPath, theirsPath)
	if err != nil {
		t.Fatalf("MergeFiles failed: %v", err)
	}
	if !res.Clean {
		t.Errorf("MergeFiles expected clean merge")
	}
	if !strings.Contains(res.MergedCode, "func B") || !strings.Contains(res.MergedCode, "func C") {
		t.Errorf("MergeFiles missing symbols in: %s", res.MergedCode)
	}
}

// =========================================================================
// BUG-477: the ours==theirs shortcut must not report a clean merge over
// source that cannot parse.
// =========================================================================

func TestMerge_Negative_IdenticalButInvalidSource(t *testing.T) {
	broken := "package f\nfunc Broken( {"

	_, err := Merge(broken, broken, broken)
	if err == nil {
		t.Fatalf("expected an error merging identical-but-invalid ours/theirs source")
	}
}

// =========================================================================
// BUG-212: the declaration bound must fail the merge, not truncate it.
// =========================================================================

func TestMerge_Boundary_ExactlyMaxDeclarations(t *testing.T) {
	src := manyFuncsSource("atbound", maxASTDeclarations)

	res, err := Merge(src, src, src)
	if err != nil {
		t.Fatalf("expected exactly %d declarations to merge, got: %v", maxASTDeclarations, err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge at the declaration bound")
	}
}

func TestMerge_Negative_TooManyDeclarations(t *testing.T) {
	src := manyFuncsSource("overbound", maxASTDeclarations+1)

	_, err := Merge(src, src, src)
	if err == nil {
		t.Fatalf("expected an error past the %d declaration bound", maxASTDeclarations)
	}
	if !strings.Contains(err.Error(), "exceeding") {
		t.Errorf("unexpected error: %v", err)
	}
}

func manyFuncsSource(pkg string, count int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkg)
	for i := 0; i < count; i++ {
		fmt.Fprintf(&b, "func F%d() {}\n", i)
	}
	return b.String()
}

// =========================================================================
// BUG-819: methods on distinct generic receivers must not collide on the
// same "*unknown" merge key.
// =========================================================================

func TestMerge_Positive_GenericReceiverMethodSurvives(t *testing.T) {
	base := `package generics

type Set[T any] struct{}

type Queue[T any] struct{}

func (q *Queue[T]) Close() error { return nil }
`
	ours := `package generics

type Set[T any] struct{}

type Queue[T any] struct{}

func (q *Queue[T]) Close() error { return nil }

func (s *Set[T]) Close() error { return nil }
`
	theirs := base

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge, got conflicts: %v", res.Conflicts)
	}
	if !strings.Contains(res.MergedCode, "func (q *Queue[T]) Close()") {
		t.Errorf("expected (*Queue[T]).Close to survive the merge, got:\n%s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "func (s *Set[T]) Close()") {
		t.Errorf("expected the new (*Set[T]).Close to be added, got:\n%s", res.MergedCode)
	}
}

// =========================================================================
// BUG-478: readFileWithContext must observe a context that is already
// expired, not just check it once before an unbounded read.
// =========================================================================

func TestMerge_Negative_MergeFilesCanceledContext(t *testing.T) {
	tmpDir := t.TempDir()
	path := filepath.Join(tmpDir, "ours.go")
	if err := os.WriteFile(path, []byte("package f\n"), 0644); err != nil {
		t.Fatalf("failed to write fixture: %v", err)
	}

	_, err := readFileWithContext(canceledContext(t), path)
	if err == nil {
		t.Fatalf("expected an error reading with an already-canceled context")
	}
}

// =========================================================================
// BUG-479: ResolvedCount must include cleanly-deleted symbols, not just
// carried-through additions.
// =========================================================================

func TestMerge_Boundary_ResolvedCountIncludesCleanDeletions(t *testing.T) {
	base := "package api\n\nfunc A() {}\n\nfunc B() {}\n"
	ours := "package api\n\nfunc A() {}\n" // B deleted cleanly
	theirs := base                         // unchanged

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge, got conflicts: %v", res.Conflicts)
	}
	if res.ResolvedCount != 2 {
		t.Errorf("expected ResolvedCount 2 (A kept + B cleanly deleted), got %d", res.ResolvedCount)
	}
	if strings.Contains(res.MergedCode, "func B") {
		t.Errorf("expected B to be absent from the merged output, got:\n%s", res.MergedCode)
	}
}

// =========================================================================
// BUG-214: a clean merge must carry the build-constraint comment and the
// package doc comment through, not drop them.
// =========================================================================

func TestMerge_Positive_BuildTagsAndPackageDocSurviveMerge(t *testing.T) {
	const template = `//go:build linux

// Package doccheck documents intent for the merge.
package doccheck

func Base() {}
%s`

	base := fmt.Sprintf(template, "")
	ours := fmt.Sprintf(template, "\nfunc Ours() {}\n")
	theirs := fmt.Sprintf(template, "\nfunc Theirs() {}\n")

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge, got conflicts: %v", res.Conflicts)
	}
	if !strings.Contains(res.MergedCode, "//go:build linux") {
		t.Errorf("expected the build constraint to survive the merge, got:\n%s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "Package doccheck documents intent for the merge.") {
		t.Errorf("expected the package doc comment to survive the merge, got:\n%s", res.MergedCode)
	}
	if !strings.Contains(res.MergedCode, "func Ours") || !strings.Contains(res.MergedCode, "func Theirs") {
		t.Errorf("expected both orthogonal additions in the merged output, got:\n%s", res.MergedCode)
	}
}

// =========================================================================
// BUG-480: MergeFiles needs negative/boundary coverage, and merged code
// needs to be verified compilable, not just gofmt-shaped.
// =========================================================================

func TestMerge_Negative_MergeFilesOursMissing(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "base.go")
	oursPath := filepath.Join(tmpDir, "missing-ours.go")
	theirsPath := filepath.Join(tmpDir, "theirs.go")
	writeMergeFixtureFile(t, basePath, "package f\n")
	writeMergeFixtureFile(t, theirsPath, "package f\n")

	_, err := MergeFiles(basePath, oursPath, theirsPath)
	if err == nil {
		t.Fatalf("expected an error for a missing ours file")
	}
}

func TestMerge_Negative_MergeFilesTheirsIsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	basePath := filepath.Join(tmpDir, "base.go")
	oursPath := filepath.Join(tmpDir, "ours.go")
	theirsDir := filepath.Join(tmpDir, "theirs-is-a-dir")
	writeMergeFixtureFile(t, basePath, "package f\n")
	writeMergeFixtureFile(t, oursPath, "package f\n")
	if err := os.Mkdir(theirsDir, 0755); err != nil {
		t.Fatalf("failed creating directory fixture: %v", err)
	}

	_, err := MergeFiles(basePath, oursPath, theirsDir)
	if err == nil {
		t.Fatalf("expected an error when theirs path is a directory, not a file")
	}
}

func writeMergeFixtureFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed writing fixture %s: %v", path, err)
	}
}

func TestMerge_Boundary_MergedCodeTypeChecks(t *testing.T) {
	base := "package typecheck\n\nfunc Base() int { return 1 }\n"
	ours := "package typecheck\n\nfunc Base() int { return 1 }\n\nfunc Ours() string { return \"ours\" }\n"
	theirs := "package typecheck\n\nfunc Base() int { return 1 }\n\nfunc Theirs() bool { return true }\n"

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge, got conflicts: %v", res.Conflicts)
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "merged.go", res.MergedCode, parser.ParseComments)
	if err != nil {
		t.Fatalf("merged code failed to parse: %v\n%s", err, res.MergedCode)
	}

	conf := types.Config{Importer: importer.Default()}
	if _, err := conf.Check("typecheck", fset, []*ast.File{file}, nil); err != nil {
		t.Fatalf("merged code failed type-checking: %v\n%s", err, res.MergedCode)
	}
}
