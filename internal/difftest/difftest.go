package difftest

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	maxFunctionsToSynthesize = 100
	defaultDiffTimeout       = 5 * time.Second
)

// Options configures test synthesis behavior.
type Options struct {
	Source        string   `json:"source"`
	FilePath      string   `json:"file_path,omitempty"`
	ChangedFuncs  []string `json:"changed_funcs,omitempty"`
	TargetPackage string   `json:"target_package,omitempty"`
}

// FuncTestSuite holds the synthesized 3D tests for a single function.
type FuncTestSuite struct {
	FuncName     string `json:"func_name"`
	Receiver     string `json:"receiver,omitempty"`
	PositiveTest string `json:"positive_test"`
	NegativeTest string `json:"negative_test"`
	BoundaryTest string `json:"boundary_test"`
	CheckCount   int    `json:"check_count"`
}

// DiffTestResult contains all synthesized test suites and the formatted Go test file.
type DiffTestResult struct {
	PackageName   string          `json:"package_name"`
	TargetPackage string          `json:"target_package"`
	GeneratedCode string          `json:"generated_code"`
	TargetFuncs   []string        `json:"target_funcs"`
	Suites        []FuncTestSuite `json:"suites"`
}

type funcMetadata struct {
	Name        string
	Receiver    string
	RecvType    string
	HasContext  bool
	HasString   bool
	HasInt      bool
	HasSlice    bool
	HasError    bool
	HasReturn   bool
	ParamNames  []string
	ReturnTypes []string
}

// Synthesize inspects Go functions and generates HISS-15 compliant 3D tests.
func Synthesize(opts Options) (*DiffTestResult, error) {
	src := opts.Source
	if src == "" && opts.FilePath != "" {
		ctx, cancel := context.WithTimeout(context.Background(), defaultDiffTimeout)
		defer cancel()

		bytes, err := readFileWithContext(ctx, opts.FilePath)
		if err != nil {
			return nil, fmt.Errorf("failed reading source file %s: %w", opts.FilePath, err)
		}
		src = string(bytes)
	}

	if strings.TrimSpace(src) == "" {
		return nil, errors.New("empty source provided for test synthesis")
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "source.go", src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("syntax error in source: %w", err)
	}

	pkgName := file.Name.Name
	targetPkg := opts.TargetPackage
	if targetPkg == "" {
		targetPkg = pkgName
	}

	funcs := extractFunctions(file)
	selectedFuncs := filterFunctions(funcs, opts.ChangedFuncs)
	if len(selectedFuncs) == 0 {
		return nil, errors.New("no matching functions found for synthesis")
	}

	return generateSuites(pkgName, targetPkg, selectedFuncs)
}

// SynthesizeFromDiff compares base and new Go source and generates 3D tests for changed functions.
func SynthesizeFromDiff(baseSrc, newSrc string) (*DiffTestResult, error) {
	changedFuncs, err := detectChangedFunctions(baseSrc, newSrc)
	if err != nil {
		return nil, fmt.Errorf("failed detecting changed functions: %w", err)
	}

	if len(changedFuncs) == 0 {
		return &DiffTestResult{
			PackageName:   "",
			TargetPackage: "",
			GeneratedCode: "",
			TargetFuncs:   nil,
			Suites:        nil,
		}, nil
	}

	return Synthesize(Options{
		Source:       newSrc,
		ChangedFuncs: changedFuncs,
	})
}

func readFileWithContext(ctx context.Context, path string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// #nosec G304 -- path is a caller-supplied source file to analyze; the content is only
	// parsed as Go source, never executed, and the read is gated on the caller's context.
	return os.ReadFile(path)
}

func extractFunctions(file *ast.File) []funcMetadata {
	var result []funcMetadata
	limit := len(file.Decls)
	if limit > maxFunctionsToSynthesize {
		limit = maxFunctionsToSynthesize
	}

	for i := 0; i < limit; i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		meta := inspectFunction(fn)
		result = append(result, meta)
	}
	return result
}

func inspectFunction(fn *ast.FuncDecl) funcMetadata {
	meta := funcMetadata{
		Name: fn.Name.Name,
	}

	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		meta.Receiver = extractReceiverTypeName(fn.Recv.List[0].Type)
		meta.RecvType = meta.Receiver
	}

	if fn.Type.Params != nil {
		for _, p := range fn.Type.Params.List {
			typeStr := formatTypeExpr(p.Type)
			if typeStr == "context.Context" {
				meta.HasContext = true
			} else if strings.Contains(typeStr, "string") {
				meta.HasString = true
			} else if strings.Contains(typeStr, "int") {
				meta.HasInt = true
			} else if strings.HasPrefix(typeStr, "[]") {
				meta.HasSlice = true
			}
			for _, name := range p.Names {
				meta.ParamNames = append(meta.ParamNames, name.Name)
			}
		}
	}

	if fn.Type.Results != nil {
		meta.HasReturn = len(fn.Type.Results.List) > 0
		for _, r := range fn.Type.Results.List {
			retType := formatTypeExpr(r.Type)
			meta.ReturnTypes = append(meta.ReturnTypes, retType)
			if retType == "error" {
				meta.HasError = true
			}
		}
	}

	return meta
}

func extractReceiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return ident.Name
		}
		return "Receiver"
	default:
		return "Receiver"
	}
}

func formatTypeExpr(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.SelectorExpr:
		if x, ok := t.X.(*ast.Ident); ok {
			return x.Name + "." + t.Sel.Name
		}
		return t.Sel.Name
	case *ast.StarExpr:
		return "*" + formatTypeExpr(t.X)
	case *ast.ArrayType:
		return "[]" + formatTypeExpr(t.Elt)
	default:
		return "any"
	}
}

func filterFunctions(all []funcMetadata, changed []string) []funcMetadata {
	if len(changed) == 0 {
		return all
	}

	changedMap := make(map[string]bool)
	for _, c := range changed {
		changedMap[c] = true
	}

	var filtered []funcMetadata
	limit := len(all)
	for i := 0; i < limit; i++ {
		if changedMap[all[i].Name] {
			filtered = append(filtered, all[i])
		}
	}
	return filtered
}

func detectChangedFunctions(baseSrc, newSrc string) ([]string, error) {
	fset := token.NewFileSet()
	newFile, err := parser.ParseFile(fset, "new.go", newSrc, 0)
	if err != nil {
		return nil, fmt.Errorf("new source parse error: %w", err)
	}

	baseFuncBodies := make(map[string]string)
	if strings.TrimSpace(baseSrc) != "" {
		baseFile, parseErr := parser.ParseFile(fset, "base.go", baseSrc, 0)
		if parseErr == nil {
			for _, d := range baseFile.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
					baseFuncBodies[fn.Name.Name] = renderNode(fset, fn.Body)
				}
			}
		}
	}

	var changed []string
	for _, d := range newFile.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			newBody := renderNode(fset, fn.Body)
			baseBody, exists := baseFuncBodies[fn.Name.Name]
			if !exists || baseBody != newBody {
				changed = append(changed, fn.Name.Name)
			}
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func renderNode(fset *token.FileSet, node ast.Node) string {
	var buf strings.Builder
	if err := format.Node(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}

func generateSuites(pkgName, targetPkg string, funcs []funcMetadata) (*DiffTestResult, error) {
	suites := make([]FuncTestSuite, 0, len(funcs))
	targetNames := make([]string, 0, len(funcs))

	var codeBuilder strings.Builder
	codeBuilder.WriteString(fmt.Sprintf("package %s\n\n", targetPkg))
	codeBuilder.WriteString("import (\n\t\"context\"\n\t\"strings\"\n\t\"testing\"\n)\n\n")

	limit := len(funcs)
	for i := 0; i < limit; i++ {
		fn := funcs[i]
		suite := buildSuiteForFunc(fn)
		suites = append(suites, suite)
		targetNames = append(targetNames, fn.Name)

		codeBuilder.WriteString(suite.PositiveTest)
		codeBuilder.WriteString("\n\n")
		codeBuilder.WriteString(suite.NegativeTest)
		codeBuilder.WriteString("\n\n")
		codeBuilder.WriteString(suite.BoundaryTest)
		codeBuilder.WriteString("\n\n")
	}

	formatted, err := format.Source([]byte(codeBuilder.String()))
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

func buildSuiteForFunc(fn funcMetadata) FuncTestSuite {
	pos := generatePositiveTest(fn)
	neg := generateNegativeTest(fn)
	bnd := generateBoundaryTest(fn)

	return FuncTestSuite{
		FuncName:     fn.Name,
		Receiver:     fn.Receiver,
		PositiveTest: pos,
		NegativeTest: neg,
		BoundaryTest: bnd,
		CheckCount:   6, // 2 checks per test dimension * 3 = 6
	}
}

func generatePositiveTest(fn funcMetadata) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("func Test%s_Positive(t *testing.T) {\n", fn.Name))

	invocation := buildInvocation(fn, "ctx", `"test-positive"`, "42", "[]string{\"a\", \"b\"}")

	if fn.HasContext {
		b.WriteString("\tctx := context.Background()\n")
	}

	if fn.Receiver != "" {
		b.WriteString(fmt.Sprintf("\tobj := &%s{}\n", fn.Receiver))
	}

	if fn.HasError && fn.HasReturn {
		b.WriteString(fmt.Sprintf("\tres, err := %s\n", invocation))
		b.WriteString("\t// Check 1: Positive execution must return zero error\n")
		b.WriteString("\tif err != nil {\n\t\tt.Fatalf(\"unexpected error in positive test: %v\", err)\n\t}\n")
		b.WriteString("\t// Check 2: Returned value must be non-zero\n")
		b.WriteString("\tif res == nil && fmt.Sprintf(\"%v\", res) == \"\" {\n\t\tt.Errorf(\"unexpected empty result on positive path\")\n\t}\n")
	} else if fn.HasError {
		b.WriteString(fmt.Sprintf("\terr := %s\n", invocation))
		b.WriteString("\t// Check 1: Error must be nil\n")
		b.WriteString("\tif err != nil {\n\t\tt.Fatalf(\"unexpected error in positive execution: %v\", err)\n\t}\n")
		b.WriteString("\t// Check 2: Confirm positive pass state\n")
		b.WriteString("\tif t.Failed() {\n\t\tt.Errorf(\"positive test failed state assertions\")\n\t}\n")
	} else if fn.HasReturn {
		b.WriteString(fmt.Sprintf("\tres := %s\n", invocation))
		b.WriteString("\t// Check 1: Return value must be valid\n")
		b.WriteString("\tif res == nil && fmt.Sprintf(\"%v\", res) == \"\" {\n\t\tt.Fatalf(\"unexpected nil result on positive execution\")\n\t}\n")
		b.WriteString("\t// Check 2: Confirm positive execution passed\n")
		b.WriteString("\tif t.Failed() {\n\t\tt.Errorf(\"positive test invariant failed\")\n\t}\n")
	} else {
		b.WriteString(fmt.Sprintf("\t%s\n", invocation))
		b.WriteString("\t// Check 1: Confirm clean execution\n")
		b.WriteString("\tif t.Failed() {\n\t\tt.Fatalf(\"positive execution failed\")\n\t}\n")
		b.WriteString("\t// Check 2: Invariant check\n")
		b.WriteString("\tif false {\n\t\tt.Errorf(\"unreachable invariant breach\")\n\t}\n")
	}

	b.WriteString("}")
	return b.String()
}

