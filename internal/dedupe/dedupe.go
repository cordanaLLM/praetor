package dedupe

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// JscpdVersion is the pinned jscpd version required for foreign clone detection.
const JscpdVersion = "5.4.2"

// FileLocation points to a file and line number.
type FileLocation struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	FuncName string `json:"func_name,omitempty"`
	Root     string `json:"root,omitempty"`
	Expired  string `json:"expired,omitempty"`
}

// ExceptionEntry records one manifest exception that excuses a duplicate block.
type ExceptionEntry struct {
	Path    string `json:"path"`
	Reason  string `json:"reason"`
	Expires string `json:"expires"`
}

// DuplicateGroup contains instances of duplicated code.
type DuplicateGroup struct {
	Language   string           `json:"language,omitempty"`
	Hash       string           `json:"hash"`
	LOC        int              `json:"loc"`
	Locations  []FileLocation   `json:"locations"`
	Exceptions []ExceptionEntry `json:"exceptions,omitempty"`
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
	TotalFilesScanned     int              `json:"total_files_scanned"`
	TotalFuncsScanned     int              `json:"total_funcs_scanned"`
	SkippedGeneratedFiles int              `json:"skipped_generated_files,omitempty"`
	Duplicates            []DuplicateGroup `json:"duplicates"`
	Excepted              []DuplicateGroup `json:"excepted,omitempty"`
	SprawlItems           []SprawlItem     `json:"sprawl_items"`
	StaleExceptions       []string         `json:"stale_exceptions,omitempty"`
	NotCheckedExceptions  []string         `json:"not_checked_exceptions,omitempty"`
	CleanlinessScore      float64          `json:"cleanliness_score"`
	Passed                bool             `json:"passed"`
	// Applicable reports whether there was anything to scan. This detector reads Go sources
	// only, so a repository with none is not clean -- it is unexamined, and the difference
	// matters to every caller that reads the score.
	Applicable bool `json:"applicable"`
	// Unscanned counts, per language (util.SourceLanguage), the repository's source files
	// this detector cannot read. HISS-19 is not measured for them: a clean verdict over the
	// Go files of a polyglot repository says nothing about the rest (#161).
	Unscanned map[string]int `json:"unscanned,omitempty"`
	// Partial reports a verdict that covers the Go sources only while the repository also
	// holds source listed in Unscanned.
	Partial bool `json:"partial"`

	// ForeignDuplicates holds duplicate groups discovered by the foreign scanner (jscpd).
	// Foreign duplicate groups are reported, not gated (they do not alter CleanlinessScore or Passed).
	ForeignDuplicates []DuplicateGroup `json:"foreign_duplicates,omitempty"`
	// ForeignExcepted holds foreign groups fully excused by live HISS-19 exceptions.
	ForeignExcepted []DuplicateGroup `json:"foreign_excepted,omitempty"`
	// ForeignScanner records the name and version of the foreign scanner when executed.
	ForeignScanner string `json:"foreign_scanner,omitempty"`
	// ForeignSkipped records why the foreign scan was skipped, if applicable.
	ForeignSkipped string `json:"foreign_skipped,omitempty"`
	// ForeignScanned counts source files per language scanned by the foreign scanner (reported, not gated).
	ForeignScanned map[string]int `json:"foreign_scanned,omitempty"`
}

// UnscannedLanguages returns the languages in Unscanned, sorted, so a report lists them in
// the same order on every run.
func (r *DedupeReport) UnscannedLanguages() []string {
	languages := make([]string, 0, len(r.Unscanned))
	for language := range r.Unscanned {
		languages = append(languages, language)
	}
	sort.Strings(languages)
	return languages
}

// ScanOptions configures a repository scan for AST clones and utility sprawl.
type ScanOptions struct {
	// RepoPath is the root of the repository to scan.
	RepoPath string
	// AcrossRoots are additional repository roots to scan for cross-root clones.
	AcrossRoots []string
	// Exceptions are HISS-19 exception entries. If nil, they are loaded from .standards.yaml in RepoPath.
	Exceptions []config.Exception
	// Today is the date against which exception expiry is judged. If zero, time.Now() is used.
	Today time.Time
}

