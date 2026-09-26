package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

const (
	maxOutputOracleEntries          = 256
	maxOutputOracleFiles            = 128
	maxOutputOracleFileBytes        = 1 << 20
	maxOutputOracleTotalBytes       = 8 << 20
	expectedMCPOutputCallsiteDigest = "sha256:d458273d8002c9c1b15e35dc6cd43a0ef07b2627107b172fbbf14ae80606496b"
)

type outputCallsite struct{ identity, kind string }

type outputOracleFile struct {
	path    string
	file    *ast.File
	imports map[string]string
}

func independentMCPOutputCallsites(ctx context.Context, root string) (callsites []outputCallsite, inventory []string, err error) {
	directory, err := contextopt.OpenDirectory(ctx, filepath.Join(root, "cmd", "standards-mcp"))
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, directory.Close()) }()
	stream, err := directory.Open(".")
	if err != nil {
		return nil, nil, err
	}
	defer func() { err = errors.Join(err, stream.Close()) }()
	entries, readErr := stream.ReadDir(maxOutputOracleEntries + 1)
	if readErr != nil && !errors.Is(readErr, io.EOF) || len(entries) > maxOutputOracleEntries {
		return nil, nil, errors.New("output census directory bound failed")
	}
	files, total := token.NewFileSet(), 0
	callsites = make([]outputCallsite, 0, 203)
	parsedFiles := make([]outputOracleFile, 0, maxOutputOracleFiles)
	count := 0
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() {
			return nil, nil, fmt.Errorf("output census rejects non-regular entry %s", entry.Name())
		}
		if filepath.Ext(entry.Name()) != ".go" || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		count++
		path := filepath.ToSlash(filepath.Join("cmd", "standards-mcp", entry.Name()))
		inventory = append(inventory, path)
		data, readErr := contextopt.ReadRootSnapshot(ctx, directory, entry.Name())
		total += len(data)
		if readErr != nil || count > maxOutputOracleFiles || len(data) > maxOutputOracleFileBytes || total > maxOutputOracleTotalBytes {
			return nil, nil, errors.New("output census read or source bound failed")
		}
		parsed, parseErr := parser.ParseFile(files, entry.Name(), data, 0)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		imports, importErr := outputOracleImports(parsed)
		if importErr != nil {
			return nil, nil, importErr
		}
		parsedFiles = append(parsedFiles, outputOracleFile{path: path, file: parsed, imports: imports})
	}
	resultGlobals, globalsErr := oraclePackageResultNames(parsedFiles)
	if globalsErr != nil {
		return nil, nil, globalsErr
	}
	for _, parsed := range parsedFiles {
		found, censusErr := censusOutputASTWithGlobals(files, parsed, resultGlobals)
		if censusErr != nil {
			return nil, nil, censusErr
		}
		callsites = append(callsites, found...)
	}
	if count == 0 || len(callsites) == 0 {
		return nil, nil, errors.New("output census found zero files or callsites")
	}
	sort.Slice(callsites, func(i, j int) bool { return callsites[i].identity < callsites[j].identity })
	sort.Strings(inventory)
	return callsites, inventory, nil
}

func censusOutputAST(files *token.FileSet, path string, file *ast.File) ([]outputCallsite, error) {
	imports, err := outputOracleImports(file)
	if err != nil {
		return nil, err
	}
	return censusOutputASTWithGlobals(files, outputOracleFile{path: path, file: file, imports: imports}, nil)
}

func censusOutputASTWithGlobals(
	files *token.FileSet,
	parsed outputOracleFile,
	resultGlobals map[string]bool,
) ([]outputCallsite, error) {
	file, imports, path := parsed.file, parsed.imports, parsed.path
	if err := rejectOracleResultTypeReferences(file, imports); err != nil {
		return nil, err
	}
	if err := rejectOraclePredeclaredShadows(file); err != nil {
		return nil, err
	}
	if err := rejectOracleResultMutations(file, imports, resultGlobals); err != nil {
		return nil, err
	}
	var found []outputCallsite
	for _, declaration := range file.Decls {
		var scoped []outputCallsite
		var scanErr error
		switch value := declaration.(type) {
		case *ast.FuncDecl:
			if value.Body != nil {
				scoped, scanErr = censusOutputNode(files, path, value, value.Body, imports)
			}
		case *ast.GenDecl:
			scoped, scanErr = censusOutputValues(files, path, value, imports)
		}
		if scanErr != nil {
			return nil, scanErr
		}
		found = append(found, scoped...)
	}
	return found, nil
}