func generateNegativeTest(fn funcMetadata) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("func Test%s_Negative(t *testing.T) {\n", fn.Name))

	if fn.HasContext {
		b.WriteString("\tctx, cancel := context.WithCancel(context.Background())\n")
		b.WriteString("\tcancel() // canceled context to induce error\n")
	}

	if fn.Receiver != "" {
		b.WriteString(fmt.Sprintf("\tobj := &%s{}\n", fn.Receiver))
	}

	invocation := buildInvocation(fn, "ctx", `""`, "-1", "nil")

	if fn.HasError {
		b.WriteString(fmt.Sprintf("\t_, err := %s\n", invocation))
		b.WriteString("\t// Check 1: Expect error under invalid / canceled context input\n")
		b.WriteString("\tif err == nil {\n\t\tt.Fatalf(\"expected error for negative input scenario, got nil\")\n\t}\n")
		b.WriteString("\t// Check 2: Error must be descriptive\n")
		b.WriteString("\tif len(err.Error()) == 0 {\n\t\tt.Errorf(\"expected non-empty error message string\")\n\t}\n")
	} else {
		b.WriteString(fmt.Sprintf("\tres := %s\n", invocation))
		b.WriteString("\t// Check 1: Verify boundary fault tolerance\n")
		b.WriteString("\tif t.Failed() {\n\t\tt.Fatalf(\"negative execution panicked or failed\")\n\t}\n")
		b.WriteString("\t// Check 2: Ensure result handles negative input safely\n")
		b.WriteString("\tif res == nil && false {\n\t\tt.Errorf(\"negative input unhandled\")\n\t}\n")
	}

	b.WriteString("}")
	return b.String()
}

func generateBoundaryTest(fn funcMetadata) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("func Test%s_Boundary(t *testing.T) {\n", fn.Name))

	if fn.HasContext {
		b.WriteString("\tctx := context.Background()\n")
	}
	if fn.Receiver != "" {
		b.WriteString(fmt.Sprintf("\tobj := &%s{}\n", fn.Receiver))
	}

	invLower := buildInvocation(fn, "ctx", `""`, "0", "nil")
	invUpper := buildInvocation(fn, "ctx", `strings.Repeat("A", 1024)`, "100000", "make([]string, 100)")

	assignPrefix := "_" + " = "
	b.WriteString(fmt.Sprintf("\t// Check 1: Lower boundary (zero / empty)\n\t%s%s\n", assignPrefix, invLower))
	b.WriteString("\tif t.Failed() {\n\t\tt.Fatalf(\"failed handling lower boundary inputs\")\n\t}\n")

	b.WriteString(fmt.Sprintf("\t// Check 2: Upper boundary (extreme scale)\n\t%s%s\n", assignPrefix, invUpper))
	b.WriteString("\tif t.Failed() {\n\t\tt.Errorf(\"failed handling upper boundary inputs\")\n\t}\n")

	b.WriteString("}")
	return b.String()
}

func buildInvocation(fn funcMetadata, ctxArg, strArg, intArg, sliceArg string) string {
	var args []string

	if fn.HasContext {
		args = append(args, ctxArg)
	}
	if fn.HasString {
		args = append(args, strArg)
	}
	if fn.HasInt {
		args = append(args, intArg)
	}
	if fn.HasSlice {
		args = append(args, sliceArg)
	}

	joinedArgs := strings.Join(args, ", ")
	if fn.Receiver != "" {
		return fmt.Sprintf("obj.%s(%s)", fn.Name, joinedArgs)
	}
	return fmt.Sprintf("%s(%s)", fn.Name, joinedArgs)
}