// ScanRepo scans a repository for function-level clones and utility sprawl.
func ScanRepo(repoPath string) (*DedupeReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	return ScanRepoContext(ctx, repoPath)
}

// ScanRepoContext scans tracked and nonignored working tree Go sources and counts the source
// files in other languages it cannot read (DedupeReport.Unscanned). Non-Git directories
// retain a filesystem scan. Incomplete scans return an error.
func ScanRepoContext(ctx context.Context, repoPath string) (*DedupeReport, error) {
	return ScanRepoWithOptions(ctx, ScanOptions{RepoPath: repoPath})
}

func normalizeScanOptions(opts ScanOptions) (string, time.Time) {
	repoPath := opts.RepoPath
	if repoPath == "" {
		repoPath = "."
	}
	today := opts.Today
	if today.IsZero() {
		today = time.Now()
	}
	return repoPath, today
}

// ScanRepoWithOptions scans tracked and nonignored working tree Go sources with explicit scan options.
func ScanRepoWithOptions(ctx context.Context, opts ScanOptions) (*DedupeReport, error) {
	if ctx == nil {
		return nil, errors.New("dedupe scan requires a context")
	}
	repoPath, today := normalizeScanOptions(opts)
	exceptions, err := resolveScanExceptions(ctx, repoPath, opts.Exceptions, today)
	if err != nil {
		return nil, err
	}

	scope, err := sourceFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	engineMode, err := isEngineRepo(repoPath)
	if err != nil {
		return nil, err
	}
	report := &DedupeReport{
		Duplicates:           make([]DuplicateGroup, 0),
		Excepted:             make([]DuplicateGroup, 0),
		SprawlItems:          make([]SprawlItem, 0),
		StaleExceptions:      make([]string, 0),
		NotCheckedExceptions: make([]string, 0),
		Unscanned:            scope.unscanned,
	}

	fset := token.NewFileSet()
	funcHashMap := make(map[string][]FileLocation)
	funcLocMap := make(map[string]int)
	var adopterStarts []adopterGitStart

	for _, relPath := range scope.goFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := scanGoFile(ctx, fset, filepath.Join(repoPath, relPath), relPath, funcHashMap, funcLocMap, engineMode, &adopterStarts, report); err != nil {
			return nil, err
		}
	}

	if !engineMode {
		resolveAdopterGitSprawl(adopterStarts, report)
	}

	used := make([]bool, len(exceptions))
	collectDuplicates(funcHashMap, funcLocMap, exceptions, today, used, report)
	if err := scanForeign(ctx, repoPath, opts.AcrossRoots, scope, report); err != nil {
		return nil, fmt.Errorf("scan foreign duplicates: %w", err)
	}
	classifyForeignDuplicates(repoPath, exceptions, today, used, report)
	collectStaleExceptions(exceptions, used, report)
	calculateScore(report)
	return report, nil
}

func resolveScanExceptions(ctx context.Context, repoPath string, explicit []config.Exception, today time.Time) ([]config.Exception, error) {
	exceptions := explicit
	if exceptions == nil {
		var err error
		exceptions, err = config.LoadExceptionsFor(ctx, repoPath, config.ExceptionRuleDedupe)
		if err != nil {
			return nil, fmt.Errorf("load HISS-19 exceptions: %w", err)
		}
	}
	exceptions = config.ExceptionsFor(exceptions, config.ExceptionRuleDedupe)
	if err := ValidateExceptions(ctx, repoPath, exceptions, today); err != nil {
		return nil, fmt.Errorf("validate dedupe exceptions: %w", err)
	}
	return exceptions, nil
}