func censusOutputValues(files *token.FileSet, path string, declaration *ast.GenDecl, imports map[string]string) ([]outputCallsite, error) {
	var found []outputCallsite
	for _, raw := range declaration.Specs {
		spec, ok := raw.(*ast.ValueSpec)
		if !ok {
			continue
		}
		for _, value := range spec.Values {
			scoped, err := censusOutputNode(files, path, nil, value, imports)
			if err != nil {
				return nil, err
			}
			found = append(found, scoped...)
		}
	}
	return found, nil
}

func censusOutputNode(files *token.FileSet, path string, owner *ast.FuncDecl, node ast.Node, imports map[string]string) ([]outputCallsite, error) {
	var found []outputCallsite
	var inspectErr error
	ast.Inspect(node, func(current ast.Node) bool {
		call, ok := current.(*ast.CallExpr)
		if !ok || inspectErr != nil {
			return inspectErr == nil
		}
		kind, callee, include, err := classifyOutputCall(owner, call, imports)
		position := files.Position(call.Pos())
		if err != nil {
			inspectErr = fmt.Errorf("%s:%d:%d: %w", path, position.Line, position.Column, err)
		} else if include {
			identity := fmt.Sprintf("%s:%d:%d:%s:%s", path, position.Line, position.Column, kind, callee)
			found = append(found, outputCallsite{identity, kind})
		}
		return inspectErr == nil
	})
	return found, inspectErr
}

func rejectOracleResultTypeReferences(file *ast.File, imports map[string]string) error {
	allowed := oracleAllowedResultTypeReferences(file, imports)
	var firstErr error
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || firstErr != nil {
			return firstErr == nil
		}
		name := outputOracleName(selector, imports)
		if (name == "mcp.ToolResult" || name == "mcp.ContentItem") && !allowed[selector] {
			firstErr = fmt.Errorf("direct %s type reference bypasses independent output census", name)
			return false
		}
		return true
	})
	return firstErr
}

// rejectOraclePredeclaredShadows keeps universe names such as nil out of result tracking:
// a sibling file resolves them to the universe object, so no package may redefine them.
func rejectOraclePredeclaredShadows(file *ast.File) error {
	for name := range file.Scope.Objects {
		if types.Universe.Lookup(name) != nil {
			return fmt.Errorf("package declaration shadows predeclared identifier %s", name)
		}
	}
	return nil
}

type oracleResultKind uint8

const (
	oracleResultNone oracleResultKind = iota
	oracleResultValue
	oracleResultContent
	oracleResultItem
	oracleResultText
)

type oracleObjectIdentity struct {
	declaration any
	name        string
}

func oracleIdentifierIdentity(identifier *ast.Ident) (oracleObjectIdentity, bool) {
	if identifier == nil || identifier.Name == "" ||
		identifier.Obj == nil && types.Universe.Lookup(identifier.Name) != nil {
		return oracleObjectIdentity{}, false
	}
	if identifier.Obj == nil || identifier.Obj.Decl == nil {
		return oracleObjectIdentity{name: identifier.Name}, true
	}
	return oracleObjectIdentity{declaration: identifier.Obj.Decl, name: identifier.Obj.Name}, true
}

func rejectOracleResultMutations(
	file *ast.File,
	imports map[string]string,
	resultGlobals map[string]bool,
) error {
	values, err := oracleResultValues(file, imports, resultGlobals)
	if err != nil {
		return err
	}
	var firstErr error
	ast.Inspect(file, func(node ast.Node) bool {
		if firstErr != nil {
			return false
		}
		switch value := node.(type) {
		case *ast.AssignStmt:
			firstErr = oracleRejectAssignment(value, values, imports)
		case *ast.IncDecStmt:
			firstErr = oracleRejectMutationTarget(value.X, values, imports)
		case *ast.CallExpr:
			firstErr = oracleRejectResultEscape(value, values, imports)
		case *ast.SendStmt:
			if oracleResultExpressionKind(value.Value, values, imports) != oracleResultNone {
				firstErr = errors.New("post-construction result escapes through channel")
			}
		case *ast.CompositeLit:
			firstErr = oracleRejectCompositeEscape(value, values, imports)
		}
		return firstErr == nil
	})
	return firstErr
}

