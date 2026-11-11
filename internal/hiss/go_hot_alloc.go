// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"io/fs"
	"os"
	slashpath "path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	hotAllocRuleID   = "HISS-03"
	hotMarkerTag     = "hiss:hot"
	maxHotAllocFiles = 20000
	maxHotAllocDecls = 4000
	maxDocLines      = 128
	maxDocComments   = 4000
	maxHotAllocStmts = 10000
)

type hotPathFunction struct {
	file   string
	symbol string
	name   string
	line   int
	isBare bool
}

// checkHotAllocations checks marked hot paths across directories so missing allocation tests are reported.
func checkHotAllocations(ctx context.Context, rep *ScanReport, root string, paths []string) error {
	byDir, dirs := groupFilesByDir(paths, rep)
	if len(dirs) > maxHotAllocFiles {
		rep.Truncated = true
	}
	for i := 0; i < len(dirs) && i < maxHotAllocFiles; i++ {
		if err := ctx.Err(); err != nil {
			rep.Truncated = true
			return err
		}
		if err := checkDirHotAlloc(ctx, rep, root, byDir[dirs[i]]); err != nil {
			if ctx.Err() != nil {
				rep.Truncated = true
				return err
			}
			recordHotDirSkip(rep, err)
		}
	}
	return nil
}

// recordHotDirSkip records a directory check failure as an unparsed skip without double-counting pass-one syntax errors.
func recordHotDirSkip(rep *ScanReport, err error) {
	var parseErr scanner.ErrorList
	if errors.As(err, &parseErr) && rep.Coverage != nil && rep.Coverage.FilesRead > 0 && rep.Skips.Unparsed > 0 {
		return
	}
	rep.Skips.Unparsed++
}