// ValidateExceptions validates the HISS-19 exceptions against the repository at repoPath:
// each entry must pass config.ValidateExceptions, and its target must name an existing regular file.
func ValidateExceptions(ctx context.Context, repoPath string, exceptions []config.Exception, today time.Time) error {
	if ctx == nil {
		return errors.New("validate exceptions requires a context")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := config.ValidateExceptions(exceptions, today); err != nil {
		return err
	}
	for index := 0; index < len(exceptions) && index < config.MaxExceptions; index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := validateExceptionTarget(repoPath, exceptions[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateExceptionTarget(repoPath string, entry config.Exception) error {
	target := filepath.Join(repoPath, entry.Path)
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("exceptions entry %s (%s): target file does not exist", entry.Path, config.ExceptionRuleDedupe)
		}
		return fmt.Errorf("exceptions entry %s (%s): inspect target: %w", entry.Path, config.ExceptionRuleDedupe, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("exceptions entry %s (%s): target is not a regular file", entry.Path, config.ExceptionRuleDedupe)
	}
	return nil
}

func scanGoFile(ctx context.Context, fset *token.FileSet, path, relPath string, hashMap map[string][]FileLocation, locMap map[string]int, engineMode bool, adopterStarts *[]adopterGitStart, report *DedupeReport) error {
	content, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return fmt.Errorf("read dedupe source %s: %w", relPath, err)
	}
	node, err := parser.ParseFile(fset, path, content, parser.ParseComments)
	if err != nil {
		return fmt.Errorf("parse dedupe source %s: %w", relPath, err)
	}

	if util.IsGeneratedGoSource(node) {
		report.SkippedGeneratedFiles++
		return nil
	}
	report.TotalFilesScanned++

	if engineMode {
		checkUtilitySprawl(fset, node, relPath, report)
	} else {
		collectAdopterGitStarts(fset, node, relPath, adopterStarts)
	}

	imports := fileImports(node)

	for _, decl := range node.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil || len(fn.Body.List) < minCloneStatements {
			continue
		}
		report.TotalFuncsScanned++

		hashKey, lines, err := cloneKey(fset, fn, imports)
		if err != nil {
			return fmt.Errorf("print dedupe source %s: %w", relPath, err)
		}
		if lines < minCloneLines {
			continue
		}
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

const (
	// minCloneStatements and minCloneLines are the smallest function body the clone detector
	// hashes, so a trivial accessor never becomes a finding. A four-line helper copied into
	// several packages is still below them. Lowering them to two statements and four lines
	// was measured on this repository with local renaming in place: 13 further clone groups,
	// among them eight forge-driver stubs that differ only by receiver type. The scan gates
	// verify-all, so the floor moves in its own change with those findings resolved.
	minCloneStatements = 3
	minCloneLines      = 5
)

// cloneKey hashes a function body with its locals renamed canonically and its imported
// package selectors normalized, returning the key and the printed body's line count.
//
// The key used to be the printed body text, so renaming one local variable in a copied
// function produced a different hash and the pair stopped being a clone group: the cheapest
// possible evasion of the rule. Parameters, results, the receiver and every local are now
// renamed before printing, numbered in the order the body first uses them, so two bodies that
// differ only in what their locals are called print identically, while a body that uses a
// different local in the same place still differs. Numbering by use rather than declaration
// keeps an unused receiver or parameter from shifting every later name. Imported package
// identifiers on the left of selector expressions are normalised to the import path so that
// an import alias does not hide a copy. The renamed body is only hashed, never shown.
func cloneKey(fset *token.FileSet, fn *ast.FuncDecl, imports map[string]string) (string, int, error) {
	locals := localNames(fn)
	normalizeImportSelectors(fn.Body, imports, locals)
	renameLocals(fn.Body, locals)
	var buf strings.Builder
	if err := printer.Fprint(&buf, fset, fn.Body); err != nil {
		return "", 0, err
	}
	body := strings.TrimSpace(buf.String())
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:8]), strings.Count(body, "\n") + 1, nil
}

// localNames returns the names the function declares for itself.
func localNames(fn *ast.FuncDecl) map[string]bool {
	c := localCollector{names: make(map[string]bool)}
	c.fields(fn.Recv)
	c.fields(fn.Type.Params)
	c.fields(fn.Type.Results)
	ast.Inspect(fn.Body, c.visit)
	return c.names
}

type localCollector struct {
	names map[string]bool
}

func (c *localCollector) add(expr ast.Expr) {
	if ident, ok := expr.(*ast.Ident); ok && ident != nil && ident.Name != "_" {
		c.names[ident.Name] = true
	}
}

func (c *localCollector) fields(list *ast.FieldList) {
	if list == nil {
		return
	}
	for _, field := range list.List {
		for _, name := range field.Names {
			c.add(name)
		}
	}
}

// visit records the declarations a body can make: short variable declarations, var and const
// specs, range variables, closure parameters and labels.
func (c *localCollector) visit(n ast.Node) bool {
	switch node := n.(type) {
	case *ast.AssignStmt:
		if node.Tok == token.DEFINE {
			for _, lhs := range node.Lhs {
				c.add(lhs)
			}
		}
	case *ast.ValueSpec:
		for _, name := range node.Names {
			c.add(name)
		}
	case *ast.RangeStmt:
		if node.Tok == token.DEFINE {
			c.add(node.Key)
			c.add(node.Value)
		}
	case *ast.FuncLit:
		c.fields(node.Type.Params)
		c.fields(node.Type.Results)
	case *ast.LabeledStmt:
		c.add(node.Label)
	}
	return true
}

// renameLocals rewrites every use of a local in body to a placeholder numbered by first use.
// The placeholder is not a valid Go identifier, so it cannot collide with a package-level name
// the body also uses. A selector's member and a composite literal's key name a field rather
// than a local, so they keep their names: renaming them would merge two bodies that read
// different fields.
func renameLocals(body *ast.BlockStmt, locals map[string]bool) {
	if len(locals) == 0 {
		return
	}
	members := make(map[*ast.Ident]bool)
	placeholders := make(map[string]string, len(locals))
	ast.Inspect(body, func(n ast.Node) bool {
		switch node := n.(type) {
		case *ast.SelectorExpr:
			members[node.Sel] = true
		case *ast.KeyValueExpr:
			if key, ok := node.Key.(*ast.Ident); ok {
				members[key] = true
			}
		case *ast.Ident:
			if locals[node.Name] && !members[node] {
				node.Name = placeholder(placeholders, node.Name)
			}
		}
		return true
	})
}

// placeholder returns the placeholder already given to name, or the next free one.
func placeholder(assigned map[string]string, name string) string {
	if canonical, ok := assigned[name]; ok {
		return canonical
	}
	canonical := "$" + strconv.Itoa(len(assigned))
	assigned[name] = canonical
	return canonical
}

// normalizeImportSelectors replaces package identifiers on the left of selector expressions
// with their import path, unless the identifier is shadowed by a local variable or parameter.
func normalizeImportSelectors(body *ast.BlockStmt, imports map[string]string, locals map[string]bool) {
	if body == nil || len(imports) == 0 {
		return
	}
	ast.Inspect(body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		xIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if importPath, ok := imports[xIdent.Name]; ok && !locals[xIdent.Name] {
			xIdent.Name = importPath
		}
		return true
	})
}