func oracleResultValues(
	file *ast.File,
	imports map[string]string,
	resultGlobals map[string]bool,
) (map[oracleObjectIdentity]oracleResultKind, error) {
	values := make(map[oracleObjectIdentity]oracleResultKind)
	for name := range resultGlobals {
		values[oracleObjectIdentity{name: name}] = oracleResultValue
	}
	oracleSeedPackageResults(file, resultGlobals, values)
	oracleSeedReturnedResults(file, imports, values)
	for pass := 0; pass < maxOutputOracleEntries; pass++ {
		if !oraclePropagateResultValues(file, imports, values) {
			return values, nil
		}
	}
	if !oraclePropagateResultValues(file, imports, values) {
		return values, nil
	}
	return nil, errors.New("independent output census result alias bound exceeded")
}

// oraclePackageResultNames returns every package-level variable that holds a tool result in
// any census file. Object resolution is per file, so a global declared in one file and
// escaped in another is only visible through this bounded cross-file fixed point.
func oraclePackageResultNames(parsedFiles []outputOracleFile) (map[string]bool, error) {
	names := make(map[string]bool)
	for pass := 0; pass <= maxOutputOracleEntries; pass++ {
		changed := false
		for _, parsed := range parsedFiles {
			values, err := oracleResultValues(parsed.file, parsed.imports, names)
			if err != nil {
				return nil, err
			}
			changed = oracleCollectPackageResults(parsed.file, values, names) || changed
		}
		if !changed {
			return names, nil
		}
	}
	return nil, errors.New("independent output census package result bound exceeded")
}

func oracleCollectPackageResults(
	file *ast.File,
	values map[oracleObjectIdentity]oracleResultKind,
	names map[string]bool,
) bool {
	specs := oraclePackageValueSpecs(file)
	changed := false
	for identity, kind := range values {
		if kind != oracleResultValue || names[identity.name] {
			continue
		}
		spec, declared := identity.declaration.(*ast.ValueSpec)
		if identity.declaration != nil && (!declared || !specs[spec]) {
			continue
		}
		names[identity.name] = true
		changed = true
	}
	return changed
}

func oracleSeedPackageResults(
	file *ast.File,
	names map[string]bool,
	known map[oracleObjectIdentity]oracleResultKind,
) {
	for spec := range oraclePackageValueSpecs(file) {
		for _, name := range spec.Names {
			if names[name.Name] {
				known[oracleObjectIdentity{declaration: spec, name: name.Name}] = oracleResultValue
			}
		}
	}
}

func oraclePackageValueSpecs(file *ast.File) map[*ast.ValueSpec]bool {
	specs := make(map[*ast.ValueSpec]bool)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, raw := range general.Specs {
			if spec, isValue := raw.(*ast.ValueSpec); isValue {
				specs[spec] = true
			}
		}
	}
	return specs
}

func oraclePropagateResultValues(
	file *ast.File,
	imports map[string]string,
	values map[oracleObjectIdentity]oracleResultKind,
) bool {
	changed := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			changed = oracleBindAssignment(value.Lhs, value.Rhs, values, imports) || changed
			changed = oracleBindReturnedSources(value.Lhs, value.Rhs, values) || changed
		case *ast.ValueSpec:
			changed = oracleBindIdentifiers(value.Names, value.Values, values, imports) || changed
			changed = oracleBindReturnedValueSpec(value, values) || changed
		case *ast.RangeStmt:
			changed = oracleBindRangeValue(value, values, imports) || changed
		}
		return true
	})
	return changed
}

func oracleBindReturnedValueSpec(
	spec *ast.ValueSpec,
	known map[oracleObjectIdentity]oracleResultKind,
) bool {
	targets := make([]ast.Expr, len(spec.Names))
	for index := range spec.Names {
		targets[index] = spec.Names[index]
	}
	return oracleBindReturnedSources(targets, spec.Values, known)
}

func oracleBindRangeValue(
	statement *ast.RangeStmt,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) bool {
	if oracleResultExpressionKind(statement.X, known, imports) != oracleResultContent {
		return false
	}
	identifier, ok := statement.Value.(*ast.Ident)
	return ok && oracleBind(identifier, oracleResultItem, known)
}

func oracleSeedReturnedResults(
	file *ast.File,
	imports map[string]string,
	known map[oracleObjectIdentity]oracleResultKind,
) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			oracleSeedFunctionReturns(function.Type, function.Body, imports, known)
		case *ast.FuncLit:
			oracleSeedFunctionReturns(function.Type, function.Body, imports, known)
		}
		return true
	})
}