// groupFilesByDir groups source paths by directory and sorts the groups for deterministic scanning.
func groupFilesByDir(paths []string, rep *ScanReport) (map[string][]string, []string) {
	if len(paths) > maxHotAllocFiles {
		rep.Truncated = true
	}
	byDir := make(map[string][]string)
	for i := 0; i < len(paths) && i < maxHotAllocFiles; i++ {
		dir := filepath.Dir(paths[i])
		byDir[dir] = append(byDir[dir], paths[i])
	}
	dirs := make([]string, 0, len(byDir))
	for dir := range byDir {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	return byDir, dirs
}

type hotDirScope struct {
	root     string
	dir      string
	prodPkgs map[string]bool
	rep      *ScanReport
}

// checkDirHotAlloc matches marked production functions against zero-allocation tests in their directory.
func checkDirHotAlloc(ctx context.Context, rep *ScanReport, root string, files []string) error {
	if len(files) == 0 {
		return nil
	}
	if len(files) > maxHotAllocFiles {
		rep.Truncated = true
	}
	var prodFiles, testFiles []string
	for i := 0; i < len(files) && i < maxHotAllocFiles; i++ {
		if strings.HasSuffix(files[i], "_test.go") {
			testFiles = append(testFiles, files[i])
		} else {
			prodFiles = append(prodFiles, files[i])
		}
	}
	fset := token.NewFileSet()
	targets, prodPkgs, err := collectHotTargets(ctx, fset, root, prodFiles, rep)
	if err != nil {
		return err
	}
	if len(targets) == 0 {
		return nil
	}
	scope := hotDirScope{root: root, dir: filepath.Dir(files[0]), prodPkgs: prodPkgs, rep: rep}
	tests, err := findZeroAllocTests(ctx, fset, scope, testFiles)
	if err != nil {
		return err
	}
	reportMissingHotTests(rep, targets, tests)
	return nil
}

// reportMissingHotTests reports bare markers and marked functions without a matching zero-allocation test.
func reportMissingHotTests(rep *ScanReport, targets []hotPathFunction, tests []string) {
	testMap := make(map[string]bool, len(tests))
	for i := 0; i < len(tests) && i < maxHotAllocFiles; i++ {
		testMap[tests[i]] = true
	}
	for i := 0; i < len(targets) && i < maxHotAllocFiles; i++ {
		target := targets[i]
		if target.isBare {
			recordViolation(rep, hotAllocRuleID, target.file, target.line, target.symbol,
				fmt.Sprintf("//hiss:hot marker on %s requires a reason explaining why the function is on the hot path", target.symbol))
			continue
		}
		if !testMap[target.name] {
			msg := fmt.Sprintf("//hiss:hot function %s has no testing.AllocsPerRun test asserting zero allocations; add a test calling testing.AllocsPerRun(n, f) and checking if allocs != 0 { t.Fatalf(...) }", target.symbol)
			recordViolation(rep, hotAllocRuleID, target.file, target.line, target.symbol, msg)
		}
	}
}

// parseHotFile reads filePath under ctx and MaxScanFileSize and parses its Go AST.
func parseHotFile(ctx context.Context, fset *token.FileSet, root, filePath string) (*ast.File, string, error) {
	rel, relErr := filepath.Rel(root, filePath)
	if relErr != nil {
		return nil, "", fmt.Errorf("relativize %s: %w", filePath, relErr)
	}
	fsRoot := root
	fsName := filepath.ToSlash(rel)
	if rel == "." {
		fsRoot = filepath.Dir(filePath)
		fsName = filepath.Base(filePath)
	} else if _, err := util.ConfinePath(root, rel); err != nil {
		return nil, "", fmt.Errorf("confine %s: %w", filePath, err)
	}
	data, err := contextopt.ReadFS(ctx, os.DirFS(fsRoot), fsName, MaxScanFileSize)
	if err != nil {
		return nil, "", err
	}
	file, err := parser.ParseFile(fset, filePath, data, parser.ParseComments|parser.SkipObjectResolution)
	if err != nil {
		return nil, "", err
	}
	return file, filepath.ToSlash(rel), nil
}

// collectHotTargets parses production files under ctx and collects marked functions and declared package names.
func collectHotTargets(ctx context.Context, fset *token.FileSet, root string, prodFiles []string, rep *ScanReport) ([]hotPathFunction, map[string]bool, error) {
	if len(prodFiles) > maxHotAllocFiles {
		rep.Truncated = true
	}
	var targets []hotPathFunction
	prodPkgs := make(map[string]bool)
	for i := 0; i < len(prodFiles) && i < maxHotAllocFiles; i++ {
		file, rel, err := parseHotFile(ctx, fset, root, prodFiles[i])
		if err != nil {
			return nil, nil, fmt.Errorf("parse %s: %w", prodFiles[i], err)
		}
		if file.Name != nil && file.Name.Name != "" {
			prodPkgs[file.Name.Name] = true
		}
		targets = append(targets, collectFileHotFunctions(fset, file, rel, rep)...)
	}
	return targets, prodPkgs, nil
}

// collectFileHotFunctions collects marked declarations and reports markers attached to other syntax.
func collectFileHotFunctions(fset *token.FileSet, file *ast.File, rel string, rep *ScanReport) []hotPathFunction {
	if len(file.Decls) > maxHotAllocDecls {
		rep.Truncated = true
	}
	funcDocs := make(map[*ast.CommentGroup]bool)
	var targets []hotPathFunction
	for i := 0; i < len(file.Decls) && i < maxHotAllocDecls; i++ {
		fn, ok := file.Decls[i].(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		funcDocs[fn.Doc] = true
		found, isBare := parseDocCommentHot(fn.Doc)
		if !found {
			continue
		}
		name, symbol := funcDeclSymbol(fn)
		if fn.Recv == nil {
			name = "func:" + name
		} else {
			// Methods with the same name on different receiver types remain ambiguous.
			name = "method:" + name
		}
		targets = append(targets, hotPathFunction{
			file:   rel,
			symbol: symbol,
			name:   name,
			line:   fset.Position(fn.Pos()).Line,
			isBare: isBare,
		})
	}
	checkNonFunctionMarkers(fset, file, rel, funcDocs, rep)
	return targets
}

// checkNonFunctionMarkers reports hot markers outside function documentation because they cannot identify a target.
func checkNonFunctionMarkers(fset *token.FileSet, file *ast.File, rel string, funcDocs map[*ast.CommentGroup]bool, rep *ScanReport) {
	for i := 0; i < len(file.Comments) && i < maxDocComments; i++ {
		cg := file.Comments[i]
		if funcDocs[cg] {
			continue
		}
		for j := 0; j < len(cg.List) && j < maxDocLines; j++ {
			lines := strings.Split(cg.List[j].Text, "\n")
			for k := 0; k < len(lines) && k < maxDocLines; k++ {
				if matched, _, _ := parseHotCommentLine(lines[k]); matched {
					line := fset.Position(cg.List[j].Pos()).Line + k
					recordViolation(rep, hotAllocRuleID, rel, line, "", "//hiss:hot marker is only valid on function or method declarations; ignored on non-function")
				}
			}
		}
	}
}

// parseDocCommentHot finds a hot marker in a documentation group and identifies a missing reason.
func parseDocCommentHot(cg *ast.CommentGroup) (bool, bool) {
	if cg == nil {
		return false, false
	}
	for i := 0; i < len(cg.List) && i < maxDocLines; i++ {
		lines := strings.Split(cg.List[i].Text, "\n")
		for j := 0; j < len(lines) && j < maxDocLines; j++ {
			if matched, _, bare := parseHotCommentLine(lines[j]); matched {
				return true, bare
			}
		}
	}
	return false, false
}

// parseHotCommentLine recognizes a hot marker and extracts its reason without accepting a longer tag.
func parseHotCommentLine(line string) (bool, string, bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "//")
	trimmed = strings.TrimPrefix(trimmed, "/*")
	trimmed = strings.TrimPrefix(trimmed, "*")
	trimmed = strings.TrimSuffix(trimmed, "*/")
	trimmed = strings.TrimSpace(trimmed)
	if !strings.HasPrefix(trimmed, hotMarkerTag) {
		return false, "", false
	}
	rest := strings.TrimPrefix(trimmed, hotMarkerTag)
	if len(rest) > 0 && rest[0] != ' ' && rest[0] != '\t' && rest[0] != ':' {
		return false, "", false
	}
	rest = strings.TrimPrefix(rest, ":")
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return true, "", true
	}
	return true, rest, false
}

// funcDeclSymbol derives a function name and receiver-qualified symbol for findings.
func funcDeclSymbol(fn *ast.FuncDecl) (string, string) {
	name := fn.Name.Name
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return name, name
	}
	recv := fn.Recv.List[0].Type
	if star, ok := recv.(*ast.StarExpr); ok {
		if id, ok := star.X.(*ast.Ident); ok {
			return name, "(*" + id.Name + ")." + name
		}
	}
	if id, ok := recv.(*ast.Ident); ok {
		return name, id.Name + "." + name
	}
	return name, name
}

// findZeroAllocTests collects functions covered by zero-allocation assertions in reachable test functions.
func findZeroAllocTests(ctx context.Context, fset *token.FileSet, scope hotDirScope, testFiles []string) ([]string, error) {
	if len(testFiles) > maxHotAllocFiles {
		scope.rep.Truncated = true
	}
	byPkg := make(map[string][]*ast.File)
	var pkgs []string
	for i := 0; i < len(testFiles) && i < maxHotAllocFiles; i++ {
		file, _, err := parseHotFile(ctx, fset, scope.root, testFiles[i])
		if err != nil {
			return nil, fmt.Errorf("parse test %s: %w", testFiles[i], err)
		}
		pkg := file.Name.Name
		if _, seen := byPkg[pkg]; !seen {
			pkgs = append(pkgs, pkg)
		}
		byPkg[pkg] = append(byPkg[pkg], file)
	}
	prodImport, err := externalProdImport(ctx, scope, pkgs)
	if err != nil {
		return nil, err
	}
	if len(pkgs) > maxHotAllocFiles {
		scope.rep.Truncated = true
	}
	var results []string
	for i := 0; i < len(pkgs) && i < maxHotAllocFiles; i++ {
		pkgImport := ""
		if isExternalTestPkg(pkgs[i], scope.prodPkgs) {
			pkgImport = prodImport
		}
		results = append(results, findZeroAllocTestsInPackage(byPkg[pkgs[i]], pkgImport, scope.rep)...)
	}
	return results, nil
}