const maxFileImports = 4096

// fileImports maps the package identifiers a file's selector expressions use to their import paths.
// Unaliased imports map by their default package name, aliased imports map by their alias.
// Blank and dot imports are omitted because they bind no selector prefix.
func fileImports(file *ast.File) map[string]string {
	if file == nil {
		return nil
	}
	specs := util.GoImportSpecs(file)
	if len(specs) == 0 {
		return nil
	}
	imports := make(map[string]string, len(specs))
	for i := 0; i < len(specs) && i < maxFileImports; i++ {
		spec := specs[i]
		name := spec.Name
		if name == "" {
			name = hiss.DefaultImportName(spec.Path)
		}
		if name != "" && name != "_" && name != "." {
			imports[name] = spec.Path
		}
	}
	return imports
}

func checkUtilitySprawl(fset *token.FileSet, node *ast.File, relPath string, report *DedupeReport) {
	if strings.HasPrefix(relPath, "internal/util") || managedAsset(relPath) {
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

// managedAsset reports whether relPath is an asset of a managed asset family
// (internal/managedasset): a file adoption writes into another repository, which has no
// internal/util, so the audited helpers cannot be called from it. The Go API compatibility
// gate, tools/apicompat/gate/main.go, is such a standalone program; .golangci.yml exempts the
// same file from the forbidigo rule this check mirrors.
func managedAsset(relPath string) bool {
	families := managedasset.Families()
	for index := 0; index < len(families) && index < managedasset.MaxFamilies; index++ {
		if slices.Contains(families[index].AssetPaths(), relPath) {
			return true
		}
	}
	return false
}

// gitSprawlReplacement names the audited git entry points because no single one answers for
// every call. util.RunGit and util.RunGitBytes (its bounded-output form) run under the
// caller's environment and the repository's own configuration, so recommending them alone
// reproduced the defect this rule exists to prevent -- a scanned tree's core.fsmonitor
// command executing during the scan. util.RunGitProbe scrubs that environment but is
// read-only by construction (internal/util/git_probe.go forces a five-second deadline and
// core.hooksPath=devnull), so recommending it alone tells a clone, fetch or commit to
// silently drop its hooks and die at five seconds. The .golangci.yml forbidigo rule for the
// same call names the same helpers (plus util.RunCommand, which answers for a non-git
// binary), and TestScanRepo_Boundary_SprawlAdviceMatchesTheLinterRule reads this constant's
// helper names and requires that message to carry each of them, so the two cannot drift
// apart unnoticed.
const gitSprawlReplacement = "util.RunGit(ctx, repoPath, ...) or util.RunGitBytes(ctx, repoPath, maxBytes, ...), " +
	"or util.RunGitProbe(ctx, repoPath, maxBytes, ...) for a read-only inspection of a repository Praetor does not own"

// gitCallForm records where a command constructor keeps the executable name and how to
// print the call.
type gitCallForm struct {
	nameIndex int
	pattern   string
}

// gitCallForms lists every constructor that can run git outside the audited git helpers,
// keyed by the package-qualified name the call is written with. exec.Command takes the name
// first and exec.CommandContext takes the context first. util.RunCommand and
// util.RunCommandBytes are audited exec entry points, but naming "git" to them bypasses the
// git helpers, which is how four call sites stayed out of this report (BUG-818): the rule
// matched the exec package only. Inside internal/util they are the implementation, so
// checkUtilitySprawl never inspects that package.
var gitCallForms = map[string]gitCallForm{
	"exec.Command":         {nameIndex: 0, pattern: `exec.Command("git", ...)`},
	"exec.CommandContext":  {nameIndex: 1, pattern: `exec.CommandContext(ctx, "git", ...)`},
	"util.RunCommand":      {nameIndex: 2, pattern: `util.RunCommand(ctx, dir, "git", ...)`},
	"util.RunCommandBytes": {nameIndex: 2, pattern: `util.RunCommandBytes(ctx, dir, "git", maxBytes, ...)`},
}

// matchGitCall checks whether call matches one of gitCallForms and runs git.
func matchGitCall(call *ast.CallExpr) (gitCallForm, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return gitCallForm{}, false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return gitCallForm{}, false
	}
	form, ok := gitCallForms[ident.Name+"."+sel.Sel.Name]
	if !ok || len(call.Args) <= form.nameIndex {
		return gitCallForm{}, false
	}
	lit, ok := call.Args[form.nameIndex].(*ast.BasicLit)
	if !ok || lit.Value != `"git"` {
		return gitCallForm{}, false
	}
	return form, true
}

// checkAdHocGit reports a git execution that bypasses the audited git helpers and names the
// helper to use instead.
//
// The name index comes from gitCallForms. Asserting a literal "git" at Args[0] for every
// constructor left the CommandContext arm dead by construction -- Args[0] is then the
// context expression, never a BasicLit -- so exec.CommandContext(ctx, "git", ...) passed the
// name check and was dropped one line later (BUG-755, and one clause of BUG-818).
func checkAdHocGit(call *ast.CallExpr, relPath string, line int, report *DedupeReport) {
	form, ok := matchGitCall(call)
	if !ok {
		return
	}
	report.SprawlItems = append(report.SprawlItems, SprawlItem{
		File:        relPath,
		Line:        line,
		Pattern:     form.pattern,
		Replacement: gitSprawlReplacement,
	})
}

// isEngineRepo reports whether the repository at repoPath declares the engine module path.
// When go.mod is missing or declares no module path, it reports false. Any other error
// reading go.mod is wrapped and returned.
func isEngineRepo(repoPath string) (bool, error) {
	modPath, err := gomanifest.ReadModulePath(repoPath)
	if err == nil {
		return modPath == gomanifest.EngineModulePath, nil
	}
	if errors.Is(err, os.ErrNotExist) || errors.Is(err, gomanifest.ErrNoModulePath) {
		return false, nil
	}
	return false, fmt.Errorf("read module path: %w", err)
}

// maxAdopterGitStarts bounds recorded direct git invocations in an adopted repository (HISS-02).
const maxAdopterGitStarts = 10000

// maxDeclsPerFile bounds declarations inspected per file (HISS-02).
const maxDeclsPerFile = 4096

// adopterGitStart records one direct git invocation outside audited engine helpers.
type adopterGitStart struct {
	file     string
	line     int
	pattern  string
	funcName string
	funcLine int
}

// adopterGitFunction tracks a function or package site that invokes git directly.
type adopterGitFunction struct {
	file      string
	funcName  string
	firstLine int
	count     int
}

func collectAdopterGitStarts(fset *token.FileSet, node *ast.File, relPath string, starts *[]adopterGitStart) {
	if managedAsset(relPath) {
		return
	}
	for i := 0; i < len(node.Decls) && i < maxDeclsPerFile && len(*starts) < maxAdopterGitStarts; i++ {
		collectDeclGitStarts(fset, node.Decls[i], relPath, starts)
	}
}

func collectDeclGitStarts(fset *token.FileSet, decl ast.Decl, relPath string, starts *[]adopterGitStart) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		name := enclosingFuncDeclName(d)
		fLine := fset.Position(d.Pos()).Line
		ast.Inspect(d, func(n ast.Node) bool {
			recordCallGitStart(fset, n, relPath, name, fLine, starts)
			return true
		})
	case *ast.GenDecl:
		name := filepath.Base(relPath)
		ast.Inspect(d, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				pos := fset.Position(call.Pos())
				recordCallGitStart(fset, call, relPath, name, pos.Line, starts)
			}
			return true
		})
	}
}

