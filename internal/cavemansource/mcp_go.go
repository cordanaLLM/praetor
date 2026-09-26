package cavemansource

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"go/types"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/config"
)

const (
	maxMCPCallsites    = 1024
	maxMCPTypeDepth    = 8
	maxGoSelectorDepth = 32
	maxGoStaticParts   = 1024
	mcpImportPath      = "github.com/cordanaLLM/praetor/internal/mcp"
)

type mcpGoScanner struct {
	item           discoveredInput
	files          *token.FileSet
	sources        []Source
	counts         map[string]int
	helperDecls    map[*ast.FuncDecl]bool
	governed       map[string]bool
	governedDecls  map[*ast.FuncDecl]bool
	packageKey     string
	builderFuncs   map[*ast.FuncDecl]bool
	builderMethods map[string]bool
	globalBuilders map[string]bool
	globalValues   map[string]bool
	globalUnsafe   map[string]bool
	builders       map[string]bool
	values         map[string]bool
	unsafeValues   map[string]bool
	imports        map[string]string
	firstErr       error
	function       string
	receiverName   string
	receiverType   string
	helper         bool
}

func extractGoMCP(item discoveredInput, data []byte, packageGoverned map[string]bool) ([]Source, error) {
	files := token.NewFileSet()
	parsed, err := parser.ParseFile(files, item.path, data, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("caveman source %s: parse go: %w", item.path, err)
	}
	imports, err := resolveMCPImports(parsed)
	if err != nil {
		return nil, fmt.Errorf("caveman source %s: %w", item.path, err)
	}
	scanner := &mcpGoScanner{item: item, files: files, counts: make(map[string]int),
		helperDecls: make(map[*ast.FuncDecl]bool), governed: maps.Clone(packageGoverned),
		governedDecls: make(map[*ast.FuncDecl]bool), packageKey: mcpGoPackageKey(item.path, parsed.Name.Name),
		builderFuncs: make(map[*ast.FuncDecl]bool), builderMethods: make(map[string]bool),
		globalBuilders: make(map[string]bool),
		globalValues:   make(map[string]bool), globalUnsafe: make(map[string]bool), imports: imports}
	if item.input.Selector == "mcp.outputs" {
		scanner.rejectRawMCPResultTypeReferences(parsed)
		scanner.rejectPredeclaredShadows(parsed)
		scanner.rejectMCPResultMutations(parsed)
		scanner.rejectAmbiguousBuilderClosures(parsed)
		scanner.findGovernedFunctions(parsed)
		scanner.findBuilderFunctions(parsed)
		scanner.validateHelpers(parsed)
		scanner.scanGlobalBindings(parsed)
		scanner.rejectOutputReferences(parsed)
	} else {
		scanner.scanSchemaDescriptions(parsed)
	}
	if scanner.firstErr != nil {
		return nil, scanner.firstErr
	}
	scanner.scanFunctions(parsed)
	if scanner.firstErr != nil {
		return nil, scanner.firstErr
	}
	return scanner.sources, nil
}

type mcpResultValueKind uint8

const (
	mcpResultValueNone mcpResultValueKind = iota
	mcpResultValue
	mcpResultContent
	mcpResultItem
	mcpResultText
)

type mcpResultMutationAnalysis struct {
	scanner      *mcpGoScanner
	values       map[goObjectIdentity]mcpResultValueKind
	packageSpecs map[*ast.ValueSpec]bool
}

type goObjectIdentity struct {
	declaration any
	name        string
}

// goIdentifierIdentity keys an identifier by its resolved declaration. Unresolved names
// are package objects declared in a sibling file, except predeclared identifiers such as
// nil: those never name a result value, and rejectPredeclaredShadows forbids a package
// declaration from redefining them.
func goIdentifierIdentity(identifier *ast.Ident) (goObjectIdentity, bool) {
	if identifier == nil || identifier.Name == "" || predeclaredGoIdentifier(identifier) {
		return goObjectIdentity{}, false
	}
	if identifier.Obj == nil || identifier.Obj.Decl == nil {
		return goObjectIdentity{name: identifier.Name}, true
	}
	return goObjectIdentity{declaration: identifier.Obj.Decl, name: identifier.Obj.Name}, true
}

func predeclaredGoIdentifier(identifier *ast.Ident) bool {
	return identifier.Obj == nil && types.Universe.Lookup(identifier.Name) != nil
}

// rejectPredeclaredShadows fails a package declaration that redefines a predeclared
// identifier, because sibling files resolve that name to the universe object.
func (s *mcpGoScanner) rejectPredeclaredShadows(file *ast.File) {
	if s.firstErr != nil {
		return
	}
	for _, declaration := range file.Decls {
		for _, name := range packageDeclarationNames(declaration) {
			if types.Universe.Lookup(name.Name) != nil {
				s.fail(name, "package declaration shadows predeclared identifier "+name.Name+" and hides it from the output census")
				return
			}
		}
	}
}

func packageDeclarationNames(declaration ast.Decl) []*ast.Ident {
	switch value := declaration.(type) {
	case *ast.FuncDecl:
		if value.Recv == nil {
			return []*ast.Ident{value.Name}
		}
	case *ast.GenDecl:
		var names []*ast.Ident
		for _, raw := range value.Specs {
			switch spec := raw.(type) {
			case *ast.ValueSpec:
				names = append(names, spec.Names...)
			case *ast.TypeSpec:
				names = append(names, spec.Name)
			}
		}
		return names
	}
	return nil
}

func (s *mcpGoScanner) rejectMCPResultMutations(file *ast.File) {
	analysis := &mcpResultMutationAnalysis{scanner: s, values: make(map[goObjectIdentity]mcpResultValueKind),
		packageSpecs: packageValueSpecs(file)}
	analysis.seedReturnedResults(file)
	ast.Inspect(file, analysis.inspect)
}

func (a *mcpResultMutationAnalysis) seedReturnedResults(file *ast.File) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch function := node.(type) {
		case *ast.FuncDecl:
			a.seedFunctionReturns(function.Type, function.Body)
		case *ast.FuncLit:
			a.seedFunctionReturns(function.Type, function.Body)
		}
		return a.scanner.firstErr == nil
	})
	for pass := 0; pass < maxGoSelectorDepth; pass++ {
		changed := a.seedReturnedAliasSources(file)
		if !changed {
			return
		}
	}
	if !a.seedReturnedAliasSources(file) {
		return
	}
	a.scanner.fail(file, fmt.Sprintf("mcp result alias chain exceeds %d", maxGoSelectorDepth))
}

func (a *mcpResultMutationAnalysis) seedFunctionReturns(function *ast.FuncType, body *ast.BlockStmt) {
	indexes := a.toolResultIndexes(function.Results)
	if len(indexes) == 0 || body == nil {
		return
	}
	a.seedNamedResults(function.Results, indexes)
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
				a.bindExpression(returned.Results[index], mcpResultValue)
			}
		}
		return true
	})
}

func (a *mcpResultMutationAnalysis) toolResultIndexes(results *ast.FieldList) []int {
	if results == nil {
		return nil
	}
	indexes := make([]int, 0, len(results.List))
	position := 0
	for _, result := range results.List {
		width := max(1, len(result.Names))
		if a.scanner.pointerMCPToolResult(result.Type) != nil {
			for offset := 0; offset < width; offset++ {
				indexes = append(indexes, position+offset)
			}
		}
		position += width
	}
	return indexes
}

func (a *mcpResultMutationAnalysis) seedNamedResults(results *ast.FieldList, indexes []int) {
	positions := make(map[int]bool, len(indexes))
	for _, index := range indexes {
		positions[index] = true
	}
	position := 0
	for _, result := range results.List {
		for _, name := range result.Names {
			if positions[position] {
				a.bind(name, mcpResultValue)
			}
			position++
		}
		if len(result.Names) == 0 {
			position++
		}
	}
}

func (a *mcpResultMutationAnalysis) seedReturnedAliasSources(file *ast.File) bool {
	changed := false
	ast.Inspect(file, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.AssignStmt:
			changed = a.seedAliasSources(value.Lhs, value.Rhs) || changed
		case *ast.ValueSpec:
			targets := make([]ast.Expr, len(value.Names))
			for index := range value.Names {
				targets[index] = value.Names[index]
			}
			changed = a.seedAliasSources(targets, value.Values) || changed
		}
		return true
	})
	return changed
}