// isExternalTestPkg reports whether pkg is an external _test package for one of prodPkgs.
func isExternalTestPkg(pkg string, prodPkgs map[string]bool) bool {
	base, ok := strings.CutSuffix(pkg, "_test")
	return ok && base != "" && prodPkgs[base]
}

// externalProdImport resolves the production package import path when pkgs includes an external test package.
func externalProdImport(ctx context.Context, scope hotDirScope, pkgs []string) (string, error) {
	for i := 0; i < len(pkgs) && i < maxHotAllocFiles; i++ {
		if isExternalTestPkg(pkgs[i], scope.prodPkgs) {
			return resolveHotProdImport(ctx, scope.root, scope.dir)
		}
	}
	return "", nil
}

// slashDirWithinRoot returns dir's slash-separated path relative to root when confined inside root.
func slashDirWithinRoot(root, dir string) (string, bool) {
	relDir, err := filepath.Rel(root, dir)
	if err != nil {
		return "", false
	}
	if _, err := util.ConfinePath(root, relDir); err != nil {
		return "", false
	}
	return filepath.ToSlash(relDir), true
}

// resolveHotProdImport derives dir's Go import path from the nearest go.mod at or above dir within root.
// If go.mod cannot be read or declares no module, it returns "" so qualified calls fail closed.
func resolveHotProdImport(ctx context.Context, root, dir string) (string, error) {
	slashDir, ok := slashDirWithinRoot(root, dir)
	if !ok {
		return "", nil
	}
	at := slashDir
	for depth := 0; depth < maxModuleDepth; depth++ {
		modPath, stop := readHotModuleManifest(ctx, root, at)
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if stop {
			if modPath == "" {
				return "", nil
			}
			return joinHotModuleImport(modPath, at, slashDir), nil
		}
		if at == "." {
			break
		}
		at = slashpath.Dir(at)
	}
	return "", nil
}

// readHotModuleManifest reads modDir/go.mod under ctx and maxModuleManifestBytes.
// stop is false only when no go.mod exists at modDir; any read error or empty module fails closed.
func readHotModuleManifest(ctx context.Context, root, modDir string) (string, bool) {
	manifestRel := slashpath.Join(modDir, "go.mod")
	if _, err := util.ConfinePath(root, filepath.FromSlash(manifestRel)); err != nil {
		return "", true
	}
	data, err := contextopt.ReadFS(ctx, os.DirFS(root), manifestRel, maxModuleManifestBytes)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && ctx.Err() == nil {
			return "", false
		}
		return "", true
	}
	return gomanifest.ParseManifest(data).Module, true
}

// joinHotModuleImport joins module path modPath with pkgDir relative to modDir.
func joinHotModuleImport(modPath, modDir, pkgDir string) string {
	modPath = strings.TrimSuffix(modPath, "/")
	if pkgDir == modDir {
		return modPath
	}
	if modDir == "." {
		return modPath + "/" + pkgDir
	}
	return modPath + "/" + strings.TrimPrefix(pkgDir, modDir+"/")
}

// hotTestContext carries file import bindings, external production import path, active testing parameters and invoked literals.
type hotTestContext struct {
	imports     GoImports
	prodImport  string
	testParams  []string
	invokedLits map[*ast.FuncLit][]string
}

// withParams returns a copy of ctx with params as the active testing parameter names.
func (ctx hotTestContext) withParams(params []string) hotTestContext {
	ctx.testParams = params
	return ctx
}

type hotTestFunc struct {
	decl  *ast.FuncDecl
	ctx   hotTestContext
	entry bool
}

// firstParamName returns the first non-blank identifier in names, or "" when none is bound.
func firstParamName(names []*ast.Ident) string {
	for i := 0; i < len(names) && i < maxHotAllocDecls; i++ {
		if names[i] != nil && names[i].Name != "_" {
			return names[i].Name
		}
	}
	return ""
}

// hotTestingParam finds the first named testing parameter accepted by allocation assertions.
func hotTestingParam(fn *ast.FuncDecl, imports GoImports) string {
	if fn == nil {
		return ""
	}
	return funcTypeTestingParam(fn.Type, imports)
}

// funcTypeTestingParam finds the first named testing parameter in a function signature.
func funcTypeTestingParam(ft *ast.FuncType, imports GoImports) string {
	if ft == nil || ft.Params == nil {
		return ""
	}
	for i := 0; i < len(ft.Params.List) && i < maxHotAllocDecls; i++ {
		field := ft.Params.List[i]
		if name := firstParamName(field.Names); name != "" && isHotTestingType(field.Type, imports) {
			return name
		}
	}
	return ""
}

// isHotTestingType recognizes *T, *B, *F and TB qualified by a local import name bound to "testing".
func isHotTestingType(expr ast.Expr, imports GoImports) bool {
	return isTestingPtrType(expr, imports, "T") ||
		isTestingPtrType(expr, imports, "B") ||
		isTestingPtrType(expr, imports, "F") ||
		isTestingNamedType(expr, imports, "TB")
}

// isTestingPtrType reports whether expr is a pointer to a named type imported from "testing".
func isTestingPtrType(expr ast.Expr, imports GoImports, typeName string) bool {
	star, ok := unwrapCond(expr).(*ast.StarExpr)
	if !ok {
		return false
	}
	return isTestingNamedType(star.X, imports, typeName)
}