func recordCallGitStart(fset *token.FileSet, n ast.Node, relPath, funcName string, funcLine int, starts *[]adopterGitStart) {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return
	}
	form, ok := matchGitCall(call)
	if !ok || len(*starts) >= maxAdopterGitStarts {
		return
	}
	pos := fset.Position(call.Pos())
	*starts = append(*starts, adopterGitStart{
		file:     relPath,
		line:     pos.Line,
		pattern:  form.pattern,
		funcName: funcName,
		funcLine: funcLine,
	})
}

func enclosingFuncDeclName(fn *ast.FuncDecl) string {
	if fn == nil || fn.Name == nil {
		return ""
	}
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		if recvType, ok := hiss.ReceiverTypeName(fn.Recv.List[0].Type); ok && recvType != "" {
			return recvType + "." + fn.Name.Name
		}
	}
	return fn.Name.Name
}

func resolveAdopterGitSprawl(starts []adopterGitStart, report *DedupeReport) {
	if len(starts) == 0 {
		return
	}
	candidates := groupGitCandidates(starts)
	if len(candidates) <= 1 {
		return
	}
	sortGitCandidates(candidates)
	helper := candidates[0]
	reportAdopterSprawl(starts, helper, report)
}

func groupGitCandidates(starts []adopterGitStart) []adopterGitFunction {
	type candidateKey struct {
		file     string
		funcName string
	}
	groups := make(map[candidateKey]*adopterGitFunction)
	var ordered []*adopterGitFunction

	for i := 0; i < len(starts) && i < maxAdopterGitStarts; i++ {
		s := starts[i]
		key := candidateKey{file: s.file, funcName: s.funcName}
		existing, found := groups[key]
		if !found {
			entry := &adopterGitFunction{
				file:      s.file,
				funcName:  s.funcName,
				firstLine: s.funcLine,
				count:     1,
			}
			groups[key] = entry
			ordered = append(ordered, entry)
		} else {
			existing.count++
		}
	}

	result := make([]adopterGitFunction, len(ordered))
	for i := 0; i < len(ordered) && i < maxAdopterGitStarts; i++ {
		result[i] = *ordered[i]
	}
	return result
}