func oracleSeedFunctionReturns(
	function *ast.FuncType,
	body *ast.BlockStmt,
	imports map[string]string,
	known map[oracleObjectIdentity]oracleResultKind,
) {
	indexes := oracleToolResultIndexes(function.Results, imports)
	if len(indexes) == 0 || body == nil {
		return
	}
	oracleSeedNamedResults(function.Results, indexes, known)
	ast.Inspect(body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		returned, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, index := range indexes {
			if index < len(returned.Results) {
				oracleBindReturnedExpression(returned.Results[index], known)
			}
		}
		return true
	})
}

func oracleToolResultIndexes(results *ast.FieldList, imports map[string]string) []int {
	if results == nil {
		return nil
	}
	var indexes []int
	position := 0
	for _, result := range results.List {
		width := max(1, len(result.Names))
		pointer, ok := result.Type.(*ast.StarExpr)
		if ok && outputOracleName(pointer.X, imports) == "mcp.ToolResult" {
			for offset := 0; offset < width; offset++ {
				indexes = append(indexes, position+offset)
			}
		}
		position += width
	}
	return indexes
}

func oracleSeedNamedResults(
	results *ast.FieldList,
	indexes []int,
	known map[oracleObjectIdentity]oracleResultKind,
) {
	positions := make(map[int]bool, len(indexes))
	for _, index := range indexes {
		positions[index] = true
	}
	position := 0
	for _, result := range results.List {
		for _, name := range result.Names {
			if positions[position] {
				oracleBind(name, oracleResultValue, known)
			}
			position++
		}
		if len(result.Names) == 0 {
			position++
		}
	}
}

func oracleBindReturnedSources(
	targets []ast.Expr,
	values []ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
) bool {
	changed := false
	for index, target := range targets {
		identifier, ok := target.(*ast.Ident)
		identity, declared := oracleIdentifierIdentity(identifier)
		if !ok || !declared || known[identity] != oracleResultValue || index >= len(values) {
			continue
		}
		changed = oracleBindReturnedExpression(values[index], known) || changed
	}
	return changed
}

func oracleBindReturnedExpression(
	expression ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
) bool {
	for depth := 0; depth < 32; depth++ {
		switch value := expression.(type) {
		case *ast.Ident:
			return oracleBind(value, oracleResultValue, known)
		case *ast.ParenExpr:
			expression = value.X
		case *ast.StarExpr:
			expression = value.X
		case *ast.UnaryExpr:
			expression = value.X
		default:
			return false
		}
	}
	return false
}

func oracleBindAssignment(
	targets []ast.Expr,
	values []ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) bool {
	identifiers := make([]*ast.Ident, len(targets))
	for index, target := range targets {
		identifier, ok := target.(*ast.Ident)
		if ok {
			identifiers[index] = identifier
		}
	}
	return oracleBindIdentifiers(identifiers, values, known, imports)
}

func oracleBindIdentifiers(
	targets []*ast.Ident,
	values []ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) bool {
	changed := false
	for index, target := range targets {
		if target == nil || index >= len(values) {
			continue
		}
		kind := oracleResultExpressionKind(values[index], known, imports)
		changed = oracleBind(target, kind, known) || changed
	}
	return changed
}

func oracleBind(
	identifier *ast.Ident,
	kind oracleResultKind,
	known map[oracleObjectIdentity]oracleResultKind,
) bool {
	identity, declared := oracleIdentifierIdentity(identifier)
	if !declared || kind == oracleResultNone || known[identity] == kind {
		return false
	}
	known[identity] = kind
	return true
}

func oracleRejectAssignment(
	statement *ast.AssignStmt,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) error {
	for _, target := range statement.Lhs {
		if _, identifier := target.(*ast.Ident); identifier {
			continue
		}
		if oracleSensitiveResultPath(target) ||
			oracleResultExpressionKind(target, known, imports) != oracleResultNone {
			return errors.New("post-construction result mutation bypasses independent output census")
		}
	}
	for index, target := range statement.Lhs {
		if _, identifier := target.(*ast.Ident); identifier || index >= len(statement.Rhs) {
			continue
		}
		if oracleResultExpressionKind(statement.Rhs[index], known, imports) != oracleResultNone {
			return errors.New("post-construction result assignment escapes independent output census")
		}
	}
	return nil
}

func oracleRejectCompositeEscape(
	literal *ast.CompositeLit,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) error {
	for _, element := range literal.Elts {
		if oracleResultExpressionKind(element, known, imports) != oracleResultNone {
			return errors.New("post-construction result composite escapes independent output census")
		}
	}
	return nil
}

