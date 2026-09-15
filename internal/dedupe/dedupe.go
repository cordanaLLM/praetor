package dedupe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// FileLocation points to a file and line number.
type FileLocation struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	FuncName string `json:"func_name"`
}

// DuplicateGroup contains instances of duplicated code.
type DuplicateGroup struct {
	Hash      string         `json:"hash"`
	LOC       int            `json:"loc"`
	Locations []FileLocation `json:"locations"`
}

// SprawlItem highlights ad-hoc utility implementations that should use centralized utils.
type SprawlItem struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Pattern     string `json:"pattern"`
	Replacement string `json:"replacement"`
}

// DedupeReport summarizes codebase duplication and utility sprawl.
type DedupeReport struct {
	TotalFilesScanned int              `json:"total_files_scanned"`
	TotalFuncsScanned int              `json:"total_funcs_scanned"`
	Duplicates        []DuplicateGroup `json:"duplicates"`
	SprawlItems       []SprawlItem     `json:"sprawl_items"`
	CleanlinessScore  float64          `json:"cleanliness_score"`
	Passed            bool             `json:"passed"`
}

// ScanRepo scans a repository for function-level clones and utility sprawl.
func ScanRepo(repoPath string) (*DedupeReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return ScanRepoContext(ctx, repoPath)
}

// ScanRepoContext scans tracked and nonignored working tree Go sources. Non-Git
// directories retain a filesystem scan. Incomplete scans return an error.
func ScanRepoContext(ctx context.Context, repoPath string) (*DedupeReport, error) {
	files, err := sourceFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	report := &DedupeReport{
		Duplicates:  make([]DuplicateGroup, 0),
		SprawlItems: make([]SprawlItem, 0),
	}

	fset := token.NewFileSet()
	funcHashMap := make(map[string][]FileLocation)
	funcLocMap := make(map[string]int)

	for _, relPath := range files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report.TotalFilesScanned++
		if err := scanGoFile(ctx, fset, filepath.Join(repoPath, relPath), relPath, funcHashMap, funcLocMap, report); err != nil {
			return nil, err
		}
	}

	collectDuplicates(funcHashMap, funcLocMap, report)
	calculateScore(report)
	return report, nil
}

// shouldSkipDir excludes directories whose contents are not this repository's own source.
//
// testdata is Go's own convention for inputs rather than code, and a fixture corpus is
// deliberately repetitive: a rule needing a tested and an untested copy of the same function
// must contain two near-identical files, so scanning them reports duplication that is the
// point of the fixture rather than a defect. The HISS scanner skips the same name.
func shouldSkipDir(name string) bool {
	return name == ".git" || name == "vendor" || name == "node_modules" ||
		name == ".workingdir" || name == "testdata"
}

func scanGoFile(ctx context.Context, fset *token.FileSet, path, relPath string, hashMap map[string][]FileLocation, locMap map[string]int, report *DedupeReport) error {
	content, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("read dedupe source %s: %w", relPath, err)
	}
	node, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse dedupe source %s: %w", relPath, err)
	}

	checkUtilitySprawl(fset, node, relPath, report)

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) < 3 {
			continue
		}
		report.TotalFuncsScanned++

		var buf strings.Builder
		if err := printer.Fprint(&buf, fset, fn.Body); err != nil {
			return fmt.Errorf("print dedupe source %s: %w", relPath, err)
		}
		bodyStr := strings.TrimSpace(buf.String())
		lines := strings.Count(bodyStr, "\n") + 1
		if lines < 5 {
			continue
		}

		h := sha256.Sum256([]byte(bodyStr))
		hashKey := hex.EncodeToString(h[:8])
		pos := fset.Position(fn.Pos())

		hashMap[hashKey] = append(hashMap[hashKey], FileLocation{
			Path:     relPath,
			Line:     pos.Line,
			FuncName: fn.Name.Name,
		})
		locMap[hashKey] = lines
	}
	return nil
}

func checkUtilitySprawl(fset *token.FileSet, node *ast.File, relPath string, report *DedupeReport) {
	if strings.HasPrefix(relPath, "internal/util") {
		return
	}

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}

		pos := fset.Position(call.Pos())
		checkAdHocGit(call, relPath, pos.Line, report)
		return true
	})
}

func checkAdHocGit(call *ast.CallExpr, relPath string, line int, report *DedupeReport) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}

	if ident.Name == "exec" && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext") {
		if len(call.Args) > 0 {
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Value == "\"git\"" {
				report.SprawlItems = append(report.SprawlItems, SprawlItem{
					File:        relPath,
					Line:        line,
					Pattern:     "exec.Command(\"git\", ...)",
					Replacement: "util.RunGit(ctx, repoPath, ...)",
				})
			}
		}
	}
}

func collectDuplicates(hashMap map[string][]FileLocation, locMap map[string]int, report *DedupeReport) {
	for hash, locs := range hashMap {
		if len(locs) > 1 {
			report.Duplicates = append(report.Duplicates, DuplicateGroup{
				Hash:      hash,
				LOC:       locMap[hash],
				Locations: locs,
			})
		}
	}
	sort.Slice(report.Duplicates, func(i, j int) bool {
		return report.Duplicates[i].Hash < report.Duplicates[j].Hash
	})
}

func calculateScore(report *DedupeReport) {
	deduction := float64(len(report.Duplicates)*15 + len(report.SprawlItems)*5)
	score := 100.0 - deduction
	if score < 0.0 {
		score = 0.0
	}
	report.CleanlinessScore = score
	report.Passed = score >= 80.0 && len(report.Duplicates) == 0
}