func sortGitCandidates(candidates []adopterGitFunction) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].count != candidates[j].count {
			return candidates[i].count > candidates[j].count
		}
		if candidates[i].file != candidates[j].file {
			return candidates[i].file < candidates[j].file
		}
		return candidates[i].firstLine < candidates[j].firstLine
	})
}

func reportAdopterSprawl(starts []adopterGitStart, helper adopterGitFunction, report *DedupeReport) {
	replacement := fmt.Sprintf("route through the repository's one git helper, %s:%s (HISS-19: one behaviour, one implementation)", helper.file, helper.funcName)
	for i := 0; i < len(starts) && i < maxAdopterGitStarts; i++ {
		s := starts[i]
		if s.file == helper.file && s.funcName == helper.funcName {
			continue
		}
		report.SprawlItems = append(report.SprawlItems, SprawlItem{
			File:        s.file,
			Line:        s.line,
			Pattern:     s.pattern,
			Replacement: replacement,
		})
	}
	sort.Slice(report.SprawlItems, func(i, j int) bool {
		if report.SprawlItems[i].File != report.SprawlItems[j].File {
			return report.SprawlItems[i].File < report.SprawlItems[j].File
		}
		return report.SprawlItems[i].Line < report.SprawlItems[j].Line
	})
}

func collectDuplicates(hashMap map[string][]FileLocation, locMap map[string]int, exceptions []config.Exception, today time.Time, used []bool, report *DedupeReport) {
	for hash, locs := range hashMap {
		if len(locs) <= 1 {
			continue
		}
		group, allExcepted := classifyDuplicateGroup(DuplicateGroup{Hash: hash, LOC: locMap[hash], Locations: locs}, "", exceptions, today, used)
		if allExcepted {
			report.Excepted = append(report.Excepted, group)
		} else {
			report.Duplicates = append(report.Duplicates, group)
		}
	}
	sort.Slice(report.Duplicates, func(i, j int) bool {
		return report.Duplicates[i].Hash < report.Duplicates[j].Hash
	})
	sort.Slice(report.Excepted, func(i, j int) bool {
		return report.Excepted[i].Hash < report.Excepted[j].Hash
	})
}