// isTestingNamedType reports whether expr selects typeName from an import bound to "testing".
func isTestingNamedType(expr ast.Expr, imports GoImports, typeName string) bool {
	sel, ok := unwrapCond(expr).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != typeName {
		return false
	}
	pkg, ok := unwrapCond(sel.X).(*ast.Ident)
	return ok && imports.Binds(pkg.Name, "testing")
}

// isHotTestEntry recognizes Test*, Benchmark* and Fuzz* entry points accepted by go test.
func isHotTestEntry(fn *ast.FuncDecl, imports GoImports) bool {
	wantType := entryTestingTypeName(fn.Name.Name)
	if wantType == "" || !hasEntrySignatureShape(fn.Type) {
		return false
	}
	return isTestingPtrType(fn.Type.Params.List[0].Type, imports, wantType)
}

// entryTestingTypeName maps a Go test entry-point name to its required testing pointer type.
func entryTestingTypeName(name string) string {
	if name == "TestMain" {
		return ""
	}
	switch {
	case hasTestEntryPrefix(name, "Test"):
		return "T"
	case hasTestEntryPrefix(name, "Benchmark"):
		return "B"
	case hasTestEntryPrefix(name, "Fuzz"):
		return "F"
	default:
		return ""
	}
}

// hasTestEntryPrefix reports whether name starts with prefix and has no lower-case next rune.
func hasTestEntryPrefix(name, prefix string) bool {
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	rest := name[len(prefix):]
	if rest == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(rest)
	return !unicode.IsLower(r)
}

// hasEntrySignatureShape checks that ft has one parameter, no type parameters and no results.
func hasEntrySignatureShape(ft *ast.FuncType) bool {
	if ft == nil || ft.Params == nil || len(ft.Params.List) != 1 || len(ft.Params.List[0].Names) > 1 {
		return false
	}
	return (ft.TypeParams == nil || len(ft.TypeParams.List) == 0) &&
		(ft.Results == nil || len(ft.Results.List) == 0)
}

// findZeroAllocTestsInPackage credits assertions in package test entry points and helpers they reach.
func findZeroAllocTestsInPackage(files []*ast.File, prodImport string, rep *ScanReport) []string {
	funcs, byName := indexHotTestFuncs(files, prodImport, rep)
	reachable := reachableHotTestFuncs(funcs, byName)
	var tested []string
	for i := 0; i < len(reachable) && i < maxHotAllocDecls; i++ {
		fn := funcs[reachable[i]]
		if len(fn.ctx.testParams) > 0 {
			tested = append(tested, inspectFuncForAllocTests(fn.decl.Body, fn.ctx)...)
		}
	}
	return tested
}

// indexHotTestFuncs collects non-method test entry points and testing-parameter helpers in files.
func indexHotTestFuncs(files []*ast.File, prodImport string, rep *ScanReport) ([]hotTestFunc, map[string][]int) {
	if len(files) > maxHotAllocFiles {
		rep.Truncated = true
	}
	var funcs []hotTestFunc
	byName := make(map[string][]int)
	for i := 0; i < len(files) && i < maxHotAllocFiles; i++ {
		funcs = appendFileHotTestFuncs(files[i], prodImport, funcs, byName, rep)
	}
	return funcs, byName
}

// candidateHotTestDecl returns decl as a plain function with a body; methods stay out of the
// reach set because go test never enters them and plain-name helper calls cannot target them.
func candidateHotTestDecl(decl ast.Decl) (*ast.FuncDecl, bool) {
	fn, ok := decl.(*ast.FuncDecl)
	if !ok || fn.Recv != nil || fn.Body == nil || fn.Name == nil {
		return nil, false
	}
	return fn, true
}

// buildHotTestFunc builds a hotTestFunc when decl is a test entry point or testing-parameter helper.
func buildHotTestFunc(decl ast.Decl, imports GoImports, prodImport string) (hotTestFunc, bool) {
	fn, ok := candidateHotTestDecl(decl)
	if !ok {
		return hotTestFunc{}, false
	}
	entry := isHotTestEntry(fn, imports)
	param := hotTestingParam(fn, imports)
	if !entry && param == "" {
		return hotTestFunc{}, false
	}
	var params []string
	if param != "" {
		params = []string{param}
	}
	ctx := hotTestContext{
		imports:     imports,
		prodImport:  prodImport,
		testParams:  params,
		invokedLits: collectInvokedFuncLits(fn.Body, imports, params),
	}
	return hotTestFunc{decl: fn, ctx: ctx, entry: entry}, true
}

// appendFileHotTestFuncs indexes one test file's entry points and helpers into funcs and byName.
func appendFileHotTestFuncs(file *ast.File, prodImport string, funcs []hotTestFunc, byName map[string][]int, rep *ScanReport) []hotTestFunc {
	imports := FileImports(file)
	if len(file.Decls) > maxHotAllocDecls {
		rep.Truncated = true
	}
	for i := 0; i < len(file.Decls) && i < maxHotAllocDecls; i++ {
		tf, ok := buildHotTestFunc(file.Decls[i], imports, prodImport)
		if !ok {
			continue
		}
		if len(funcs) >= maxHotAllocDecls {
			rep.Truncated = true
			return funcs
		}
		idx := len(funcs)
		funcs = append(funcs, tf)
		byName[tf.decl.Name.Name] = append(byName[tf.decl.Name.Name], idx)
	}
	return funcs
}