func (a *mcpResultMutationAnalysis) seedAliasSources(targets, values []ast.Expr) bool {
	changed := false
	for index, target := range targets {
		identifier, ok := target.(*ast.Ident)
		identity, declared := goIdentifierIdentity(identifier)
		if !ok || !declared || a.values[identity] != mcpResultValue || index >= len(values) {
			continue
		}
		changed = a.bindExpression(values[index], mcpResultValue) || changed
	}
	return changed
}

func (a *mcpResultMutationAnalysis) bindExpression(expression ast.Expr, kind mcpResultValueKind) bool {
	identifier, ok := unwrapGoReference(expression).(*ast.Ident)
	return ok && a.bind(identifier, kind)
}

func (a *mcpResultMutationAnalysis) inspect(node ast.Node) bool {
	if a.scanner.firstErr != nil {
		return false
	}
	switch value := node.(type) {
	case *ast.AssignStmt:
		a.scanAssignment(value)
	case *ast.ValueSpec:
		a.scanValueSpec(value)
	case *ast.RangeStmt:
		a.scanRange(value)
	case *ast.IncDecStmt:
		a.rejectMutationTarget(value.X)
	case *ast.CallExpr:
		a.rejectResultEscape(value)
	case *ast.SendStmt:
		a.rejectEscapingExpression(value.Value)
	case *ast.CompositeLit:
		a.rejectCompositeEscape(value)
	}
	return a.scanner.firstErr == nil
}

func (a *mcpResultMutationAnalysis) scanAssignment(statement *ast.AssignStmt) {
	for index, target := range statement.Lhs {
		a.rejectMutationTarget(target)
		if _, identifier := target.(*ast.Ident); !identifier &&
			assignmentExpressionKind(statement.Rhs, index, a.expressionKind) != mcpResultValueNone {
			a.scanner.fail(statement.Rhs[min(index, len(statement.Rhs)-1)],
				"post-construction mcp result escape bypasses the output census")
		}
	}
	for index, target := range statement.Lhs {
		identifier, ok := target.(*ast.Ident)
		if !ok {
			continue
		}
		a.bind(identifier, assignmentExpressionKind(statement.Rhs, index, a.expressionKind))
	}
}

func assignmentExpressionKind(
	values []ast.Expr,
	index int,
	kind func(ast.Expr) mcpResultValueKind,
) mcpResultValueKind {
	if index < len(values) {
		return kind(values[index])
	}
	if len(values) == 1 && index == 0 {
		return kind(values[0])
	}
	return mcpResultValueNone
}

func (a *mcpResultMutationAnalysis) scanValueSpec(spec *ast.ValueSpec) {
	for index, name := range spec.Names {
		a.bind(name, assignmentExpressionKind(spec.Values, index, a.expressionKind))
	}
}

func (a *mcpResultMutationAnalysis) scanRange(statement *ast.RangeStmt) {
	if a.expressionKind(statement.X) != mcpResultContent {
		return
	}
	if value, ok := statement.Value.(*ast.Ident); ok {
		a.bind(value, mcpResultItem)
	}
}

func (a *mcpResultMutationAnalysis) bind(identifier *ast.Ident, kind mcpResultValueKind) bool {
	identity, declared := goIdentifierIdentity(identifier)
	if !declared || kind == mcpResultValueNone || a.values[identity] == kind {
		return false
	}
	a.values[identity] = kind
	return true
}

func (a *mcpResultMutationAnalysis) rejectMutationTarget(expression ast.Expr) {
	if _, ok := expression.(*ast.Ident); ok {
		return
	}
	if sensitiveMCPResultPath(expression) || a.expressionKind(expression) != mcpResultValueNone {
		a.scanner.fail(expression, "post-construction mcp result mutation bypasses the output census")
	}
}

func sensitiveMCPResultPath(expression ast.Expr) bool {
	found := false
	ast.Inspect(expression, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if ok && (selector.Sel.Name == "Content" || selector.Sel.Name == "Text" || selector.Sel.Name == "Type") {
			found = true
			return false
		}
		return !found
	})
	return found
}

func (a *mcpResultMutationAnalysis) rejectResultEscape(call *ast.CallExpr) {
	name := a.scanner.callName(call.Fun)
	if (name == "len" || name == "cap") && len(call.Args) == 1 {
		return
	}
	if selector, ok := unwrapGoReference(call.Fun).(*ast.SelectorExpr); ok &&
		a.expressionKind(selector.X) != mcpResultValueNone {
		a.scanner.fail(call, "post-construction mcp result escape bypasses the output census")
		return
	}
	for _, argument := range call.Args {
		if a.expressionKind(argument) != mcpResultValueNone {
			a.scanner.fail(argument, "post-construction mcp result escape bypasses the output census")
			return
		}
	}
}

func (a *mcpResultMutationAnalysis) rejectEscapingExpression(expression ast.Expr) {
	if a.expressionKind(expression) != mcpResultValueNone {
		a.scanner.fail(expression, "post-construction mcp result escape bypasses the output census")
	}
}

// packageResultGlobal reports whether identity names a package-level variable that any
// census file initializes from, or returns as, a tool result. Sibling-file references are
// unresolved; same-file references resolve to one of this file's package value specs.
func (a *mcpResultMutationAnalysis) packageResultGlobal(identity goObjectIdentity) bool {
	if identity.declaration != nil {
		spec, ok := identity.declaration.(*ast.ValueSpec)
		if !ok || !a.packageSpecs[spec] {
			return false
		}
	}
	return a.scanner.governed[mcpGoResultValueKey(a.scanner.packageKey, identity.name)]
}

func (a *mcpResultMutationAnalysis) rejectCompositeEscape(literal *ast.CompositeLit) {
	for _, element := range literal.Elts {
		a.rejectEscapingExpression(element)
	}
}

type mcpResultStep struct {
	index    bool
	selector bool
	field    string
}

// expressionKind classifies expression by walking its wrapper chain iteratively down to the
// leaf, then applying index and selector steps outward. The walk is bounded; an expression
// deeper than maxGoSelectorDepth fails the census instead of being assumed harmless.
func (a *mcpResultMutationAnalysis) expressionKind(expression ast.Expr) mcpResultValueKind {
	steps := make([]mcpResultStep, 0, 4)
	for depth := 0; depth < maxGoSelectorDepth; depth++ {
		inner, step, wrapped := unwrapMCPResultExpression(expression)
		if !wrapped {
			kind := a.leafExpressionKind(expression)
			for index := len(steps) - 1; index >= 0; index-- {
				kind = applyMCPResultStep(kind, steps[index])
			}
			return kind
		}
		steps = append(steps, step)
		expression = inner
	}
	a.scanner.fail(expression, fmt.Sprintf("mcp result expression exceeds depth %d", maxGoSelectorDepth))
	return mcpResultValueNone
}

func (a *mcpResultMutationAnalysis) leafExpressionKind(expression ast.Expr) mcpResultValueKind {
	switch value := expression.(type) {
	case *ast.Ident:
		identity, declared := goIdentifierIdentity(value)
		if !declared {
			return mcpResultValueNone
		}
		if a.packageResultGlobal(identity) {
			return mcpResultValue
		}
		return a.values[identity]
	case *ast.CallExpr:
		if mcpResultConstructor(a.scanner.callName(value.Fun)) {
			return mcpResultValue
		}
	}
	return mcpResultValueNone
}

func unwrapMCPResultExpression(expression ast.Expr) (ast.Expr, mcpResultStep, bool) {
	switch value := expression.(type) {
	case *ast.SelectorExpr:
		return value.X, mcpResultStep{selector: true, field: value.Sel.Name}, true
	case *ast.IndexExpr:
		return value.X, mcpResultStep{index: true}, true
	case *ast.ParenExpr:
		return value.X, mcpResultStep{}, true
	case *ast.StarExpr:
		return value.X, mcpResultStep{}, true
	case *ast.UnaryExpr:
		return value.X, mcpResultStep{}, true
	case *ast.SliceExpr:
		return value.X, mcpResultStep{}, true
	case *ast.TypeAssertExpr:
		return value.X, mcpResultStep{}, true
	case *ast.KeyValueExpr:
		return value.Value, mcpResultStep{}, true
	}
	return expression, mcpResultStep{}, false
}

func applyMCPResultStep(kind mcpResultValueKind, step mcpResultStep) mcpResultValueKind {
	switch {
	case step.selector:
		return selectedMCPResultKind(kind, step.field)
	case step.index:
		return indexedMCPResultKind(kind)
	}
	return kind
}

func indexedMCPResultKind(kind mcpResultValueKind) mcpResultValueKind {
	if kind == mcpResultContent {
		return mcpResultItem
	}
	return kind
}

