package dedupe

import (
	"context"
	"go/ast"
	"go/token"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestScoreCountsSmallFunctions covers functions below the clone detection threshold.
func TestScoreCountsSmallFunctions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(path, []byte("package sample\nfunc a() {}\nfunc b() { println(1) }\n"), 0600); err != nil {
		t.Fatal(err)
	}
	report := &DedupeReport{}
	var starts []adopterGitStart
	err := scanGoFile(context.Background(), token.NewFileSet(), path, "sample.go", map[string][]FileLocation{}, map[string]int{}, false, &starts, report)
	if err != nil {
		t.Fatal(err)
	}
	if report.TotalFuncsScanned != 2 {
		t.Fatalf("scanned %d functions, want 2", report.TotalFuncsScanned)
	}
}

// TestScoreFindsSprawlFunction maps multiple git calls and a clone to one function.
func TestScoreFindsSprawlFunction(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.go")
	source := "package sample\nimport \"os/exec\"\nfunc a() {\nexec.Command(\"git\")\nexec.Command(\"git\")\n}\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	report := &DedupeReport{}
	var starts []adopterGitStart
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := scanGoFile(ctx, token.NewFileSet(), path, "sample.go", map[string][]FileLocation{}, map[string]int{}, true, &starts, report); err != nil {
		t.Fatal(err)
	}
	report.Duplicates = []DuplicateGroup{{Locations: []FileLocation{{Path: "sample.go", Line: 3}}}}
	if len(report.SprawlItems) != 2 || countFindingFunctions(report) != 1 {
		t.Fatalf("sprawl=%v participants=%d", report.SprawlItems, countFindingFunctions(report))
	}
}

// TestScoreSprawlRejectsExcessDeclarations fails closed when function mapping exceeds its bound.
func TestScoreSprawlRejectsExcessDeclarations(t *testing.T) {
	node := &ast.File{Decls: make([]ast.Decl, maxDeclsPerFile+1)}
	for i := 0; i < len(node.Decls) && i < maxDeclsPerFile+1; i++ {
		node.Decls[i] = &ast.GenDecl{Tok: token.CONST}
	}
	if err := collectSprawlForFile(token.NewFileSet(), node, "sample.go", true, nil, &DedupeReport{}); err == nil {
		t.Fatal("excess declarations accepted")
	}
}

// TestScoreSprawlSharedDeclarationLine attributes calls by their source position.
func TestScoreSprawlSharedDeclarationLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.go")
	source := "package sample\nimport \"os/exec\"\nfunc first() {\nexec.Command(\"git\") }; func second() { exec.Command(\"git\") }\n"
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	report := &DedupeReport{}
	var starts []adopterGitStart
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := scanGoFile(ctx, token.NewFileSet(), path, "sample.go", map[string][]FileLocation{}, map[string]int{}, true, &starts, report); err != nil {
		t.Fatal(err)
	}
	if len(report.SprawlItems) != 2 || report.SprawlItems[0].funcLine != 3 || report.SprawlItems[1].funcLine != 4 {
		t.Fatalf("sprawl attribution: %+v", report.SprawlItems)
	}
}