// reachableHotTestFuncs traverses plain-name helper calls breadth-first from package entry points.
func reachableHotTestFuncs(funcs []hotTestFunc, byName map[string][]int) []int {
	visited := make([]bool, len(funcs))
	queue := make([]int, 0, len(funcs))
	for i := 0; i < len(funcs) && i < maxHotAllocDecls; i++ {
		if funcs[i].entry {
			visited[i] = true
			queue = append(queue, i)
		}
	}
	for head := 0; head < len(queue) && head < maxHotAllocDecls; head++ {
		cur := funcs[queue[head]]
		callees := plainHelperCalls(cur.decl, cur.ctx.invokedLits)
		queue = enqueueHotHelpers(callees, byName, visited, queue)
	}
	return queue
}

// enqueueHotHelpers appends unvisited helper indices for callees within maxHotAllocDecls.
func enqueueHotHelpers(callees []string, byName map[string][]int, visited []bool, queue []int) []int {
	for i := 0; i < len(callees) && i < maxHotAllocDecls; i++ {
		targets := byName[callees[i]]
		for j := 0; j < len(targets) && len(queue) < maxHotAllocDecls; j++ {
			idx := targets[j]
			if !visited[idx] {
				visited[idx] = true
				queue = append(queue, idx)
			}
		}
	}
	return queue
}

// plainHelperCalls returns unshadowed plain-name callees reached from fn outside uncalled literals.
func plainHelperCalls(fn *ast.FuncDecl, invoked map[*ast.FuncLit][]string) []string {
	var scope goScope
	var callees []string
	seen := make(map[string]bool)
	scope.inspect(fn, func(n ast.Node) bool {
		if lit, ok := n.(*ast.FuncLit); ok {
			_, ok = invoked[lit]
			return ok
		}
		call, ok := n.(*ast.CallExpr)
		if !ok || len(callees) >= maxHotAllocDecls {
			return true
		}
		ident, ok := stripTypeArgs(call.Fun).(*ast.Ident)
		if ok && !scope.binds(ident.Name) && !seen[ident.Name] {
			seen[ident.Name] = true
			callees = append(callees, ident.Name)
		}
		return true
	})
	return callees
}

type litParamStack struct {
	invoked    map[*ast.FuncLit][]string
	stack      []ast.Node
	paramStack [][]string
}

// newLitParamStack initializes a stack tracking active testing parameters across invoked literals.
func newLitParamStack(ctx hotTestContext) litParamStack {
	return litParamStack{
		invoked:    ctx.invokedLits,
		paramStack: [][]string{ctx.testParams},
	}
}

// params returns the testing parameter names active at the current traversal depth.
func (s *litParamStack) params() []string {
	return s.paramStack[len(s.paramStack)-1]
}

// full reports whether the traversal stack has reached maxNodeStack.
func (s *litParamStack) full() bool {
	return len(s.stack) >= maxNodeStack
}

// push enters node n, refusing uncalled function literals so their bodies are skipped.
func (s *litParamStack) push(n ast.Node) bool {
	if lit, ok := n.(*ast.FuncLit); ok {
		params, ok := s.invoked[lit]
		if !ok {
			return false
		}
		s.paramStack = append(s.paramStack, params)
	}
	s.stack = append(s.stack, n)
	return true
}

// pop exits the most recently pushed AST node on a post-order nil callback.
func (s *litParamStack) pop() {
	last := len(s.stack) - 1
	if last < 0 {
		return
	}
	if _, isLit := s.stack[last].(*ast.FuncLit); isLit {
		s.paramStack = s.paramStack[:len(s.paramStack)-1]
	}
	s.stack = s.stack[:last]
}

type invokedLitCollector struct {
	imports GoImports
	frames  litParamStack
}

// collectInvokedFuncLits maps each immediately invoked or t.Run/b.Run function literal
// reachable from body to the testing parameter names active inside that literal.
func collectInvokedFuncLits(body *ast.BlockStmt, imports GoImports, rootParams []string) map[*ast.FuncLit][]string {
	if body == nil {
		return nil
	}
	invoked := make(map[*ast.FuncLit][]string)
	c := invokedLitCollector{
		imports: imports,
		frames: litParamStack{
			invoked:    invoked,
			paramStack: [][]string{rootParams},
		},
	}
	ast.Inspect(body, c.visit)
	return invoked
}

// visit records invoked function literals in pre-order and skips uncalled literal bodies.
func (c *invokedLitCollector) visit(n ast.Node) bool {
	if n == nil {
		c.frames.pop()
		return true
	}
	if c.frames.full() {
		return false
	}
	if call, ok := n.(*ast.CallExpr); ok {
		c.recordCall(call, c.frames.params())
	}
	return c.frames.push(n)
}

// recordCall marks an immediately invoked literal or t.Run/b.Run callback as invoked.
func (c *invokedLitCollector) recordCall(call *ast.CallExpr, curParams []string) {
	if lit, ok := unwrapCond(call.Fun).(*ast.FuncLit); ok {
		c.frames.invoked[lit] = extendTestingParams(lit.Type, c.imports, curParams)
		return
	}
	if lit, params, ok := subtestRunLit(call, c.imports, curParams); ok {
		c.frames.invoked[lit] = params
	}
}

// filterShadowedParams retains testing parameter names not redeclared by ft.
func filterShadowedParams(ft *ast.FuncType, params []string) []string {
	var kept []string
	for i := 0; i < len(params) && i < maxHotAllocDecls; i++ {
		if !funcTypeNamesVar(ft, params[i]) {
			kept = append(kept, params[i])
		}
	}
	return kept
}

// extendTestingParams computes active testing parameters inside a literal signature ft.
func extendTestingParams(ft *ast.FuncType, imports GoImports, curParams []string) []string {
	next := filterShadowedParams(ft, curParams)
	if param := funcTypeTestingParam(ft, imports); param != "" && !slices.Contains(next, param) {
		next = append(next, param)
	}
	return next
}

// isRunMethodOnParam reports whether fun selects Run on one of curParams.
func isRunMethodOnParam(fun ast.Expr, curParams []string) bool {
	sel, ok := unwrapCond(fun).(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Run" {
		return false
	}
	recv, ok := unwrapCond(sel.X).(*ast.Ident)
	return ok && slices.Contains(curParams, recv.Name)
}