func selectedMCPResultKind(kind mcpResultValueKind, field string) mcpResultValueKind {
	switch {
	case kind == mcpResultValue && field == "Content":
		return mcpResultContent
	case kind == mcpResultItem && (field == "Text" || field == "Type"):
		return mcpResultText
	}
	return mcpResultValueNone
}

func mcpResultConstructor(name string) bool {
	switch name {
	case "mcp.TextResult", "mcp.ErrorResult", "mcpTextResult", "mcpErrorResult",
		"mcpComposedTextResult", "mcpComposedErrorResult":
		return true
	}
	return false
}

func collectMCPGovernedFunctions(items []discoveredInput, reader *sourceReader) (map[string]bool, error) {
	governed := make(map[string]bool)
	for index := range items {
		item := items[index]
		if item.input.Format != config.SourceFormatGo || item.input.Selector != "mcp.outputs" {
			continue
		}
		if err := collectMCPGovernedFile(item, reader, governed); err != nil {
			return nil, err
		}
	}
	return governed, nil
}

func collectMCPGovernedFile(item discoveredInput, reader *sourceReader, governed map[string]bool) error {
	data, err := reader.read(item.path)
	if err != nil {
		return err
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), item.path, data, 0)
	if err != nil {
		return fmt.Errorf("caveman source %s: parse go: %w", item.path, err)
	}
	if len(parsed.Decls) > maxGoSourceDeclarations {
		return fmt.Errorf("caveman source %s: go source exceeds %d declarations", item.path, maxGoSourceDeclarations)
	}
	packageKey := mcpGoPackageKey(item.path, parsed.Name.Name)
	imports, err := resolveMCPImports(parsed)
	if err != nil {
		return fmt.Errorf("caveman source %s: %w", item.path, err)
	}
	for _, declaration := range parsed.Decls {
		collectMCPGovernedDeclaration(declaration, packageKey, governed)
	}
	collectMCPResultGlobalFacts(parsed, packageKey, imports, governed)
	return nil
}

func collectMCPGovernedDeclaration(declaration ast.Decl, packageKey string, governed map[string]bool) {
	function, ok := declaration.(*ast.FuncDecl)
	if !ok || len(governedResultIndexes(function.Type.Results)) == 0 {
		return
	}
	if function.Recv == nil {
		governed[mcpGoFunctionKey(packageKey, function.Name.Name)] = true
		return
	}
	if receiverType := localGoReceiverType(function); receiverType != "" {
		governed[mcpGoMethodKey(packageKey, receiverType, function.Name.Name)] = true
	}
}

func mcpGoPackageKey(sourcePath, packageName string) string {
	return path.Dir(sourcePath) + "\x00" + packageName
}

func mcpGoFunctionKey(packageKey, functionName string) string {
	return packageKey + "\x00func\x00" + functionName
}

func mcpGoMethodKey(packageKey, receiverType, methodName string) string {
	return packageKey + "\x00method\x00" + receiverType + "\x00" + methodName
}

func mcpGoResultValueKey(packageKey, valueName string) string {
	return packageKey + "\x00result-value\x00" + valueName
}

func collectMCPResultGlobalFacts(
	file *ast.File,
	packageKey string,
	imports map[string]string,
	facts map[string]bool,
) {
	globals := packageValueSpecs(file)
	for spec := range globals {
		collectInitializedMCPResultGlobals(spec, packageKey, imports, facts)
	}
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if ok {
			collectReturnedMCPResultGlobals(function, packageKey, imports, globals, facts)
		}
	}
}

func packageValueSpecs(file *ast.File) map[*ast.ValueSpec]bool {
	values := make(map[*ast.ValueSpec]bool)
	for _, declaration := range file.Decls {
		general, ok := declaration.(*ast.GenDecl)
		if !ok || general.Tok != token.VAR {
			continue
		}
		for _, raw := range general.Specs {
			if spec, ok := raw.(*ast.ValueSpec); ok {
				values[spec] = true
			}
		}
	}
	return values
}

func collectInitializedMCPResultGlobals(
	spec *ast.ValueSpec,
	packageKey string,
	imports map[string]string,
	facts map[string]bool,
) {
	for index, name := range spec.Names {
		if index >= len(spec.Values) {
			continue
		}
		call, ok := spec.Values[index].(*ast.CallExpr)
		if ok && mcpResultConstructor(canonicalGoName(call.Fun, imports)) {
			facts[mcpGoResultValueKey(packageKey, name.Name)] = true
		}
	}
}

func collectReturnedMCPResultGlobals(
	function *ast.FuncDecl,
	packageKey string,
	imports map[string]string,
	globals map[*ast.ValueSpec]bool,
	facts map[string]bool,
) {
	indexes := mcpToolResultIndexes(function.Type.Results, imports)
	if len(indexes) == 0 || function.Body == nil {
		return
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		returned, ok := node.(*ast.ReturnStmt)
		if ok {
			collectReturnedMCPResultNames(returned, indexes, packageKey, globals, facts)
		}
		return true
	})
}

func collectReturnedMCPResultNames(
	returned *ast.ReturnStmt,
	indexes []int,
	packageKey string,
	globals map[*ast.ValueSpec]bool,
	facts map[string]bool,
) {
	for _, index := range indexes {
		if index >= len(returned.Results) {
			continue
		}
		identifier, ok := unwrapGoReference(returned.Results[index]).(*ast.Ident)
		if ok && packageResultIdentifier(identifier, globals) {
			facts[mcpGoResultValueKey(packageKey, identifier.Name)] = true
		}
	}
}

func packageResultIdentifier(identifier *ast.Ident, globals map[*ast.ValueSpec]bool) bool {
	if identifier.Obj == nil {
		return !predeclaredGoIdentifier(identifier)
	}
	declaration, ok := identifier.Obj.Decl.(*ast.ValueSpec)
	return ok && globals[declaration]
}

func mcpToolResultIndexes(results *ast.FieldList, imports map[string]string) []int {
	if results == nil {
		return nil
	}
	var indexes []int
	position := 0
	for _, result := range results.List {
		width := max(1, len(result.Names))
		if mcpPointerToolResult(result.Type, imports) {
			for offset := 0; offset < width; offset++ {
				indexes = append(indexes, position+offset)
			}
		}
		position += width
	}
	return indexes
}

func mcpPointerToolResult(expression ast.Expr, imports map[string]string) bool {
	pointer, ok := expression.(*ast.StarExpr)
	return ok && canonicalGoName(pointer.X, imports) == "mcp.ToolResult"
}

func localGoReceiverType(function *ast.FuncDecl) string {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return ""
	}
	expression := function.Recv.List[0].Type
	if pointer, ok := expression.(*ast.StarExpr); ok {
		expression = pointer.X
	}
	return goIdentifier(expression)
}

func resolveMCPImports(file *ast.File) (map[string]string, error) {
	imports := make(map[string]string)
	for _, spec := range file.Imports {
		importPath, err := strconv.Unquote(spec.Path.Value)
		canonical, governed := governedGoImport(importPath)
		if err != nil || !governed {
			continue
		}
		if spec.Name == nil {
			imports[canonical] = canonical
			continue
		}
		if spec.Name.Name == "." {
			return nil, fmt.Errorf("dot import of %s is unsupported", importPath)
		}
		if spec.Name.Name == "_" {
			if importPath == mcpImportPath {
				return nil, errors.New("blank import of internal/mcp is unsupported")
			}
			continue
		}
		imports[spec.Name.Name] = canonical
	}
	return imports, nil
}

func governedGoImport(importPath string) (string, bool) {
	switch importPath {
	case mcpImportPath:
		return "mcp", true
	case "fmt":
		return "fmt", true
	case "io":
		return "io", true
	case "net/http":
		return "http", true
	case "strings":
		return "strings", true
	default:
		return "", false
	}
}