const globCharacters = "*?[]\\"

func exceptionLanguage(entry config.Exception) string {
	if entry.Path != "" {
		if lang := sourceLanguageOf(entry.Path); lang != "" {
			return lang
		}
		return "unknown"
	}
	if entry.Glob != "" {
		return globLanguage(entry.Glob)
	}
	return "unknown"
}

func globLanguage(glob string) string {
	ext := filepath.Ext(glob)
	if ext == "" || strings.ContainsAny(ext, globCharacters) {
		return "unknown"
	}
	if isWorkflowYAML(glob) {
		return "yaml"
	}
	if lang := util.SourceLanguage("file" + ext); lang != "" {
		return lang
	}
	return "unknown"
}

func isScannedLanguage(lang string, report *DedupeReport) bool {
	if lang == "" || lang == "unknown" {
		return false
	}
	if lang == goLanguage && report.TotalFilesScanned > 0 {
		return true
	}
	if report.ForeignScanned != nil {
		_, ok := report.ForeignScanned[lang]
		return ok
	}
	return false
}

func collectStaleExceptions(exceptions []config.Exception, used []bool, report *DedupeReport) {
	for i := 0; i < len(exceptions) && i < config.MaxExceptions; i++ {
		if used[i] {
			continue
		}
		classifyUnusedException(exceptions[i], report)
	}
}