func oracleRejectMutationTarget(
	target ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) error {
	if oracleSensitiveResultPath(target) || oracleResultExpressionKind(target, known, imports) != oracleResultNone {
		return errors.New("post-construction result mutation bypasses independent output census")
	}
	return nil
}

func oracleSensitiveResultPath(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Content" || selector.Sel.Name == "Text" || selector.Sel.Name == "Type") {
			found = true
		}
		return !found
	})
	return found
}

func oracleRejectResultEscape(
	call *ast.CallExpr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) error {
	name := outputOracleName(call.Fun, imports)
	if (name == "len" || name == "cap") && len(call.Args) == 1 {
		return nil
	}
	if selector, ok := call.Fun.(*ast.SelectorExpr); ok &&
		oracleResultExpressionKind(selector.X, known, imports) != oracleResultNone {
		return errors.New("post-construction result receiver escapes independent output census")
	}
	for _, argument := range call.Args {
		if oracleResultExpressionKind(argument, known, imports) != oracleResultNone {
			return errors.New("post-construction result argument escapes independent output census")
		}
	}
	return nil
}

// oracleResultExpressionKind records the wrapper chain from expression down to its leaf,
// then evaluates the leaf and replays index and selector links outward. An over-deep chain
// counts as a result value so the census fails closed.
func oracleResultExpressionKind(
	expression ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) oracleResultKind {
	chain := make([]ast.Expr, 0, 4)
	for depth := 0; depth < 32; depth++ {
		chain = append(chain, expression)
		inner := oracleInnerExpression(expression)
		if inner == nil {
			return oracleReplayResultChain(chain, known, imports)
		}
		expression = inner
	}
	return oracleResultValue
}

func oracleInnerExpression(expression ast.Expr) ast.Expr {
	switch value := expression.(type) {
	case *ast.ParenExpr:
		return value.X
	case *ast.StarExpr:
		return value.X
	case *ast.UnaryExpr:
		return value.X
	case *ast.IndexExpr:
		return value.X
	case *ast.SliceExpr:
		return value.X
	case *ast.TypeAssertExpr:
		return value.X
	case *ast.KeyValueExpr:
		return value.Value
	case *ast.SelectorExpr:
		return value.X
	}
	return nil
}

func oracleReplayResultChain(
	chain []ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) oracleResultKind {
	kind := oracleLeafResultKind(chain[len(chain)-1], known, imports)
	for index := len(chain) - 2; index >= 0; index-- {
		switch link := chain[index].(type) {
		case *ast.IndexExpr:
			kind = oracleIndexedResultKind(kind)
		case *ast.SelectorExpr:
			kind = oracleSelectedResultKind(kind, link.Sel.Name)
		}
	}
	return kind
}

func oracleLeafResultKind(
	expression ast.Expr,
	known map[oracleObjectIdentity]oracleResultKind,
	imports map[string]string,
) oracleResultKind {
	switch value := expression.(type) {
	case *ast.Ident:
		identity, declared := oracleIdentifierIdentity(value)
		if !declared {
			return oracleResultNone
		}
		return known[identity]
	case *ast.CallExpr:
		if oracleResultConstructor(outputOracleName(value.Fun, imports)) {
			return oracleResultValue
		}
	}
	return oracleResultNone
}

func oracleIndexedResultKind(kind oracleResultKind) oracleResultKind {
	if kind == oracleResultContent {
		return oracleResultItem
	}
	return kind
}

func oracleSelectedResultKind(kind oracleResultKind, field string) oracleResultKind {
	if kind == oracleResultValue && field == "Content" {
		return oracleResultContent
	}
	if kind == oracleResultItem && (field == "Text" || field == "Type") {
		return oracleResultText
	}
	return oracleResultNone
}

func oracleResultConstructor(name string) bool {
	switch name {
	case "mcp.TextResult", "mcp.ErrorResult", "mcpTextResult", "mcpErrorResult",
		"mcpComposedTextResult", "mcpComposedErrorResult":
		return true
	}
	return false
}

func oracleAllowedResultTypeReferences(file *ast.File, imports map[string]string) map[*ast.SelectorExpr]bool {
	allowed := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncType)
		if !ok || function.Results == nil {
			return true
		}
		for _, result := range function.Results.List {
			expression := result.Type
			if pointer, pointerOK := expression.(*ast.StarExpr); pointerOK {
				selector, selectorOK := pointer.X.(*ast.SelectorExpr)
				if selectorOK && outputOracleName(selector, imports) == "mcp.ToolResult" {
					allowed[selector] = true
				}
			}
		}
		return true
	})
	return allowed
}