func (s *mcpGoScanner) scanFunctions(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Body == nil {
			continue
		}
		s.function = function.Name.Name
		if s.item.input.Selector == "mcp.outputs" {
			s.receiverName, s.receiverType = functionReceiver(function, s)
			s.helper = s.helperDecls[function]
			s.builders = maps.Clone(s.globalBuilders)
			s.values = maps.Clone(s.globalValues)
			s.unsafeValues = maps.Clone(s.globalUnsafe)
			s.collectFunctionTextTypes(function)
			s.validateGovernedReturns(function)
			s.rejectOutputReferences(function.Body)
		}
		ast.Inspect(function.Body, s.inspect)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) scanGlobalBindings(file *ast.File) {
	if len(file.Decls) > maxGoSourceDeclarations {
		s.fail(file, fmt.Sprintf("go source exceeds %d declarations", maxGoSourceDeclarations))
		return
	}
	s.builders = s.globalBuilders
	s.values = s.globalValues
	s.unsafeValues = s.globalUnsafe
	s.function = "<package>"
	for _, declaration := range file.Decls {
		s.scanGlobalDeclaration(declaration)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) scanGlobalDeclaration(declaration ast.Decl) {
	general, ok := declaration.(*ast.GenDecl)
	if !ok || general.Tok != token.VAR {
		return
	}
	for _, rawSpec := range general.Specs {
		spec, ok := rawSpec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		s.scanGlobalValueSpec(spec)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) scanGlobalValueSpec(spec *ast.ValueSpec) {
	s.collectTextValueSpec(spec)
	for _, value := range spec.Values {
		ast.Inspect(value, s.inspect)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) inspect(node ast.Node) bool {
	if s.firstErr != nil {
		return false
	}
	if call, ok := node.(*ast.CallExpr); ok {
		s.scanCall(call)
	}
	if returned, ok := node.(*ast.ReturnStmt); ok {
		s.rejectBuilderReturn(returned)
	}
	if selector, ok := node.(*ast.SelectorExpr); ok && !s.helper && s.builderStorageReference(selector) {
		s.fail(selector, "direct mcpTextBuilder storage access bypasses the output census")
	}
	return s.firstErr == nil
}

func (s *mcpGoScanner) rejectBuilderReturn(statement *ast.ReturnStmt) {
	for _, result := range statement.Results {
		if s.builderReference(result) || s.builderStorageReference(result) {
			s.fail(result, "returning mcpTextBuilder storage bypasses governed templates")
			return
		}
	}
}

func (s *mcpGoScanner) scanCall(call *ast.CallExpr) {
	name := s.callName(call.Fun)
	if s.item.input.Selector == "mcp.descriptions" {
		s.scanToolDescription(name, call)
		return
	}
	if s.rejectBuilderBypass(name, call) || s.scanNamedOutputCall(name, call) {
		return
	}
	s.scanOutputMethod(name, call)
}

func (s *mcpGoScanner) rejectBuilderBypass(name string, call *ast.CallExpr) bool {
	if s.methodReceiverIsBuilder(call.Fun) && !allowedMCPBuilderMethod(builderMethodName(call.Fun)) {
		s.fail(call, "unsupported mcpTextBuilder method bypasses governed templates")
		return true
	}
	if !s.helper && s.builderStorageCall(call.Fun) {
		s.fail(call, "direct mcpTextBuilder storage write bypasses governed templates")
		return true
	}
	if !s.helper && s.callPassesBuilderReference(call) && !s.trustedBuilderCall(call.Fun) {
		s.fail(call, "mcpTextBuilder storage access bypasses governed templates")
		return true
	}
	return false
}

func (s *mcpGoScanner) scanNamedOutputCall(name string, call *ast.CallExpr) bool {
	switch name {
	case "mcp.ErrorResult", "mcp.TextResult":
		s.scanResultCall(name, call)
	case "mcpTextResult", "mcpErrorResult":
		s.scanClassifiedResult(name, call)
	case "mcpComposedTextResult", "mcpComposedErrorResult":
		s.scanComposedResult(name, call)
	case "mcpTextf":
		s.scanTextTemplate(call)
	case "fmt.Fprintf", "fmt.Fprint":
		s.scanBuilderFormat(name, call)
	case "http.Error":
		s.scanHTTPError(call)
	case "io.WriteString":
		s.scanIOWriteString(call)
	case "fmt.Fprintln":
		if s.firstCallArgumentIsBuilder(call) {
			s.fail(call, name+" bypasses mcpTextBuilder.Template")
		}
	default:
		return false
	}
	return true
}

func (s *mcpGoScanner) scanOutputMethod(name string, call *ast.CallExpr) {
	method := builderMethodName(call.Fun)
	switch {
	case method == "Template" && s.methodReceiverIsBuilder(call.Fun):
		s.scanBuilderTemplate(call)
	case method == "External" && s.methodReceiverIsBuilder(call.Fun):
		s.scanBuilderExternal(call)
	case method == "Append" && s.methodReceiverIsBuilder(call.Fun):
		s.scanBuilderAppend(call)
	case method == "WriteString":
		s.scanBuilderString(call)
	case name == "mcpGovernedText" && !s.helper:
		s.fail(call, "direct mcpGovernedText conversion bypasses governed builders")
	}
}

func (s *mcpGoScanner) builderStorageCall(expression ast.Expr) bool {
	method := goSelector(expression)
	return method != nil && s.builderStorageReference(method.X)
}

func allowedMCPBuilderMethod(name string) bool {
	switch lastGoName(name) {
	case "Template", "External", "Append", "Text":
		return true
	default:
		return false
	}
}

func (s *mcpGoScanner) methodReceiverIsBuilder(expression ast.Expr) bool {
	selector := goSelector(expression)
	return selector != nil && s.builderReference(selector.X)
}

func (s *mcpGoScanner) callPassesBuilderReference(call *ast.CallExpr) bool {
	for _, argument := range call.Args {
		if s.builderReference(argument) || s.builderStorageReference(argument) {
			return true
		}
	}
	return false
}

func (s *mcpGoScanner) firstCallArgumentIsBuilder(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	return s.builderReference(call.Args[0]) || s.builderStorageReference(call.Args[0])
}

func (s *mcpGoScanner) builderReference(expression ast.Expr) bool {
	identifier, ok := unwrapGoReference(expression).(*ast.Ident)
	return ok && s.builders[identifier.Name]
}

func (s *mcpGoScanner) builderStorageReference(expression ast.Expr) bool {
	selector, ok := unwrapGoReference(expression).(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "value" && s.builderReference(selector.X)
}

func unwrapGoReference(expression ast.Expr) ast.Expr {
	for depth := 0; depth < maxGoSelectorDepth; depth++ {
		switch value := expression.(type) {
		case *ast.ParenExpr:
			expression = value.X
		case *ast.StarExpr:
			expression = value.X
		case *ast.UnaryExpr:
			if value.Op != token.AND {
				return expression
			}
			expression = value.X
		default:
			return expression
		}
	}
	return expression
}

func goSelector(expression ast.Expr) *ast.SelectorExpr {
	for depth := 0; depth < maxGoSelectorDepth; depth++ {
		parenthesized, ok := expression.(*ast.ParenExpr)
		if !ok {
			selector, selectorOK := expression.(*ast.SelectorExpr)
			if !selectorOK {
				return nil
			}
			return selector
		}
		expression = parenthesized.X
	}
	return nil
}

func builderMethodName(expression ast.Expr) string {
	selector := goSelector(expression)
	if selector == nil {
		return ""
	}
	return selector.Sel.Name
}

func (s *mcpGoScanner) scanTextTemplate(call *ast.CallExpr) {
	if len(call.Args) == 0 {
		s.fail(call, "mcpTextf requires a static format")
		return
	}
	s.addStatic(call.Args[0], s.nextSelector("template"), "mcp text template")
}

func (s *mcpGoScanner) callName(expression ast.Expr) string {
	return canonicalGoName(expression, s.imports)
}

func canonicalGoName(expression ast.Expr, imports map[string]string) string {
	name := goCallName(expression)
	prefix, suffix, found := strings.Cut(name, ".")
	if canonical, ok := imports[prefix]; ok && found {
		return canonical + "." + suffix
	}
	return name
}

func (s *mcpGoScanner) scanToolDescription(name string, call *ast.CallExpr) {
	if name != "mcp.NewReadOnlyTool" && name != "mcp.NewMutatingTool" && name != "mcp.NewOpenWorldTool" {
		return
	}
	if len(call.Args) < 2 {
		s.fail(call, "tool constructor description is missing")
		return
	}
	toolName, ok := goStaticString(call.Args[0])
	if !ok {
		s.fail(call.Args[0], "tool name requires static text")
		return
	}
	s.addStatic(call.Args[1], "tool:"+toolName, "tool description")
}

func (s *mcpGoScanner) scanSchemaDescriptions(file *ast.File) {
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok || s.callName(literal.Type) != "mcp.ToolInputSchema" {
			return true
		}
		s.scanPropertyMap(literal)
		return s.firstErr == nil
	})
}

func (s *mcpGoScanner) scanPropertyMap(schema *ast.CompositeLit) {
	for _, element := range schema.Elts {
		field, ok := element.(*ast.KeyValueExpr)
		if !ok || goIdentifier(field.Key) != "Properties" {
			continue
		}
		properties, ok := field.Value.(*ast.CompositeLit)
		if !ok {
			s.fail(field.Value, "mcp property map requires a static composite literal")
			return
		}
		for _, property := range properties.Elts {
			s.scanProperty(property)
			if s.firstErr != nil {
				return
			}
		}
	}
}

func (s *mcpGoScanner) scanProperty(element ast.Expr) {
	entry, ok := element.(*ast.KeyValueExpr)
	if !ok {
		s.fail(element, "mcp property requires a keyed entry")
		return
	}
	property, ok := entry.Value.(*ast.CompositeLit)
	if !ok {
		s.fail(entry.Value, "mcp property requires a static schema literal")
		return
	}
	for _, element := range property.Elts {
		field, fieldOK := element.(*ast.KeyValueExpr)
		if fieldOK && goIdentifier(field.Key) == "Description" {
			s.addStatic(field.Value, s.nextSelector("property"), "property description")
			return
		}
	}
	s.fail(entry.Value, "mcp property description is missing")
}

func (s *mcpGoScanner) scanResultCall(name string, call *ast.CallExpr) {
	if len(call.Args) != 1 {
		s.fail(call, name+" requires one text argument")
		return
	}
	if s.helper {
		return
	}
	s.addStatic(call.Args[0], s.nextSelector("result"), "tool result")
}

func (s *mcpGoScanner) scanComposedResult(name string, call *ast.CallExpr) {
	if len(call.Args) != 1 || !s.governedExpression(call.Args[0]) {
		s.fail(call, name+" requires text from mcpTextf, mcpTextBuilder.Text, or a governed formatter")
		return
	}
	s.addNotApplicable(call.Args[0], s.nextSelector("governed-result"), "governed-composition")
}

func (s *mcpGoScanner) scanClassifiedResult(name string, call *ast.CallExpr) {
	if len(call.Args) != 2 {
		s.fail(call, name+" requires text plus an explicit supported text class")
		return
	}
	reason, ok := mcpTextClass(call.Args[1])
	if !ok {
		s.fail(call.Args[1], name+" requires an explicit supported text class")
		return
	}
	s.addNotApplicable(call.Args[0], s.nextSelector("classified-result"), reason)
}

func (s *mcpGoScanner) scanBuilderFormat(name string, call *ast.CallExpr) {
	if s.helper {
		return
	}
	if s.firstCallArgumentIsBuilder(call) {
		s.fail(call, name+" bypasses mcpTextBuilder.Template")
		return
	}
	if len(call.Args) < 2 || !goBuilderTarget(call.Args[0]) {
		return
	}
	if s.addClassifiedOutput(call.Args[1], "wire-format") {
		return
	}
	s.addStatic(call.Args[1], s.nextSelector("builder"), "builder output")
}

func (s *mcpGoScanner) scanHTTPError(call *ast.CallExpr) {
	if len(call.Args) != 3 {
		s.fail(call, "http.Error output requires writer, classified text, and status")
		return
	}
	text, reason, ok := classifiedMCPExpression(call.Args[1])
	if !ok || reason != "protocol" && reason != "untrusted-passthrough" {
		s.fail(call.Args[1], "http.Error output requires explicit protocol or untrusted classification")
		return
	}
	s.addNotApplicable(text, s.nextSelector("http-error"), reason)
}

func (s *mcpGoScanner) scanIOWriteString(call *ast.CallExpr) {
	if s.firstCallArgumentIsBuilder(call) {
		s.fail(call, "io.WriteString bypasses mcpTextBuilder.Template")
		return
	}
	if len(call.Args) != 2 || !s.addClassifiedOutput(call.Args[1], "wire-format") {
		s.fail(call, "io.WriteString output requires explicit classification")
	}
}

func (s *mcpGoScanner) addClassifiedOutput(expression ast.Expr, selector string) bool {
	text, reason, ok := classifiedMCPExpression(expression)
	if !ok {
		return false
	}
	s.addNotApplicable(text, s.nextSelector(selector), reason)
	return true
}

func (s *mcpGoScanner) scanBuilderTemplate(call *ast.CallExpr) {
	if len(call.Args) == 0 {
		s.fail(call, "mcpTextBuilder.Template requires a static format")
		return
	}
	s.addStatic(call.Args[0], s.nextSelector("builder-template"), "builder template")
}

func (s *mcpGoScanner) scanBuilderExternal(call *ast.CallExpr) {
	if len(call.Args) != 2 {
		s.fail(call, "mcpTextBuilder.External requires text plus an explicit supported text class")
		return
	}
	reason, ok := mcpTextClass(call.Args[1])
	if !ok {
		s.fail(call.Args[1], "mcpTextBuilder.External requires an explicit supported text class")
		return
	}
	s.addNotApplicable(call.Args[0], s.nextSelector("builder-external"), reason)
}

func (s *mcpGoScanner) scanBuilderAppend(call *ast.CallExpr) {
	if len(call.Args) != 1 || !s.governedExpression(call.Args[0]) {
		s.fail(call, "mcpTextBuilder.Append requires governed text")
		return
	}
	s.addNotApplicable(call.Args[0], s.nextSelector("builder-append"), "governed-composition")
}

func (s *mcpGoScanner) scanBuilderString(call *ast.CallExpr) {
	if len(call.Args) != 1 || s.helper {
		return
	}
	if s.addClassifiedOutput(call.Args[0], "writer-output") {
		return
	}
	s.addStatic(call.Args[0], s.nextSelector("builder"), "builder output")
}

func (s *mcpGoScanner) governedExpression(expression ast.Expr) bool {
	if identifier, ok := expression.(*ast.Ident); ok {
		return s.values[identifier.Name]
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok {
		return false
	}
	if s.governedFunctionCall(call.Fun) {
		return true
	}
	selector, ok := call.Fun.(*ast.SelectorExpr)
	return ok && selector.Sel.Name == "Text" && s.builders[goIdentifier(selector.X)]
}

func (s *mcpGoScanner) governedFunctionCall(expression ast.Expr) bool {
	expression = unwrapGoReference(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		if identifier.Obj == nil {
			return s.governed[mcpGoFunctionKey(s.packageKey, identifier.Name)]
		}
		declaration, declared := identifier.Obj.Decl.(*ast.FuncDecl)
		return declared && s.governedDecls[declaration]
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	receiver, ok := unwrapGoReference(selector.X).(*ast.Ident)
	if !ok {
		return false
	}
	receiverType := s.objectTypeName(receiver)
	return receiverType != "" && s.governed[mcpGoMethodKey(s.packageKey, receiverType, selector.Sel.Name)]
}

func (s *mcpGoScanner) objectTypeName(identifier *ast.Ident) string {
	if identifier == nil || identifier.Obj == nil {
		return ""
	}
	var expression ast.Expr
	switch declaration := identifier.Obj.Decl.(type) {
	case *ast.Field:
		expression = declaration.Type
	case *ast.ValueSpec:
		expression = declaration.Type
	default:
		return ""
	}
	return strings.TrimPrefix(s.typeName(expression), "*")
}

func (s *mcpGoScanner) collectFunctionTextTypes(function *ast.FuncDecl) {
	s.collectBuilderParameters(function)
	ast.Inspect(function.Body, func(node ast.Node) bool {
		switch value := node.(type) {
		case *ast.DeclStmt:
			s.collectTextDeclaration(value)
		case *ast.AssignStmt:
			s.collectTextAssignment(value)
		}
		return true
	})
}

func (s *mcpGoScanner) validateGovernedReturns(function *ast.FuncDecl) {
	indexes := governedResultIndexes(function.Type.Results)
	if len(indexes) == 0 || s.helper {
		return
	}
	ast.Inspect(function.Body, func(node ast.Node) bool {
		if s.firstErr != nil {
			return false
		}
		if _, nested := node.(*ast.FuncLit); nested {
			return false
		}
		returned, ok := node.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, index := range indexes {
			if index >= len(returned.Results) || !s.governedExpression(returned.Results[index]) {
				s.fail(returned, "mcpGovernedText function returns ungoverned text")
				return false
			}
		}
		return true
	})
}

func governedResultIndexes(results *ast.FieldList) []int {
	if results == nil {
		return nil
	}
	indexes := make([]int, 0, len(results.List))
	position := 0
	for _, result := range results.List {
		width := max(1, len(result.Names))
		if goIdentifier(result.Type) == "mcpGovernedText" {
			for offset := 0; offset < width; offset++ {
				indexes = append(indexes, position+offset)
			}
		}
		position += width
	}
	return indexes
}

func (s *mcpGoScanner) collectBuilderParameters(function *ast.FuncDecl) {
	if function.Type.Params == nil {
		return
	}
	for _, parameter := range function.Type.Params.List {
		typeName := s.typeName(parameter.Type)
		if typeName != "*mcpTextBuilder" && typeName != "mcpTextBuilder" {
			continue
		}
		for _, name := range parameter.Names {
			s.builders[name.Name] = true
		}
	}
}

func (s *mcpGoScanner) collectTextDeclaration(statement *ast.DeclStmt) {
	declaration, ok := statement.Decl.(*ast.GenDecl)
	if !ok {
		return
	}
	for _, spec := range declaration.Specs {
		value, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		s.collectTextValueSpec(value)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) collectTextValueSpec(value *ast.ValueSpec) {
	typeName := s.typeName(value.Type)
	for index, name := range value.Names {
		if typeName == "mcpTextBuilder" || typeName == "*mcpTextBuilder" {
			s.builders[name.Name] = true
		}
		if goIdentifier(value.Type) == "mcpGovernedText" {
			s.values[name.Name] = false
		}
		if index < len(value.Values) {
			s.collectTextBinding(name.Name, value.Values[index], value)
		}
	}
}

func (s *mcpGoScanner) collectTextAssignment(statement *ast.AssignStmt) {
	limit := min(len(statement.Lhs), len(statement.Rhs))
	for index := 0; index < limit; index++ {
		name := goIdentifier(statement.Lhs[index])
		if name == "" || name == "_" {
			continue
		}
		s.collectTextBinding(name, statement.Rhs[index], statement)
		if s.firstErr != nil {
			return
		}
	}
}

func (s *mcpGoScanner) collectTextBinding(name string, expression ast.Expr, node ast.Node) {
	if goBuilderInitializer(expression) {
		s.builders[name] = true
		return
	}
	if s.builderReference(expression) || s.builderStorageReference(expression) {
		s.fail(node, "aliasing mcpTextBuilder storage bypasses governed templates")
		return
	}
	if s.governedExpression(expression) {
		s.values[name] = !s.unsafeValues[name]
		return
	}
	if _, governed := s.values[name]; governed {
		s.fail(node, "governed text binding assigned ungoverned text")
		return
	}
	s.unsafeValues[name] = true
	delete(s.values, name)
}

func goBuilderInitializer(expression ast.Expr) bool {
	expression = unwrapGoReference(expression)
	if literal, ok := expression.(*ast.CompositeLit); ok {
		return goIdentifier(literal.Type) == "mcpTextBuilder"
	}
	call, ok := expression.(*ast.CallExpr)
	return ok && goCallName(call.Fun) == "new" && len(call.Args) == 1 &&
		goIdentifier(call.Args[0]) == "mcpTextBuilder"
}

func lastGoName(name string) string {
	if index := strings.LastIndexByte(name, '.'); index >= 0 {
		return name[index+1:]
	}
	return name
}

func (s *mcpGoScanner) findGovernedFunctions(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Recv != nil || function.Type.Results == nil {
			continue
		}
		for _, result := range function.Type.Results.List {
			if goIdentifier(result.Type) == "mcpGovernedText" {
				s.governedDecls[function] = true
			}
		}
	}
}

func (s *mcpGoScanner) findBuilderFunctions(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Type.Params == nil {
			continue
		}
		if !s.fieldListContainsBuilder(function.Type.Params, true) {
			continue
		}
		if function.Recv == nil {
			s.builderFuncs[function] = true
			continue
		}
		_, receiverType := functionReceiver(function, s)
		if receiverType != "" {
			s.builderMethods[receiverType+"\x00"+function.Name.Name] = true
		}
	}
}

func (s *mcpGoScanner) trustedBuilderCall(expression ast.Expr) bool {
	expression = unwrapGoReference(expression)
	if identifier, ok := expression.(*ast.Ident); ok {
		if identifier.Obj == nil {
			return false
		}
		declaration, declared := identifier.Obj.Decl.(*ast.FuncDecl)
		return declared && s.builderFuncs[declaration]
	}
	selector, ok := expression.(*ast.SelectorExpr)
	if !ok || s.receiverName == "" || s.receiverType == "" || selector.Sel.Name == "" {
		return false
	}
	receiver, ok := unwrapGoReference(selector.X).(*ast.Ident)
	return ok && receiver.Name == s.receiverName &&
		s.builderMethods[s.receiverType+"\x00"+selector.Sel.Name]
}

func functionReceiver(function *ast.FuncDecl, scanner *mcpGoScanner) (string, string) {
	if function.Recv == nil || len(function.Recv.List) != 1 {
		return "", ""
	}
	field := function.Recv.List[0]
	if len(field.Names) != 1 {
		return "", ""
	}
	return field.Names[0].Name, strings.TrimPrefix(scanner.typeName(field.Type), "*")
}

func (s *mcpGoScanner) fieldListContainsBuilder(fields *ast.FieldList, pointerOnly bool) bool {
	if fields == nil {
		return false
	}
	for _, field := range fields.List {
		typeName := s.typeName(field.Type)
		if typeName == "*mcpTextBuilder" || !pointerOnly && typeName == "mcpTextBuilder" {
			return true
		}
	}
	return false
}

func (s *mcpGoScanner) rejectRawMCPResultTypeReferences(file *ast.File) {
	allowed := s.allowedMCPResultTypeReferences(file)
	ast.Inspect(file, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || s.firstErr != nil {
			return s.firstErr == nil
		}
		name := s.callName(selector)
		if (name == "mcp.ToolResult" || name == "mcp.ContentItem") && !allowed[selector] {
			s.fail(selector, "direct "+name+" type reference bypasses the output census")
			return false
		}
		return true
	})
}

func (s *mcpGoScanner) allowedMCPResultTypeReferences(file *ast.File) map[*ast.SelectorExpr]bool {
	allowed := make(map[*ast.SelectorExpr]bool)
	ast.Inspect(file, func(node ast.Node) bool {
		function, ok := node.(*ast.FuncType)
		if !ok || function.Results == nil {
			return true
		}
		for _, result := range function.Results.List {
			if selector := s.pointerMCPToolResult(result.Type); selector != nil {
				allowed[selector] = true
			}
		}
		return true
	})
	return allowed
}

func (s *mcpGoScanner) pointerMCPToolResult(expression ast.Expr) *ast.SelectorExpr {
	for depth := 0; depth < maxMCPTypeDepth; depth++ {
		if parenthesized, ok := expression.(*ast.ParenExpr); ok {
			expression = parenthesized.X
			continue
		}
		pointer, ok := expression.(*ast.StarExpr)
		if !ok {
			return nil
		}
		expression = pointer.X
		for nested := 0; nested < maxMCPTypeDepth; nested++ {
			parenthesized, wrapped := expression.(*ast.ParenExpr)
			if !wrapped {
				break
			}
			expression = parenthesized.X
		}
		selector, selected := expression.(*ast.SelectorExpr)
		if selected && s.callName(selector) == "mcp.ToolResult" {
			return selector
		}
		return nil
	}
	return nil
}

func (s *mcpGoScanner) rejectAmbiguousBuilderClosures(file *ast.File) {
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.FuncLit)
		if !ok || s.firstErr != nil {
			return s.firstErr == nil
		}
		if s.fieldListContainsBuilder(literal.Type.Params, false) {
			s.fail(literal, "mcpTextBuilder closure bypasses declaration-bound output analysis")
			return false
		}
		return true
	})
}