// isSubtestLitType checks that ft is a single-parameter *testing.T or *testing.B callback.
func isSubtestLitType(ft *ast.FuncType, imports GoImports) bool {
	if !hasEntrySignatureShape(ft) {
		return false
	}
	paramType := ft.Params.List[0].Type
	return isTestingPtrType(paramType, imports, "T") || isTestingPtrType(paramType, imports, "B")
}

// subtestRunLit recognizes t.Run and b.Run subtest literals on active testing parameters.
func subtestRunLit(call *ast.CallExpr, imports GoImports, curParams []string) (*ast.FuncLit, []string, bool) {
	if len(curParams) == 0 || len(call.Args) != 2 || call.Ellipsis.IsValid() {
		return nil, nil, false
	}
	if !isRunMethodOnParam(call.Fun, curParams) {
		return nil, nil, false
	}
	lit, ok := unwrapCond(call.Args[1]).(*ast.FuncLit)
	if !ok || !isSubtestLitType(lit.Type, imports) {
		return nil, nil, false
	}
	return lit, extendTestingParams(lit.Type, imports, curParams), true
}

// inspectFuncForAllocTests walks blocks outside uncalled function literals for allocation tests.
func inspectFuncForAllocTests(body *ast.BlockStmt, ctx hotTestContext) []string {
	var tested []string
	frames := newLitParamStack(ctx)
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			frames.pop()
			return true
		}
		if frames.full() || !frames.push(n) {
			return false
		}
		if block, ok := n.(*ast.BlockStmt); ok {
			tested = append(tested, inspectBlockForAllocTests(block, ctx.withParams(frames.params()))...)
		}
		return true
	})
	return tested
}

// inspectBlockForAllocTests examines statements for allocation tests and their assertions.
func inspectBlockForAllocTests(block *ast.BlockStmt, ctx hotTestContext) []string {
	var tested []string
	stmts := block.List
	for i := 0; i < len(stmts) && i < maxHotAllocStmts; i++ {
		switch s := stmts[i].(type) {
		case *ast.AssignStmt:
			tested = append(tested, checkAssignAllocTest(stmts, i, s, ctx)...)
		case *ast.DeclStmt:
			tested = append(tested, checkDeclAllocTest(stmts, i, s, ctx)...)
		case *ast.IfStmt:
			tested = append(tested, checkIfAllocTest(s, ctx)...)
		}
	}
	return tested
}

