package main

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/hiss"
)

// maxFilesToScan bounds one inspection (HISS-02).
const maxFilesToScan = 500

// inspectionFiles lists every in-scope Go file or rejects incomplete inspection.
func (s *Server) inspectionFiles(path string) ([]string, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("failed stat on %s: %w", path, err)
	}
	if !stat.IsDir() {
		file, err := s.inspectableFile(path)
		if err != nil {
			return nil, err
		}
		return []string{file}, nil
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, fmt.Errorf("failed reading directory: %w", err)
	}
	files := make([]string, 0, 10)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		if len(files) == maxFilesToScan {
			return nil, fmt.Errorf("inspection exceeds maximum of %d Go files; select a smaller scope", maxFilesToScan)
		}
		file, err := s.inspectableFile(filepath.Join(path, entry.Name()))
		if err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	return files, nil
}

// inspectableFile checks discovered children as well as explicitly requested paths.
func (s *Server) inspectableFile(path string) (string, error) {
	confined, err := s.confinePath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(confined)
	if err != nil {
		return "", fmt.Errorf("inspect file %q: %w", confined, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("inspect file %q: expected a regular file", confined)
	}
	return confined, nil
}

// inspectSymbolsAtPath inspects Go files at path without recursion.
func (s *Server) inspectSymbolsAtPath(ctx context.Context, path string) (string, error) {
	files, err := s.inspectionFiles(path)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "No Go source files found for symbol inspection.", nil
	}
	// A policy that exists but does not resolve yet reports against the HISS-04 ceiling and
	// says so, as the editor projections do; only an interrupted resolution fails the tool.
	bounds, warning, err := config.ResolveRepositoryComplexity(ctx, s.rootDir)
	if err != nil {
		return "", fmt.Errorf("resolve complexity policy for %s: %w", s.rootDir, err)
	}
	fset := token.NewFileSet()
	var b strings.Builder
	b.WriteString("=== Go AST Symbol & HISS-04 Complexity Inspection ===\n\n")
	if warning != "" {
		fmt.Fprintf(&b, "[WARN] %s\n\n", warning)
	}
	for _, file := range files {
		s.inspectSingleFile(fset, file, bounds, &b)
	}
	return b.String(), nil
}

// inspectSingleFile parses a single Go file and prints its top-level declarations.
func (s *Server) inspectSingleFile(fset *token.FileSet, filePath string, bounds config.ComplexityPolicy, b *strings.Builder) {
	node, err := parser.ParseFile(fset, filePath, nil, parser.ParseComments)
	if err != nil {
		fmt.Fprintf(b, "File: %s (Parse error: %v)\n\n", filepath.Base(filePath), err)
		return
	}

	rel, err := filepath.Rel(s.rootDir, filePath)
	if err != nil {
		rel = filePath
	}
	fmt.Fprintf(b, "File: %s (Package: %s)\n", rel, node.Name.Name)

	declLimit := len(node.Decls)
	for d := 0; d < declLimit; d++ {
		if gen, ok := node.Decls[d].(*ast.GenDecl); ok {
			writeTypeReport(gen, b)
		}
		for _, unit := range hiss.MeasureDecl(fset, node.Decls[d]) {
			writeFuncReport(fset, rel, unit, bounds, b)
		}
	}
	b.WriteString("\n")
}

// writeFuncReport prints one function's HISS-04 metrics and verdict. Function length is the
// bound the audit enforces and warns; the complexity values are measured with the scanner's
// own visitor and printed as the same report-only lines every scan entry point prints.
func writeFuncReport(fset *token.FileSet, rel string, unit hiss.FuncUnit, bounds config.ComplexityPolicy, b *strings.Builder) {
	m := unit.Metrics
	measured := unit.Measurements(fset, rel, bounds.Limits())
	fmt.Fprintf(b, "  - %s: %s | LOC: %d (<=%d) | Stmts: %d (<=%d) | Cyclo: %d (<=%d) | Cognitive: %d (<=%d) [%s]\n",
		funcLabel(unit), unit.Name, m.LOC, bounds.MaxFuncLOC, m.Statements, bounds.MaxStatements,
		m.Cyclomatic, bounds.MaxCyclomatic, m.Cognitive, bounds.MaxCognitive, funcStatus(m.LOC > bounds.MaxFuncLOC, measured))
	for _, over := range measured {
		fmt.Fprintf(b, "      %s\n", over)
	}
}

// funcLabel names the kind of function a unit measured.
func funcLabel(unit hiss.FuncUnit) string {
	fn, isDecl := unit.Node.(*ast.FuncDecl)
	switch {
	case !isDecl:
		return "Func literal"
	case fn.Recv != nil && len(fn.Recv.List) > 0:
		return "(recv) Func"
	default:
		return "Func"
	}
}

// funcStatus renders the verdict tag: the enforced length warning, then the kinds measured
// over their limits under the measurements' own severity.
func funcStatus(overLength bool, measured []hiss.Measurement) string {
	var parts []string
	if overLength {
		parts = append(parts, "HISS-04 WARN: LOC")
	}
	if len(measured) > 0 {
		kinds := make([]string, 0, len(measured))
		for _, over := range measured {
			kinds = append(kinds, string(over.Kind))
		}
		parts = append(parts, fmt.Sprintf("HISS-04 %s: %s", strings.ToUpper(string(measured[0].Severity)), strings.Join(kinds, ",")))
	}
	if len(parts) == 0 {
		return "PASS"
	}
	return strings.Join(parts, "; ")
}

// writeTypeReport prints the type names declared by a type declaration group.
func writeTypeReport(decl *ast.GenDecl, b *strings.Builder) {
	if decl.Tok != token.TYPE {
		return
	}
	for _, spec := range decl.Specs {
		if ts, ok := spec.(*ast.TypeSpec); ok {
			fmt.Fprintf(b, "  - Type: %s\n", ts.Name.Name)
		}
	}
}