func (s *mcpGoScanner) addStatic(expression ast.Expr, selector, label string) {
	text, ok := goStaticTemplate(expression)
	if !ok {
		s.fail(expression, label+" has unclassified dynamic text")
		return
	}
	line := s.files.Position(expression.Pos()).Line
	s.sources = append(s.sources, sourceFrom(s.item, text, selector, line))
	s.checkBound(expression)
}

func (s *mcpGoScanner) addNotApplicable(expression ast.Expr, selector, reason string) {
	text, err := goNodeText(s.files, expression)
	if err != nil {
		s.fail(expression, "render classified mcp expression")
		return
	}
	line := s.files.Position(expression.Pos()).Line
	s.sources = append(s.sources, sourceNotApplicable(s.item, text, selector, line, reason))
	s.checkBound(expression)
}

func (s *mcpGoScanner) checkBound(node ast.Node) {
	if len(s.sources) > maxMCPCallsites {
		s.fail(node, fmt.Sprintf("mcp callsites exceed %d", maxMCPCallsites))
	}
}

func (s *mcpGoScanner) nextSelector(kind string) string {
	index := s.counts[kind]
	s.counts[kind]++
	return fmt.Sprintf("mcp:%s:%03d", kind, index)
}

func (s *mcpGoScanner) fail(node ast.Node, reason string) {
	position := s.files.Position(node.Pos())
	s.firstErr = fmt.Errorf("caveman source %s line %d: %s", s.item.path, position.Line, reason)
}