// checkAssignAllocTest recognizes an allocation measurement assigned for a later assertion.
func checkAssignAllocTest(stmts []ast.Stmt, idx int, assign *ast.AssignStmt, ctx hotTestContext) []string {
	if len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if !valid {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || id.Name == "_" {
		return nil
	}
	if hasZeroAssertion(stmts[idx+1:], id.Name, nil, ctx) {
		return targets
	}
	return nil
}

// checkDeclAllocTest recognizes an allocation measurement declared for a later assertion.
func checkDeclAllocTest(stmts []ast.Stmt, idx int, declStmt *ast.DeclStmt, ctx hotTestContext) []string {
	gen, ok := declStmt.Decl.(*ast.GenDecl)
	if !ok || gen.Tok != token.VAR {
		return nil
	}
	for i := 0; i < len(gen.Specs) && i < maxHotAllocDecls; i++ {
		spec, ok := gen.Specs[i].(*ast.ValueSpec)
		if ok {
			if targets := checkValueSpecAllocTest(stmts, idx, spec, ctx); len(targets) > 0 {
				return targets
			}
		}
	}
	return nil
}

// checkValueSpecAllocTest checks one var ValueSpec for a zero-asserted AllocsPerRun measurement.
func checkValueSpecAllocTest(stmts []ast.Stmt, idx int, spec *ast.ValueSpec, ctx hotTestContext) []string {
	if len(spec.Names) == 0 || len(spec.Values) == 0 || spec.Names[0].Name == "_" {
		return nil
	}
	call, ok := spec.Values[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && hasZeroAssertion(stmts[idx+1:], spec.Names[0].Name, nil, ctx) {
		return targets
	}
	return nil
}

// checkIfInitAllocTest recognizes an allocation measurement in an if initializer.
func checkIfInitAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	assign, ok := ifStmt.Init.(*ast.AssignStmt)
	if !ok || len(assign.Lhs) == 0 || len(assign.Rhs) == 0 {
		return nil
	}
	call, ok := assign.Rhs[0].(*ast.CallExpr)
	if !ok {
		return nil
	}
	id, ok := assign.Lhs[0].(*ast.Ident)
	if !ok || id.Name == "_" {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && isZeroCondition(ifStmt.Cond, id.Name, nil) {
		return targets
	}
	return nil
}

// checkIfCondAllocTest recognizes an allocation measurement used directly in an if condition.
func checkIfCondAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	bin, ok := unwrapCond(ifStmt.Cond).(*ast.BinaryExpr)
	if !ok {
		return nil
	}
	call, ok := bin.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	targets, valid := checkAllocsCall(call, ctx)
	if valid && isZeroCondition(bin, "", call) {
		return targets
	}
	return nil
}

// checkIfAllocTest checks whether an if statement establishes a zero-allocation assertion.
func checkIfAllocTest(ifStmt *ast.IfStmt, ctx hotTestContext) []string {
	if !bodyFailsTest(ifStmt.Body, ctx) {
		return nil
	}
	if targets := checkIfInitAllocTest(ifStmt, ctx); len(targets) > 0 {
		return targets
	}
	return checkIfCondAllocTest(ifStmt, ctx)
}

type zeroAssertionWalk struct {
	varName string
	call    *ast.CallExpr
	ctx     hotTestContext
	frames  litParamStack
	found   bool
	stopped bool
}

// hasZeroAssertion looks for a failure assertion while the measured allocation binding still holds.
func hasZeroAssertion(stmts []ast.Stmt, varName string, call *ast.CallExpr, ctx hotTestContext) bool {
	if varName == "_" {
		return false
	}
	w := zeroAssertionWalk{
		varName: varName,
		call:    call,
		ctx:     ctx,
		frames:  newLitParamStack(ctx),
	}
	for i := 0; i < len(stmts) && i < maxHotAllocStmts && !w.found && !w.stopped; i++ {
		ast.Inspect(stmts[i], w.visit)
	}
	return w.found
}

// visit inspects one AST node in source order, skipping uncalled literals and halting on rebinding.
func (w *zeroAssertionWalk) visit(n ast.Node) bool {
	if n == nil {
		w.frames.pop()
		return true
	}
	if w.found || w.stopped || w.frames.full() {
		return false
	}
	if lit, ok := n.(*ast.FuncLit); ok && funcTypeNamesVar(lit.Type, w.varName) {
		return false
	}
	if ifStmt, ok := n.(*ast.IfStmt); ok {
		return w.visitIf(ifStmt)
	}
	if rebindsHotVar(n, w.varName) {
		w.stopped = true
		return false
	}
	return w.frames.push(n)
}

// visitIf checks one if statement for a rebinding initializer or a zero-allocation failure branch.
func (w *zeroAssertionWalk) visitIf(ifStmt *ast.IfStmt) bool {
	if rebindsHotVar(ifStmt.Init, w.varName) {
		w.stopped = true
		return false
	}
	curCtx := w.ctx.withParams(w.frames.params())
	if isZeroCondition(ifStmt.Cond, w.varName, w.call) && bodyFailsTest(ifStmt.Body, curCtx) {
		w.found = true
		return false
	}
	return w.frames.push(ifStmt)
}

// rebindsHotVar reports whether node assigns to or redeclares varName.
func rebindsHotVar(node ast.Node, varName string) bool {
	if node == nil || varName == "" {
		return false
	}
	return rebindsHotStmt(node, varName) || rebindsHotDecl(node, varName)
}

// rebindsHotStmt reports whether a statement node assigns to or defines varName.
func rebindsHotStmt(node ast.Node, varName string) bool {
	switch s := node.(type) {
	case *ast.AssignStmt:
		return exprListNamesVar(s.Lhs, varName)
	case *ast.IncDecStmt:
		return exprNamesVar(s.X, varName)
	case *ast.RangeStmt:
		return isRangeBindingTok(s.Tok) && (exprNamesVar(s.Key, varName) || exprNamesVar(s.Value, varName))
	default:
		return false
	}
}

// isRangeBindingTok reports whether a range statement binds loop variables.
func isRangeBindingTok(tok token.Token) bool {
	return tok == token.DEFINE || tok == token.ASSIGN
}

// rebindsHotDecl reports whether a specification or function literal redeclares varName.
func rebindsHotDecl(node ast.Node, varName string) bool {
	switch s := node.(type) {
	case *ast.ValueSpec:
		return identListNamesVar(s.Names, varName)
	case *ast.TypeSpec:
		return s.Name != nil && s.Name.Name == varName
	case *ast.FuncLit:
		return funcTypeNamesVar(s.Type, varName)
	default:
		return false
	}
}

// exprNamesVar reports whether expr is the identifier varName after unwrapping parentheses.
func exprNamesVar(expr ast.Expr, varName string) bool {
	if expr == nil {
		return false
	}
	id, ok := unwrapCond(expr).(*ast.Ident)
	return ok && id.Name == varName
}

// exprListNamesVar reports whether any expression in exprs names varName.
func exprListNamesVar(exprs []ast.Expr, varName string) bool {
	for i := 0; i < len(exprs) && i < maxHotAllocDecls; i++ {
		if exprNamesVar(exprs[i], varName) {
			return true
		}
	}
	return false
}

// identListNamesVar reports whether any identifier in idents names varName.
func identListNamesVar(idents []*ast.Ident, varName string) bool {
	for i := 0; i < len(idents) && i < maxHotAllocDecls; i++ {
		if idents[i] != nil && idents[i].Name == varName {
			return true
		}
	}
	return false
}

// funcTypeNamesVar reports whether a function signature declares a parameter or result named varName.
func funcTypeNamesVar(ft *ast.FuncType, varName string) bool {
	if ft == nil {
		return false
	}
	return fieldListNamesVar(ft.Params, varName) || fieldListNamesVar(ft.Results, varName)
}

// fieldListNamesVar reports whether any field in fields declares varName.
func fieldListNamesVar(fields *ast.FieldList, varName string) bool {
	if fields == nil {
		return false
	}
	for i := 0; i < len(fields.List) && i < maxHotAllocDecls; i++ {
		if identListNamesVar(fields.List[i].Names, varName) {
			return true
		}
	}
	return false
}

// isZeroCondition recognizes a condition comparing an allocation count with zero.
func isZeroCondition(cond ast.Expr, varName string, call *ast.CallExpr) bool {
	bin, ok := unwrapCond(cond).(*ast.BinaryExpr)
	if !ok {
		return false
	}
	switch bin.Op {
	case token.NEQ:
		return (isMatchingAllocs(bin.X, varName, call) && isZeroLiteral(bin.Y)) ||
			(isZeroLiteral(bin.X) && isMatchingAllocs(bin.Y, varName, call))
	case token.GTR:
		return isMatchingAllocs(bin.X, varName, call) && isZeroLiteral(bin.Y)
	case token.LSS:
		return isZeroLiteral(bin.X) && isMatchingAllocs(bin.Y, varName, call)
	default:
		return false
	}
}

// unwrapCond removes enclosing parentheses before inspecting a condition.
func unwrapCond(expr ast.Expr) ast.Expr {
	for i := 0; i < maxDocLines; i++ {
		if paren, ok := expr.(*ast.ParenExpr); ok {
			expr = paren.X
		} else {
			break
		}
	}
	return expr
}

var predeclaredNumericTypes = map[string]bool{
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true, "uintptr": true,
	"float32": true, "float64": true,
}

// isPredeclaredNumericConversion reports whether call converts a single value to a predeclared numeric type.
func isPredeclaredNumericConversion(call *ast.CallExpr) bool {
	if len(call.Args) != 1 || call.Ellipsis.IsValid() {
		return false
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && predeclaredNumericTypes[id.Name]
}

// isZeroLiteral recognizes a literal zero, optionally wrapped in predeclared numeric conversions.
func isZeroLiteral(expr ast.Expr) bool {
	for i := 0; i < maxDocLines; i++ {
		expr = unwrapCond(expr)
		if call, ok := expr.(*ast.CallExpr); ok && isPredeclaredNumericConversion(call) {
			expr = call.Args[0]
			continue
		}
		break
	}
	expr = unwrapCond(expr)
	if lit, ok := expr.(*ast.BasicLit); ok && (lit.Kind == token.INT || lit.Kind == token.FLOAT) {
		return lit.Value == "0" || lit.Value == "0.0" || lit.Value == "0."
	}
	return false
}

// isMatchingAllocs matches an allocation measurement to the identifier under test.
func isMatchingAllocs(expr ast.Expr, varName string, call *ast.CallExpr) bool {
	expr = unwrapCond(expr)
	if varName != "" {
		if id, ok := expr.(*ast.Ident); ok && id.Name == varName {
			return true
		}
	}
	if call != nil && expr == call {
		return true
	}
	return false
}

// bodyFailsTest checks whether a branch contains a test failure call outside uncalled literals.
func bodyFailsTest(body *ast.BlockStmt, ctx hotTestContext) bool {
	if body == nil {
		return false
	}
	found := false
	frames := newLitParamStack(ctx)
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			frames.pop()
			return true
		}
		if found || frames.full() {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok && callFailsTest(call, frames.params()) {
			found = true
			return false
		}
		return frames.push(n)
	})
	return found
}