func classifyUnusedException(entry config.Exception, report *DedupeReport) {
	lang := exceptionLanguage(entry)
	if isScannedLanguage(lang, report) {
		report.StaleExceptions = append(report.StaleExceptions, fmt.Sprintf(
			"exceptions entry %s (%s): excuses no duplicate function block; remove the entry",
			entry.Target(), config.ExceptionRuleDedupe))
		return
	}
	report.NotCheckedExceptions = append(report.NotCheckedExceptions, fmt.Sprintf(
		"exceptions entry %s (%s): %s not scanned by this run; not checked",
		entry.Target(), config.ExceptionRuleDedupe, lang))
}

func classifyDuplicateGroup(group DuplicateGroup, mainRoot string, exceptions []config.Exception, today time.Time, used []bool) (DuplicateGroup, bool) {
	group.Locations = append([]FileLocation(nil), group.Locations...)
	allExcepted := true
	var groupExceptions []ExceptionEntry
	seen := make(map[*config.Exception]bool)

	for i := range group.Locations {
		live := judgeLocation(&group.Locations[i], mainRoot, group.Hash, exceptions, today, used)
		if live != nil {
			if !seen[live] {
				seen[live] = true
				groupExceptions = append(groupExceptions, ExceptionEntry{
					Path:    live.Path,
					Reason:  live.Reason,
					Expires: live.Expires,
				})
			}
		} else {
			allExcepted = false
		}
	}
	if allExcepted {
		group.Exceptions = groupExceptions
	}
	return group, allExcepted
}

func judgeLocation(loc *FileLocation, mainRoot, hash string, exceptions []config.Exception, today time.Time, used []bool) *config.Exception {
	// Entries belong to the main manifest, so the same path in another root cannot use them.
	if loc.Root != "" && loc.Root != mainRoot {
		return nil
	}
	live, expired := config.DedupeExceptionFor(exceptions, loc.Path, hash, loc.Line, today, used)
	if live == nil && expired != nil {
		loc.Expired = expired.Expires
	}
	return live
}

func calculateScore(report *DedupeReport) {
	// A score over an empty set is not a result. This detector reads Go sources only, so on a
	// TypeScript, Rust or Python repository it scanned nothing -- and reporting 100% and
	// Passed for a tree it never opened is a clean bill of health nobody earned. Measured on a
	// repository with 510 TypeScript and Svelte files: zero scanned, 100.0%, passed.
	//
	// Applicable is false instead, the score stays zero, and the caller reports that the check
	// did not apply rather than that the repository is clean.
	report.Applicable = report.TotalFilesScanned > 0
	report.Partial = report.Applicable && len(report.Unscanned) > 0
	if !report.Applicable {
		report.CleanlinessScore = 0
		report.Passed = false
		return
	}
	deduction := float64(len(report.Duplicates)*15 + len(report.SprawlItems)*5)
	score := 100.0 - deduction
	if score < 0.0 {
		score = 0.0
	}
	report.CleanlinessScore = score
	// A finding the verdict never mentions is a finding nobody acts on. Five points per
	// sprawl item against an 80 threshold meant four ad-hoc utility call sites scored 80
	// and passed, so the gate reported success while listing the infractions underneath
	// it. Every sprawl item now has to be resolved or waived, exactly like a clone.
	//
	// The finding lists are the verdict, and the score only describes how far a failing
	// repository is from clean: with all three lists (Duplicates, SprawlItems, StaleExceptions)
	// empty every deduction is zero and the score is always exactly 100, so keeping "score >= 80"
	// in the condition was dead logic that another finding category would have slipped past
	// unnoticed.
	//
	// order engine-dedupe-jscpd-skip-fails-closed-20261027: any foreign clone scan skip (missing
	// jscpd, version mismatch) fails closed so missing tooling never silently bypasses non-Go clone
	// detection. Applicable and not-applicable reports both get Passed=false.
	report.Passed = len(report.Duplicates) == 0 && len(report.SprawlItems) == 0 &&
		len(report.StaleExceptions) == 0 && report.ForeignSkipped == ""
}
