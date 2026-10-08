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
	"github.com/cordanaLLM/praetor/internal/managedasset"
	"github.com/cordanaLLM/praetor/internal/util"
)

// FileLocation points to a file and line number.
type FileLocation struct {
	Path     string `json:"path"`
	Line     int    `json:"line"`
	FuncName string `json:"func_name"`
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
	TotalFilesScanned int              `json:"total_files_scanned"`
	TotalFuncsScanned int              `json:"total_funcs_scanned"`
	Duplicates        []DuplicateGroup `json:"duplicates"`
	Excepted          []DuplicateGroup `json:"excepted,omitempty"`
	SprawlItems       []SprawlItem     `json:"sprawl_items"`
	StaleExceptions   []string         `json:"stale_exceptions,omitempty"`
	CleanlinessScore  float64          `json:"cleanliness_score"`
	Passed            bool             `json:"passed"`
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
	// Exceptions are HISS-19 exception entries. If nil, they are loaded from .standards.yaml in RepoPath.
	Exceptions []config.Exception
	// Today is the date against which exception expiry is judged. If zero, time.Now() is used.
	Today time.Time
}

// ScanRepo scans a repository for function-level clones and utility sprawl.
func ScanRepo(repoPath string) (*DedupeReport, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
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
	exceptions := opts.Exceptions
	if exceptions == nil {
		var err error
		exceptions, err = config.LoadExceptionsFor(ctx, repoPath, config.ExceptionRuleDedupe)
		if err != nil {
			return nil, fmt.Errorf("load HISS-19 exceptions: %w", err)
		}
	}
	if err := ValidateExceptions(ctx, repoPath, exceptions, today); err != nil {
		return nil, fmt.Errorf("validate dedupe exceptions: %w", err)
	}

	scope, err := sourceFiles(ctx, repoPath)
	if err != nil {
		return nil, err
	}
	report := &DedupeReport{
		Duplicates:      make([]DuplicateGroup, 0),
		Excepted:        make([]DuplicateGroup, 0),
		SprawlItems:     make([]SprawlItem, 0),
		StaleExceptions: make([]string, 0),
		Unscanned:       scope.unscanned,
	}

	fset := token.NewFileSet()
	funcHashMap := make(map[string][]FileLocation)
	funcLocMap := make(map[string]int)

	for _, relPath := range scope.goFiles {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		report.TotalFilesScanned++
		if err := scanGoFile(ctx, fset, filepath.Join(repoPath, relPath), relPath, funcHashMap, funcLocMap, report); err != nil {
			return nil, err
		}
	}

	collectDuplicates(funcHashMap, funcLocMap, exceptions, today, report)
	calculateScore(report)
	return report, nil
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
		if err := validateExceptionTarget(repoPath, index, exceptions[index]); err != nil {
			return err
		}
	}
	return nil
}

func validateExceptionTarget(repoPath string, index int, entry config.Exception) error {
	target := filepath.Join(repoPath, entry.Path)
	info, err := os.Lstat(target)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("exceptions[%d] target file does not exist: %s", index, entry.Path)
		}
		return fmt.Errorf("exceptions[%d] inspect target %s: %w", index, entry.Path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("exceptions[%d] target %s is not a regular file", index, entry.Path)
	}
	return nil
}