func (s *mcpGoScanner) validateHelpers(file *ast.File) {
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := function.Name.Name
		if !mcpHelperName(name) {
			continue
		}
		if !s.validHelper(function) {
			s.fail(function, "mcp classification helper does not match its fixed implementation")
			return
		}
		s.helperDecls[function] = true
	}
}

func mcpHelperName(name string) bool {
	switch name {
	case "mcpTextResult", "mcpErrorResult", "mcpClassifiedText", "mcpTextf",
		"mcpComposedTextResult", "mcpComposedErrorResult", "Template", "External", "Append", "Text":
		return true
	}
	return false
}

func (s *mcpGoScanner) validHelper(function *ast.FuncDecl) bool {
	if function.Recv != nil {
		return s.validMCPBuilderMethod(function)
	}
	switch function.Name.Name {
	case "mcpTextResult", "mcpErrorResult":
		return s.validClassifiedResultHelper(function)
	case "mcpClassifiedText":
		return s.validSignature(function, []mcpFieldSpec{{"text", "string"}, {"_", "mcpTextClassification"}}, "string") &&
			goIdentifier(singleReturnExpression(function)) == "text"
	case "mcpTextf":
		return s.validSignature(function, []mcpFieldSpec{{"template", "string"}, {"args", "...any"}}, "mcpGovernedText") &&
			validMCPTextfHelper(singleReturnExpression(function))
	case "mcpComposedTextResult", "mcpComposedErrorResult":
		return s.validComposedResultHelper(function)
	}
	return false
}