func classifyOutputCall(function *ast.FuncDecl, call *ast.CallExpr, imports map[string]string) (string, string, bool, error) {
	callee := outputOracleName(call.Fun, imports)
	switch callee {
	case "mcp.TextResult", "mcp.ErrorResult":
		return "result", callee, function == nil || !oracleHelper(function.Name.Name), nil
	case "mcpTextResult", "mcpErrorResult":
		return "classified-result", callee, true, nil
	case "mcpComposedTextResult", "mcpComposedErrorResult":
		if len(call.Args) != 1 || !oracleGovernedArgument(call.Args[0]) {
			return "", callee, false, fmt.Errorf("ungoverned composed output argument for %s", callee)
		}
		return "governed-result", callee, true, nil
	case "mcpTextf":
		return "template", callee, true, nil
	case "http.Error":
		return "http-error", callee, true, nil
	case "fmt.Fprintf", "fmt.Fprint", "fmt.Fprintln":
		if oracleBuilderHelper(function, "Template") || oracleArgName(call, 0, imports) == "os.Stderr" {
			return "", callee, false, nil
		}
		return classifiedWire(call, callee, imports)
	case "io.WriteString":
		return classifiedWire(call, callee, imports)
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", callee, false, nil
	}
	switch selector.Sel.Name {
	case "Template", "External", "Append":
		return "builder-" + strings.ToLower(selector.Sel.Name), callee, true, nil
	case "WriteString":
		if oracleBuilderHelper(function, "External") || oracleBuilderHelper(function, "Append") {
			return "", callee, false, nil
		}
		return "", callee, false, fmt.Errorf("unclassified output method %s", callee)
	}
	return "", callee, false, nil
}

func oracleGovernedArgument(expression ast.Expr) bool {
	if _, ok := expression.(*ast.Ident); ok {
		return true
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	if _, bare := call.Fun.(*ast.Ident); bare {
		return true
	}
	selector, selected := call.Fun.(*ast.SelectorExpr)
	return selected && selector.Sel.Name == "Text"
}

func classifiedWire(call *ast.CallExpr, callee string, imports map[string]string) (string, string, bool, error) {
	if oracleArgName(call, 1, imports) == "mcpClassifiedText" {
		return "wire-format", callee, true, nil
	}
	return "", callee, false, fmt.Errorf("unclassified output call %s", callee)
}

func outputOracleImports(file *ast.File) (map[string]string, error) {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		canonical := map[string]string{"fmt": "fmt", "io": "io", "os": "os", "net/http": "http",
			"github.com/cordanaLLM/praetor/internal/mcp": "mcp"}[path]
		if err != nil || canonical == "" {
			continue
		}
		name := canonical
		if spec.Name != nil {
			name = spec.Name.Name
		}
		if name == "." {
			return nil, fmt.Errorf("output census rejects dot import %s", path)
		}
		if name != "_" {
			imports[name] = canonical
		}
	}
	return imports, nil
}

func outputOracleName(expression ast.Expr, imports map[string]string) string {
	parts := make([]string, 0, 4)
	for depth := 0; depth < 32; depth++ {
		switch value := expression.(type) {
		case *ast.Ident:
			name := value.Name
			if canonical := imports[name]; canonical != "" {
				name = canonical
			}
			for index := len(parts) - 1; index >= 0; index-- {
				name += "." + parts[index]
			}
			return name
		case *ast.ParenExpr:
			expression = value.X
		case *ast.SelectorExpr:
			parts = append(parts, value.Sel.Name)
			expression = value.X
		default:
			return ""
		}
	}
	return ""
}

func oracleArgName(call *ast.CallExpr, index int, imports map[string]string) string {
	if index >= len(call.Args) {
		return ""
	}
	expression := call.Args[index]
	if nested, ok := expression.(*ast.CallExpr); ok {
		expression = nested.Fun
	}
	return outputOracleName(expression, imports)
}

func oracleHelper(name string) bool {
	return name == "mcpTextResult" || name == "mcpErrorResult" ||
		name == "mcpComposedTextResult" || name == "mcpComposedErrorResult"
}

func oracleBuilderHelper(function *ast.FuncDecl, name string) bool {
	if function == nil || function.Name.Name != name || function.Recv == nil || len(function.Recv.List) != 1 {
		return false
	}
	typeName := function.Recv.List[0].Type
	if pointer, ok := typeName.(*ast.StarExpr); ok {
		typeName = pointer.X
	}
	identifier, ok := typeName.(*ast.Ident)
	return ok && identifier.Name == "mcpTextBuilder"
}