// shouldSkipDir excludes directories whose contents are not this repository's own source.
//
// testdata is Go's own convention for inputs rather than code, and a fixture corpus is
// deliberately repetitive: a rule needing a tested and an untested copy of the same function
// must contain two near-identical files, so scanning them reports duplication that is the
// point of the fixture rather than a defect. The HISS scanner skips the same name.
//
// The scratch directories come from util.IsScratchDir, the list the HISS scanner and adopt's
// verification planner share: a .claude/worktrees copy of the checkout is the same source
// again, and scanning it reported every function as its own duplicate. The toolchain trees
// (util.IsToolchainTreeDir: zig-pkg/, zig-out/, .zig-cache/) hold fetched third-party packages
// and build output, not the repository's source.
func shouldSkipDir(name string) bool {
	return name == ".git" || name == "vendor" || name == "node_modules" ||
		name == "testdata" || util.IsScratchDir(name) || util.IsToolchainTreeDir(name)
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
		if !ok || fn.Body == nil || len(fn.Body.List) < minCloneStatements {
			continue
		}
		report.TotalFuncsScanned++

		hashKey, lines, err := cloneKey(fset, fn)
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

// cloneKey hashes a function body with its locals renamed canonically, returning the key and
// the printed body's line count.
//
// The key used to be the printed body text, so renaming one local variable in a copied
// function produced a different hash and the pair stopped being a clone group: the cheapest
// possible evasion of the rule. Parameters, results, the receiver and every local are now
// renamed before printing, numbered in the order the body first uses them, so two bodies that
// differ only in what their locals are called print identically, while a body that uses a
// different local in the same place still differs. Numbering by use rather than declaration
// keeps an unused receiver or parameter from shifting every later name. The renamed body is
// only hashed, never shown.
func cloneKey(fset *token.FileSet, fn *ast.FuncDecl) (string, int, error) {
	renameLocals(fn.Body, localNames(fn))
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

// checkAdHocGit reports a git execution that bypasses the audited git helpers and names the
// helper to use instead.
//
// The name index comes from gitCallForms. Asserting a literal "git" at Args[0] for every
// constructor left the CommandContext arm dead by construction -- Args[0] is then the
// context expression, never a BasicLit -- so exec.CommandContext(ctx, "git", ...) passed the
// name check and was dropped one line later (BUG-755, and one clause of BUG-818).
func checkAdHocGit(call *ast.CallExpr, relPath string, line int, report *DedupeReport) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return
	}
	form, ok := gitCallForms[ident.Name+"."+sel.Sel.Name]
	if !ok || len(call.Args) <= form.nameIndex {
		return
	}
	lit, ok := call.Args[form.nameIndex].(*ast.BasicLit)
	if !ok || lit.Value != `"git"` {
		return
	}
	report.SprawlItems = append(report.SprawlItems, SprawlItem{
		File:        relPath,
		Line:        line,
		Pattern:     form.pattern,
		Replacement: gitSprawlReplacement,
	})
}

func collectDuplicates(hashMap map[string][]FileLocation, locMap map[string]int, exceptions []config.Exception, today time.Time, report *DedupeReport) {
	used := make([]bool, len(exceptions))
	for hash, locs := range hashMap {
		if len(locs) <= 1 {
			continue
		}
		group, allExcepted := classifyDuplicateGroup(hash, locMap[hash], locs, exceptions, today, used)
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
	for i := 0; i < len(exceptions) && i < config.MaxExceptions; i++ {
		if !used[i] {
			report.StaleExceptions = append(report.StaleExceptions, fmt.Sprintf(
				"exceptions entry %s (%s): excuses no duplicate function block; remove the entry",
				exceptions[i].Target(), config.ExceptionRuleDedupe))
		}
	}
}

func classifyDuplicateGroup(hash string, loc int, locs []FileLocation, exceptions []config.Exception, today time.Time, used []bool) (DuplicateGroup, bool) {
	group := DuplicateGroup{
		Hash:      hash,
		LOC:       loc,
		Locations: append([]FileLocation(nil), locs...),
	}
	allExcepted := true
	var groupExceptions []ExceptionEntry
	seen := make(map[string]bool)

	for i := range group.Locations {
		live := judgeLocation(&group.Locations[i], exceptions, today, used)
		if live != nil {
			if !seen[live.Path] {
				seen[live.Path] = true
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

func judgeLocation(loc *FileLocation, exceptions []config.Exception, today time.Time, used []bool) *config.Exception {
	live, expired := config.ExceptionFor(exceptions, loc.Path, today, used)
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
	report.Passed = len(report.Duplicates) == 0 && len(report.SprawlItems) == 0 && len(report.StaleExceptions) == 0
}