type mcpFieldSpec struct {
	name     string
	typeName string
}

func (s *mcpGoScanner) validSignature(function *ast.FuncDecl, params []mcpFieldSpec, result string) bool {
	return s.validParameters(function, params) && s.validResult(function, result)
}

func (s *mcpGoScanner) validParameters(function *ast.FuncDecl, params []mcpFieldSpec) bool {
	if function.Type.Params == nil || len(function.Type.Params.List) != len(params) {
		return false
	}
	for index := 0; index < len(params); index++ {
		field := function.Type.Params.List[index]
		if len(field.Names) != 1 || field.Names[0].Name != params[index].name ||
			s.typeName(field.Type) != params[index].typeName {
			return false
		}
	}
	return true
}

func (s *mcpGoScanner) validResult(function *ast.FuncDecl, result string) bool {
	if result == "" {
		return function.Type.Results == nil || len(function.Type.Results.List) == 0
	}
	return function.Type.Results != nil && len(function.Type.Results.List) == 1 &&
		len(function.Type.Results.List[0].Names) == 0 && s.typeName(function.Type.Results.List[0].Type) == result
}

func (s *mcpGoScanner) typeName(expression ast.Expr) string {
	prefix := ""
	for depth := 0; depth < maxMCPTypeDepth; depth++ {
		switch value := expression.(type) {
		case *ast.Ident, *ast.SelectorExpr:
			return prefix + s.callName(value)
		case *ast.StarExpr:
			prefix += "*"
			expression = value.X
		case *ast.Ellipsis:
			prefix += "..."
			expression = value.Elt
		case *ast.ParenExpr:
			expression = value.X
		default:
			return ""
		}
	}
	return ""
}

func (s *mcpGoScanner) validClassifiedResultHelper(function *ast.FuncDecl) bool {
	if !s.validSignature(function, []mcpFieldSpec{{"text", "string"}, {"_", "mcpTextClassification"}}, "*mcp.ToolResult") {
		return false
	}
	call, ok := singleReturnExpression(function).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || goIdentifier(call.Args[0]) != "text" {
		return false
	}
	want := "mcp.TextResult"
	if function.Name.Name == "mcpErrorResult" {
		want = "mcp.ErrorResult"
	}
	return s.callName(call.Fun) == want
}

func (s *mcpGoScanner) validComposedResultHelper(function *ast.FuncDecl) bool {
	if !s.validSignature(function, []mcpFieldSpec{{"text", "mcpGovernedText"}}, "*mcp.ToolResult") {
		return false
	}
	call, ok := singleReturnExpression(function).(*ast.CallExpr)
	if !ok || len(call.Args) != 1 || !stringConversionOf(call.Args[0], "text") {
		return false
	}
	want := "mcp.TextResult"
	if function.Name.Name == "mcpComposedErrorResult" {
		want = "mcp.ErrorResult"
	}
	return s.callName(call.Fun) == want
}

func stringConversionOf(expression ast.Expr, identifier string) bool {
	call, ok := expression.(*ast.CallExpr)
	return ok && goCallName(call.Fun) == "string" && len(call.Args) == 1 && goIdentifier(call.Args[0]) == identifier
}

func (s *mcpGoScanner) validMCPBuilderMethod(function *ast.FuncDecl) bool {
	if !mcpTextBuilderReceiver(function.Recv) {
		return false
	}
	switch function.Name.Name {
	case "Template":
		return s.validSignature(function, []mcpFieldSpec{{"template", "string"}, {"args", "...any"}}, "") &&
			validBuilderTemplateBody(function)
	case "External":
		return s.validSignature(function, []mcpFieldSpec{{"text", "string"}, {"_", "mcpTextClassification"}}, "") &&
			validBuilderWriteBody(function, "text", false)
	case "Append":
		return s.validSignature(function, []mcpFieldSpec{{"text", "mcpGovernedText"}}, "") &&
			validBuilderWriteBody(function, "text", true)
	case "Text":
		return s.validSignature(function, nil, "mcpGovernedText") && validBuilderTextBody(function)
	}
	return false
}

func mcpTextBuilderReceiver(fields *ast.FieldList) bool {
	if fields == nil || len(fields.List) != 1 {
		return false
	}
	field := fields.List[0]
	pointer, ok := field.Type.(*ast.StarExpr)
	return ok && len(field.Names) == 1 && field.Names[0].Name == "b" && goIdentifier(pointer.X) == "mcpTextBuilder"
}

func validBuilderTemplateBody(function *ast.FuncDecl) bool {
	call := singleExpressionCall(function)
	return call != nil && goCallName(call.Fun) == "fmt.Fprintf" && len(call.Args) == 3 &&
		builderValueAddress(call.Args[0]) && goIdentifier(call.Args[1]) == "template" &&
		goIdentifier(call.Args[2]) == "args" && call.Ellipsis.IsValid()
}

func validBuilderWriteBody(function *ast.FuncDecl, identifier string, convert bool) bool {
	call := singleExpressionCall(function)
	if call == nil || goCallName(call.Fun) != "b.value.WriteString" || len(call.Args) != 1 {
		return false
	}
	if convert {
		return stringConversionOf(call.Args[0], identifier)
	}
	return goIdentifier(call.Args[0]) == identifier
}

func validBuilderTextBody(function *ast.FuncDecl) bool {
	conversion, ok := singleReturnExpression(function).(*ast.CallExpr)
	if !ok || goCallName(conversion.Fun) != "mcpGovernedText" || len(conversion.Args) != 1 {
		return false
	}
	call, ok := conversion.Args[0].(*ast.CallExpr)
	return ok && goCallName(call.Fun) == "b.value.String" && len(call.Args) == 0
}

func builderValueAddress(expression ast.Expr) bool {
	address, ok := expression.(*ast.UnaryExpr)
	if !ok || address.Op != token.AND {
		return false
	}
	selector, ok := address.X.(*ast.SelectorExpr)
	return ok && goIdentifier(selector.X) == "b" && selector.Sel.Name == "value"
}

func singleExpressionCall(function *ast.FuncDecl) *ast.CallExpr {
	if function.Body == nil || len(function.Body.List) != 1 {
		return nil
	}
	statement, ok := function.Body.List[0].(*ast.ExprStmt)
	if !ok {
		return nil
	}
	call, ok := statement.X.(*ast.CallExpr)
	if !ok {
		return nil
	}
	return call
}

func singleReturnExpression(function *ast.FuncDecl) ast.Expr {
	if function.Body == nil || len(function.Body.List) != 1 {
		return nil
	}
	returned, ok := function.Body.List[0].(*ast.ReturnStmt)
	if !ok || len(returned.Results) != 1 {
		return nil
	}
	return returned.Results[0]
}