func outputCallsiteDigest(callsites []outputCallsite, inventory []string) string {
	identities := make([]string, 0, len(callsites)+len(inventory))
	for _, path := range inventory {
		identities = append(identities, "file:"+path)
	}
	for index := range callsites {
		identities = append(identities, "call:"+callsites[index].identity)
	}
	sort.Strings(identities)
	sum := sha256.Sum256([]byte(strings.Join(identities, "\n")))
	return fmt.Sprintf("sha256:%x", sum)
}

func TestIndependentOutputCensusRejectsUnclassifiedCall(t *testing.T) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, "bad.go", `package main
import "fmt"
func output(w interface{ Write([]byte) (int, error) }, text string) { fmt.Fprint(w, text) }
`, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = censusOutputAST(files, "bad.go", parsed); err == nil || !strings.Contains(err.Error(), "unclassified output call") {
		t.Fatalf("unclassified output accepted: %v", err)
	}
}

func TestIndependentOutputCensusRejectsHiddenOutputForms(t *testing.T) {
	fixtures := map[string]string{
		"direct result composite": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output(message string) *mcp.ToolResult {
	mcp.TextResult("result: pass.")
	return &mcp.ToolResult{Content: []mcp.ContentItem{{Type: "text", Text: message}}}
}
`,
		"result aliases": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
type ResultAlias = mcp.ToolResult
type ItemAlias = mcp.ContentItem
func output(message string) *mcp.ToolResult {
	mcp.TextResult("result: pass.")
	return &ResultAlias{Content: []ItemAlias{{Type: "text", Text: message}}}
}
`,
		"named result wrapper": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
type ResultWrapper mcp.ToolResult
func output(message string) *mcp.ToolResult {
	mcp.TextResult("result: pass.")
	return (*mcp.ToolResult)(&ResultWrapper{Content: []mcp.ContentItem{{Type: "text", Text: message}}})
}
`,
		"non composite result construction": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output(message string) *mcp.ToolResult {
	mcp.TextResult("result: pass.")
	result := new(mcp.ToolResult)
	item := new(mcp.ContentItem)
	item.Text = message
	result.Content = append(result.Content, *item)
	return result
}
`,
		"post construction text mutation": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output(message string) *mcp.ToolResult {
	result := mcp.TextResult("result: pass.")
	result.Content[0].Text = message
	return result
}
`,
		"post construction pointer alias mutation": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output(message string) *mcp.ToolResult {
	result := mcp.TextResult("result: pass.")
	item := &result.Content[0]
	text := &item.Text
	*text = message
	return result
}
`,
		"post construction helper escape": `package main
import (
	"encoding/json"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
func output(payload []byte) *mcp.ToolResult {
	result := mcp.TextResult("result: pass.")
	_ = json.Unmarshal(payload, result)
	return result
}
`,
		"wrapped result helper escape": `package main
import (
	"encoding/json"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
func makeResult() *mcp.ToolResult { return mcp.TextResult("result: pass.") }
func output(payload []byte) *mcp.ToolResult {
	result := makeResult()
	alias := result
	_ = json.Unmarshal(payload, result)
	return alias
}
`,
		"result selector assignment escape": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
type resultBox struct { value any }
func output() *mcp.ToolResult {
	result := mcp.TextResult("result: pass.")
	var box resultBox
	box.value = result
	return result
}
`,
		"result keyed composite escape": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output() *mcp.ToolResult {
	result := mcp.TextResult("result: pass.")
	_ = struct{ value any }{value: result}
	return result
}
`,
		"selector collision on governed helper": `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
type mcpGovernedText string
type provider struct { Safe func() mcpGovernedText }
var other = provider{Safe: func() mcpGovernedText { return "This sentence is hidden from Caveman." }}
func Safe() mcpGovernedText { return mcpGovernedText("result: pass.") }
func output() *mcp.ToolResult { return mcpComposedTextResult(other.Safe()) }
`,
		"package builder closure": `package main
import (
	"fmt"
	"strings"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
type mcpTextBuilder struct { value strings.Builder }
func allowBuilder(*mcpTextBuilder) {}
type builderSink struct { allowBuilder func(*mcpTextBuilder, string) }
var sink = builderSink{allowBuilder: func(b *mcpTextBuilder, message string) {
	fmt.Fprint(&b.value, message)
}}
func output(message string) {
	var b mcpTextBuilder
	sink.allowBuilder(&b, message)
	mcp.TextResult("result: pass.")
}
`,
	}
	for name, source := range fixtures {
		t.Run(name, func(t *testing.T) {
			files := token.NewFileSet()
			parsed, err := parser.ParseFile(files, "bad.go", source, 0)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = censusOutputAST(files, "bad.go", parsed); err == nil {
				t.Fatal("hidden output form accepted")
			}
		})
	}
}