// callFailsTest recognizes calls that fail the current test through an active testing parameter.
func callFailsTest(c *ast.CallExpr, testParams []string) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(testParams) == 0 {
		return false
	}
	receiver, ok := sel.X.(*ast.Ident)
	if !ok || !slices.Contains(testParams, receiver.Name) {
		return false
	}
	switch sel.Sel.Name {
	case "Fatal", "Fatalf", "Error", "Errorf", "FailNow":
		return true
	default:
		return false
	}
}

// checkAllocsCall extracts target calls from a valid testing.AllocsPerRun measurement.
func checkAllocsCall(call *ast.CallExpr, ctx hotTestContext) ([]string, bool) {
	if call == nil || len(call.Args) < 2 {
		return nil, false
	}
	if !isAllocsPerRunIdent(call.Fun, ctx.imports) {
		return nil, false
	}
	if !isValidRunsArg(call.Args[0]) {
		return nil, false
	}
	targets := extractCallees(call.Args[1], ctx.imports, ctx.prodImport)
	if len(targets) == 0 {
		return nil, false
	}
	return targets, true
}

// isAllocsPerRunIdent reports whether fun resolves to testing.AllocsPerRun through imports.
func isAllocsPerRunIdent(fun ast.Expr, imports GoImports) bool {
	fun = unwrapCond(fun)
	if sel, ok := fun.(*ast.SelectorExpr); ok {
		return isTestingNamedType(sel, imports, "AllocsPerRun")
	}
	if id, ok := fun.(*ast.Ident); ok {
		return id.Name == "AllocsPerRun" && imports.DotImports("testing")
	}
	return false
}

// isValidRunsArg checks the measurement count before accepting an allocation test.
func isValidRunsArg(arg ast.Expr) bool {
	for i := 0; i < maxDocLines; i++ {
		arg = unwrapCond(arg)
		un, ok := arg.(*ast.UnaryExpr)
		if !ok {
			break
		}
		if un.Op == token.SUB {
			return false
		}
		if un.Op != token.ADD {
			break
		}
		arg = un.X
	}
	if lit, ok := arg.(*ast.BasicLit); ok && lit.Kind == token.INT {
		val, err := strconv.ParseInt(lit.Value, 0, 64)
		return err == nil && val > 0
	}
	return true
}

// extractCallees identifies the function or calls passed to an allocation measurement.
func extractCallees(arg ast.Expr, imports GoImports, prodImport string) []string {
	var callees []string
	if id, ok := unwrapCallee(arg, imports, prodImport); ok {
		callees = append(callees, id)
		return callees
	}
	ast.Inspect(arg, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if name, ok := unwrapCallee(call.Fun, imports, prodImport); ok {
				callees = append(callees, name)
			}
		}
		return true
	})
	return callees
}

// unwrapCallee removes wrappers from a callee expression before identifying its target.
// Package-qualified calls fail closed when go.mod cannot be read (prodImport == "") or inside
// internal test packages, crediting a local function only when the qualifier binds prodImport.
func unwrapCallee(fun ast.Expr, imports GoImports, prodImport string) (string, bool) {
	fun = stripTypeArgs(fun)
	if id, ok := fun.(*ast.Ident); ok {
		return "func:" + id.Name, true
	}
	sel, ok := fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	if id, ok := unwrapCond(sel.X).(*ast.Ident); ok {
		if importPath, isPkg := imports.Path(id.Name); isPkg {
			// Fail closed when go.mod cannot be read (prodImport == ""): a qualified call credits nothing local.
			if prodImport != "" && importPath == prodImport {
				return "func:" + sel.Sel.Name, true
			}
			return "pkg:" + importPath + "." + sel.Sel.Name, true
		}
	}
	return "method:" + sel.Sel.Name, true
}