func validMCPTextfHelper(expression ast.Expr) bool {
	conversion, ok := expression.(*ast.CallExpr)
	if !ok || goCallName(conversion.Fun) != "mcpGovernedText" || len(conversion.Args) != 1 {
		return false
	}
	call, ok := conversion.Args[0].(*ast.CallExpr)
	return ok && goCallName(call.Fun) == "fmt.Sprintf" && len(call.Args) == 2 &&
		goIdentifier(call.Args[0]) == "template" && goIdentifier(call.Args[1]) == "args" && call.Ellipsis.IsValid()
}

func (s *mcpGoScanner) rejectOutputReferences(node ast.Node) {
	visitor := &mcpOutputReferenceVisitor{scanner: s, stack: make([]ast.Node, 0, maxGoSelectorDepth)}
	ast.Walk(visitor, node)
}

type mcpOutputReferenceVisitor struct {
	scanner *mcpGoScanner
	stack   []ast.Node
}

func (v *mcpOutputReferenceVisitor) Visit(node ast.Node) ast.Visitor {
	if node == nil {
		v.stack = v.stack[:len(v.stack)-1]
		return v
	}
	if v.scanner.firstErr != nil {
		return nil
	}
	if selector, ok := node.(*ast.SelectorExpr); ok {
		if v.rejectSelectorReference(selector) {
			return nil
		}
	}
	if identifier, ok := node.(*ast.Ident); ok && v.rejectIdentifierReference(identifier) {
		return nil
	}
	v.stack = append(v.stack, node)
	return v
}

func (v *mcpOutputReferenceVisitor) rejectSelectorReference(selector *ast.SelectorExpr) bool {
	return v.rejectNamedOutputReference(selector) ||
		v.rejectWriteStringReference(selector) ||
		v.rejectBuilderMethodReference(selector)
}

func (v *mcpOutputReferenceVisitor) rejectNamedOutputReference(selector *ast.SelectorExpr) bool {
	name := v.scanner.callName(selector)
	if !outputFunctionName(name) || directCallCallee(v.stack, selector) {
		return false
	}
	reason := "output function reference bypasses the output census"
	if name == "mcp.ErrorResult" || name == "mcp.TextResult" {
		reason = "mcp result function reference bypasses the output census"
	}
	v.scanner.fail(selector, reason)
	return true
}

func (v *mcpOutputReferenceVisitor) rejectWriteStringReference(selector *ast.SelectorExpr) bool {
	if selector.Sel.Name != "WriteString" {
		return false
	}
	if v.scanner.outputMethodExpression(selector) || !directCallCallee(v.stack, selector) {
		v.scanner.fail(selector, "output method function reference bypasses the output census")
		return true
	}
	return false
}

func (v *mcpOutputReferenceVisitor) rejectBuilderMethodReference(selector *ast.SelectorExpr) bool {
	if !builderReferenceMethod(selector.Sel.Name) {
		return false
	}
	typeReference := mcpBuilderTypeReference(selector.X)
	if !typeReference && !v.scanner.builderReference(selector.X) {
		return false
	}
	if !typeReference && directCallCallee(v.stack, selector) {
		return false
	}
	v.scanner.fail(selector, "mcpTextBuilder function reference bypasses the output census")
	return true
}

func (s *mcpGoScanner) outputMethodExpression(selector *ast.SelectorExpr) bool {
	typeName := s.typeName(selector.X)
	return typeName == "strings.Builder" || typeName == "*strings.Builder"
}

func (v *mcpOutputReferenceVisitor) rejectIdentifierReference(identifier *ast.Ident) bool {
	if !localOutputFunctionName(identifier.Name) || declaredIdentifier(v.parent(), identifier) ||
		directCallCallee(v.stack, identifier) {
		return false
	}
	v.scanner.fail(identifier, "mcp output function reference bypasses the output census")
	return true
}

func (v *mcpOutputReferenceVisitor) parent() ast.Node {
	if len(v.stack) == 0 {
		return nil
	}
	return v.stack[len(v.stack)-1]
}

func directCallCallee(stack []ast.Node, expression ast.Expr) bool {
	var child ast.Node = expression
	for depth, index := 0, len(stack)-1; index >= 0 && depth < maxGoSelectorDepth; depth, index = depth+1, index-1 {
		switch parent := stack[index].(type) {
		case *ast.ParenExpr:
			if parent.X != child {
				return false
			}
			child = parent
		case *ast.CallExpr:
			return parent.Fun == child
		default:
			return false
		}
	}
	return false
}

func outputFunctionName(name string) bool {
	switch name {
	case "mcp.ErrorResult", "mcp.TextResult", "fmt.Fprint", "fmt.Fprintf", "fmt.Fprintln",
		"io.WriteString", "http.Error":
		return true
	default:
		return false
	}
}

func localOutputFunctionName(name string) bool {
	switch name {
	case "mcpTextResult", "mcpErrorResult", "mcpComposedTextResult", "mcpComposedErrorResult",
		"mcpTextf", "mcpClassifiedText":
		return true
	default:
		return false
	}
}

func builderReferenceMethod(name string) bool {
	return name == "Template" || name == "External" || name == "Append" || name == "Text"
}

func mcpBuilderTypeReference(expression ast.Expr) bool {
	identifier, ok := unwrapGoReference(expression).(*ast.Ident)
	return ok && identifier.Name == "mcpTextBuilder"
}

func declaredIdentifier(parent ast.Node, identifier *ast.Ident) bool {
	switch declaration := parent.(type) {
	case *ast.FuncDecl:
		return declaration.Name == identifier
	case *ast.TypeSpec:
		return declaration.Name == identifier
	default:
		return false
	}
}

func goCallName(expression ast.Expr) string {
	parts := make([]string, 0, 4)
	for depth := 0; depth < maxGoSelectorDepth; depth++ {
		switch value := expression.(type) {
		case *ast.Ident:
			parts = append(parts, value.Name)
			slices.Reverse(parts)
			return strings.Join(parts, ".")
		case *ast.SelectorExpr:
			parts = append(parts, value.Sel.Name)
			expression = value.X
		case *ast.ParenExpr:
			expression = value.X
		default:
			return ""
		}
	}
	return ""
}

func goIdentifier(expression ast.Expr) string {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return ""
	}
	return identifier.Name
}

func goStaticTemplate(expression ast.Expr) (string, bool) {
	if text, ok := goStaticString(expression); ok {
		return text, true
	}
	call, ok := expression.(*ast.CallExpr)
	if !ok || goCallName(call.Fun) != "fmt.Sprintf" || len(call.Args) == 0 {
		return "", false
	}
	return goStaticString(call.Args[0])
}

func goStaticString(expression ast.Expr) (string, bool) {
	stack := []ast.Expr{expression}
	var text strings.Builder
	for parts := 0; len(stack) > 0 && parts < maxGoStaticParts; parts++ {
		last := len(stack) - 1
		value := stack[last]
		stack = stack[:last]
		switch node := value.(type) {
		case *ast.BasicLit:
			if node.Kind != token.STRING {
				return "", false
			}
			decoded, err := strconv.Unquote(node.Value)
			if err != nil {
				return "", false
			}
			text.WriteString(decoded)
		case *ast.ParenExpr:
			stack = append(stack, node.X)
		case *ast.BinaryExpr:
			if node.Op != token.ADD {
				return "", false
			}
			stack = append(stack, node.Y, node.X)
		default:
			return "", false
		}
		if len(stack) > maxGoStaticParts {
			return "", false
		}
	}
	return text.String(), len(stack) == 0
}

func goBuilderTarget(expression ast.Expr) bool {
	address, ok := expression.(*ast.UnaryExpr)
	if ok && address.Op == token.AND {
		expression = address.X
	}
	_, ok = expression.(*ast.Ident)
	return ok
}

func classifiedMCPExpression(expression ast.Expr) (ast.Expr, string, bool) {
	call, ok := expression.(*ast.CallExpr)
	if !ok || goCallName(call.Fun) != "mcpClassifiedText" || len(call.Args) != 2 {
		return nil, "", false
	}
	reason, ok := mcpTextClass(call.Args[1])
	return call.Args[0], reason, ok
}

func mcpTextClass(expression ast.Expr) (string, bool) {
	identifier, ok := expression.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch identifier.Name {
	case "mcpTextStructuredJSON":
		return "structured-json", true
	case "mcpTextUntrusted":
		return "untrusted-passthrough", true
	case "mcpTextProtocol":
		return "protocol", true
	}
	return "", false
}

func goNodeText(files *token.FileSet, node ast.Node) (string, error) {
	var output bytes.Buffer
	if err := format.Node(&output, files, node); err != nil {
		return "", err
	}
	return output.String(), nil
}