func TestIndependentOutputCensusTracksPackageResultGlobals(t *testing.T) {
	escape := `package main
import (
	"encoding/json"
	"github.com/cordanaLLM/praetor/internal/mcp"
)
func output(payload []byte) *mcp.ToolResult {
	_ = json.Unmarshal(payload, cached)
	return mcp.TextResult("result: pass.")
}
`
	cases := []struct {
		name, declaration string
		reject            bool
	}{
		{"initialized global escapes in sibling file", `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
var cached = mcp.TextResult("result: pass.")
`, true},
		{"returned global escapes in sibling file", `package main
import "github.com/cordanaLLM/praetor/internal/mcp"
var cached = pick()
func pick() any { return nil }
func current() *mcp.ToolResult { return cached }
`, true},
		{"plain global passes to sibling call", `package main
var cached = []byte("{}")
`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			files := token.NewFileSet()
			parsedFiles := make([]outputOracleFile, 0, 2)
			for index, source := range []string{tc.declaration, escape} {
				name := fmt.Sprintf("f%d.go", index)
				parsed, err := parser.ParseFile(files, name, source, 0)
				if err != nil {
					t.Fatal(err)
				}
				imports, err := outputOracleImports(parsed)
				if err != nil {
					t.Fatal(err)
				}
				parsedFiles = append(parsedFiles, outputOracleFile{path: name, file: parsed, imports: imports})
			}
			globals, err := oraclePackageResultNames(parsedFiles)
			if err != nil {
				t.Fatal(err)
			}
			_, err = censusOutputASTWithGlobals(files, parsedFiles[1], globals)
			if tc.reject != (err != nil) {
				t.Fatalf("reject=%t globals=%v err=%v", tc.reject, globals, err)
			}
		})
	}
}

func TestIndependentOutputCensusKeepsPredeclaredNames(t *testing.T) {
	files := token.NewFileSet()
	parsedFiles := make([]outputOracleFile, 0, 2)
	for index, source := range []string{`package main
import "github.com/cordanaLLM/praetor/internal/mcp"
func output() (*mcp.ToolResult, error) { return nil, nil }
`, `package main
func consume(values ...any) {}
func exercise() { consume(nil) }
`} {
		name := fmt.Sprintf("f%d.go", index)
		parsed, err := parser.ParseFile(files, name, source, 0)
		if err != nil {
			t.Fatal(err)
		}
		imports, err := outputOracleImports(parsed)
		if err != nil {
			t.Fatal(err)
		}
		parsedFiles = append(parsedFiles, outputOracleFile{path: name, file: parsed, imports: imports})
	}
	globals, err := oraclePackageResultNames(parsedFiles)
	if err != nil || len(globals) != 0 {
		t.Fatalf("predeclared nil became a result global: %v %v", globals, err)
	}
	if _, err = censusOutputASTWithGlobals(files, parsedFiles[1], globals); err != nil {
		t.Fatalf("nil argument rejected as result escape: %v", err)
	}
	shadow, err := parser.ParseFile(files, "shadow.go", "package main\nvar nil = 0\n", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = censusOutputAST(files, "shadow.go", shadow); err == nil || !strings.Contains(err.Error(), "predeclared identifier nil") {
		t.Fatalf("predeclared shadow accepted: %v", err)
	}
}

func TestIndependentOutputCensusFileBound(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "cmd", "standards-mcp")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	for index := 0; index <= maxOutputOracleFiles; index++ {
		body := "package main\n"
		if index == 0 {
			body += "func output() { mcp.ErrorResult(\"block: stop.\") }\n"
		}
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("f%03d.go", index)), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		_, _, err := independentMCPOutputCallsites(t.Context(), root)
		if index+1 == maxOutputOracleFiles && err != nil {
			t.Fatalf("exact file bound rejected: %v", err)
		}
		if index+1 > maxOutputOracleFiles && err == nil {
			t.Fatal("file bound plus one accepted")
		}
	}
}
