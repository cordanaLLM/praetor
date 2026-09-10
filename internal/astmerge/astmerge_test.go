package astmerge

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

func TestMerge_Positive_OrthogonalModificationsAndImports(t *testing.T) {
	base := `package service

import "fmt"

func ProcessA() string {
	return fmt.Sprintf("A")
}

func ProcessB() string {
	return fmt.Sprintf("B")
}
`
	ours := `package service

import (
	"fmt"
	"strings"
)

func ProcessA() string {
	return strings.ToUpper("A-v2")
}

func ProcessB() string {
	return fmt.Sprintf("B")
}
`
	theirs := `package service

import (
	"fmt"
	"time"
)

func ProcessA() string {
	return fmt.Sprintf("A")
}

func ProcessB() string {
	return time.Now().String()
}
`

	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}

	if !res.Clean {
		t.Fatalf("expected clean merge, got conflicts: %+v", res.Conflicts)
	}

	// Both imports should be merged cleanly
	if !strings.Contains(res.MergedCode, `"strings"`) {
		t.Errorf("missing strings import in merged code")
	}
	if !strings.Contains(res.MergedCode, `"time"`) {
		t.Errorf("missing time import in merged code")
	}
	if !strings.Contains(res.MergedCode, `"fmt"`) {
		t.Errorf("missing fmt import in merged code")
	}

	// Both function updates should be preserved
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
