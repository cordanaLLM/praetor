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

	"github.com/cordanaLLM/praetor/internal/hiss"
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
	// CheckCount is the number of assertions the three tests make, counted as they are
	// emitted rather than asserted as a constant.
	CheckCount int `json:"check_count"`
	// SkipReason says why no tests were synthesized for the function, such as a type
	// parameter constraint no single type argument is known to satisfy. The test fields
	// are empty when it is set.
	SkipReason string `json:"skip_reason,omitempty"`
}

// DiffTestResult contains all synthesized test suites and the formatted Go test file.
type DiffTestResult struct {
	PackageName   string          `json:"package_name"`
	TargetPackage string          `json:"target_package"`
	GeneratedCode string          `json:"generated_code"`
	TargetFuncs   []string        `json:"target_funcs"`
	Suites        []FuncTestSuite `json:"suites"`
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

	funcs := extractFunctions(newSourceInfo(fset, file), file)
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

func extractFunctions(info *sourceInfo, file *ast.File) []funcMetadata {
	var result []funcMetadata
	total := len(file.Decls)

	for i := 0; i < total && len(result) < maxFunctionsToSynthesize; i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		result = append(result, inspectFunction(info, fn))
	}
	return result
}

// extractReceiverTypeName returns the receiver's declared type name, "" when the
// receiver expression names no type.
func extractReceiverTypeName(expr ast.Expr) string {
	name, ok := hiss.ReceiverTypeName(expr)
	if !ok {
		return ""
	}
	return name
}

// funcMetadataKey returns the identifier used to correlate a function across the base and
// new sources and to match it against Options.ChangedFuncs. A bare function name collides
// across receivers ((a *A) Run and (b *B) Run both key as "Run"), which let one receiver's
// diff mask the other's, so a method key is qualified with its receiver type.
func funcMetadataKey(name, receiver string) string {
	if receiver != "" {
		return receiver + "." + name
	}
	return name
}

// funcDeclKey is funcMetadataKey for a raw *ast.FuncDecl, reusing extractReceiverTypeName
// so the AST-walking callers (base-body map, changed-function detection) and the
// funcMetadata-walking callers (filterFunctions) key identically.
func funcDeclKey(fn *ast.FuncDecl) string {
	receiver := ""
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		receiver = extractReceiverTypeName(fn.Recv.List[0].Type)
	}
	return funcMetadataKey(fn.Name.Name, receiver)
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
		if changedMap[funcMetadataKey(all[i].Name, all[i].Receiver)] {
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

	baseFuncBodies := parseBaseFunctionBodies(fset, baseSrc)

	var changed []string
	for _, d := range newFile.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			key := funcDeclKey(fn)
			newBody := renderNode(fset, fn.Body)
			baseBody, exists := baseFuncBodies[key]
			if !exists || baseBody != newBody {
				changed = append(changed, key)
			}
		}
	}
	sort.Strings(changed)
	return changed, nil
}

func parseBaseFunctionBodies(fset *token.FileSet, baseSrc string) map[string]string {
	baseFuncBodies := make(map[string]string)
	if strings.TrimSpace(baseSrc) != "" {
		baseFile, parseErr := parser.ParseFile(fset, "base.go", baseSrc, 0)
		if parseErr == nil {
			for _, d := range baseFile.Decls {
				if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
					baseFuncBodies[funcDeclKey(fn)] = renderNode(fset, fn.Body)
				}
			}
		}
	}

	return baseFuncBodies
}

func renderNode(fset *token.FileSet, node ast.Node) string {
	var buf strings.Builder
	if err := format.Node(&buf, fset, node); err != nil {
		return ""
	}
	return buf.String()
}
