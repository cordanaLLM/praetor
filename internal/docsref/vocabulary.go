// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path"
	"slices"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds on the source read to build a vocabulary (HISS-02).
const (
	maxSourceBytes   = 4 << 20
	maxGoModBytes    = 1 << 20
	maxClosureNodes  = 1 << 17
	maxClosurePops   = 1 << 23
	maxClosureRounds = 64
)

// methodKey prefixes the merged declaration of every method with one name in a package. A
// call through a value (settings.Serve()) cannot be tied to its type without type checking,
// so the closure takes every method of that name in the packages it reached.
const methodKey = "."

// flagMethods maps each flag-registration method of Go's flag package to the index of its
// name argument and whether the flag takes a value. Only a call whose name argument is a
// string literal counts, which keeps same-named methods of other types (String(), Int64())
// out: they take no string argument.
var flagMethods = map[string]struct {
	nameArg    int
	takesValue bool
}{
	"String": {0, true}, "Bool": {0, false}, "Int": {0, true}, "Int64": {0, true},
	"Uint": {0, true}, "Uint64": {0, true}, "Float64": {0, true}, "Duration": {0, true},
	"Func": {0, true}, "BoolFunc": {0, false},
	"StringVar": {1, true}, "BoolVar": {1, false}, "IntVar": {1, true}, "Int64Var": {1, true},
	"UintVar": {1, true}, "Uint64Var": {1, true}, "Float64Var": {1, true},
	"DurationVar": {1, true}, "Var": {1, true}, "TextVar": {1, true},
}

// reference names one top-level declaration: a package directory and a declaration name.
type reference struct{ dir, name string }

// flagDefinition is one registered flag.
type flagDefinition struct {
	name       string
	takesValue bool
}

// declaration is what one top-level declaration contributes: its string literals (for the
// engine-literal path rule), the flags it registers or compares by hand, what it references,
// and, for a function, how it dispatches on its argument list.
type declaration struct {
	literals   []string
	compared   []string
	flags      []flagDefinition
	refs       []reference
	methods    []string
	function   bool
	dispatch   *dispatchSites
	mapEntries []childEntry
}

// sourceTree reads the module's Go packages on demand.
type sourceTree struct {
	root     string
	module   string
	files    map[string][]string
	packages map[string]map[string]*declaration
	names    map[string]string
	fset     *token.FileSet
}

// newSourceTree indexes the non-test Go sources of inventory by package directory.
func newSourceTree(root string, inventory []string) (*sourceTree, error) {
	module, err := modulePath(root)
	if err != nil {
		return nil, err
	}
	tree := &sourceTree{root: root, module: module, files: map[string][]string{},
		packages: map[string]map[string]*declaration{}, names: map[string]string{}, fset: token.NewFileSet()}
	for _, rel := range inventory {
		if util.IsGoNonTestSource(rel) {
			dir := path.Dir(rel)
			tree.files[dir] = append(tree.files[dir], rel)
		}
	}
	return tree, nil
}

// modulePath reads the module directive of the repository's go.mod.
func modulePath(root string) (string, error) {
	data, err := util.ReadConfinedLimited(root, "go.mod", maxGoModBytes)
	if err != nil {
		return "", fmt.Errorf("read go.mod: %w", err)
	}
	if module, ok := gomanifest.ModuleDirective(data); ok {
		return module, nil
	}
	return "", errors.New("go.mod declares no module path")
}

// flagTable maps a flag name to whether it takes a value (false only for a boolean flag).
type flagTable map[string]bool

// add records a flag; a name registered both as a boolean and with a value takes one.
func (f flagTable) add(name string, takesValue bool) {
	f[name] = f[name] || takesValue
}

// flags returns the flags the code reachable from one function of package dir defines: every
// registration through Go's flag package with a literal name, and every flag-shaped literal
// the code compares an argument against by hand ("--json" in a case clause). Other literals
// never count, so a git argument such as "--porcelain" is not mistaken for a CLI flag. The
// walk does not enter the functions in stops: a subcommand's siblings keep their flags.
func (t *sourceTree) flags(ctx context.Context, dir, function string, stops map[string]bool) (flagTable, error) {
	decls, err := t.load(ctx, dir)
	if err != nil {
		return nil, err
	}
	if decls[function] == nil || !decls[function].function {
		return nil, fmt.Errorf("%s declares no function %s", dir, function)
	}
	walk := &closureWalk{tree: t, flags: flagTable{}, visited: map[reference]bool{}, stopDir: dir, stops: stops,
		methods: map[string]bool{}, queue: []reference{{dir, function}}}
	for round := 0; round < maxClosureRounds && len(walk.queue) > 0; round++ {
		if err := walk.drain(ctx); err != nil {
			return nil, err
		}
		walk.queueMethods()
	}
	if len(walk.queue) > 0 {
		return nil, fmt.Errorf("flags of %s.%s did not settle within %d rounds", dir, function, maxClosureRounds)
	}
	return walk.flags, nil
}

// closureWalk is one breadth-first walk over the declarations a function reaches.
type closureWalk struct {
	tree    *sourceTree
	flags   flagTable
	visited map[reference]bool
	stopDir string
	stops   map[string]bool
	methods map[string]bool
	queue   []reference
}

// drain visits queued declarations until none is left. A queued reference may repeat one
// already visited, so the walk is bounded by queue pops as well as by distinct declarations.
func (w *closureWalk) drain(ctx context.Context) error {
	for pops := 0; len(w.queue) > 0; pops++ {
		if len(w.visited) >= maxClosureNodes || pops >= maxClosurePops {
			return fmt.Errorf("vocabulary walk exceeds %d declarations or %d queued references", maxClosureNodes, maxClosurePops)
		}
		next := w.queue[0]
		w.queue = w.queue[1:]
		if w.visited[next] || (next.dir == w.stopDir && w.stops[next.name]) {
			continue
		}
		decls, err := w.tree.load(ctx, next.dir)
		if err != nil {
			return err
		}
		decl := decls[next.name]
		if decl == nil {
			continue
		}
		w.visited[next] = true
		w.absorb(next.dir, decl)
	}
	return nil
}

// absorb adds one declaration's flags and queues what it references.
func (w *closureWalk) absorb(dir string, decl *declaration) {
	for _, literal := range decl.compared {
		if match := flagPattern.FindStringSubmatch(literal); match != nil && match[2] == "" {
			w.flags.add(match[1], true)
		}
	}
	for _, flag := range decl.flags {
		w.flags.add(flag.name, flag.takesValue)
	}
	for _, ref := range decl.refs {
		if ref.dir == "" {
			ref.dir = dir
		}
		if !w.visited[ref] {
			w.queue = append(w.queue, ref)
		}
	}
	for _, method := range decl.methods {
		w.methods[method] = true
	}
}

// queueMethods queues every not-yet-visited method, in every package reached so far, whose
// name the reached code calls through a value.
func (w *closureWalk) queueMethods() {
	dirs := map[string]bool{}
	for ref := range w.visited {
		dirs[ref.dir] = true
	}
	for _, dir := range slices.Sorted(maps.Keys(dirs)) {
		decls := w.tree.packages[dir]
		for _, method := range slices.Sorted(maps.Keys(w.methods)) {
			ref := reference{dir, methodKey + method}
			if decls[ref.name] != nil && !w.visited[ref] {
				w.queue = append(w.queue, ref)
			}
		}
	}
}

// load parses one package directory once and returns its declarations by name.
func (t *sourceTree) load(ctx context.Context, dir string) (map[string]*declaration, error) {
	if decls, ok := t.packages[dir]; ok {
		return decls, nil
	}
	decls := map[string]*declaration{}
	t.packages[dir] = decls
	for _, rel := range t.files[dir] {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		file, err := t.parse(rel, 0)
		if errors.Is(err, fs.ErrNotExist) {
			continue // A tracked deletion is absent from the tree being checked.
		}
		if err != nil {
			return nil, err
		}
		t.collectFile(ctx, file, decls)
	}
	return decls, nil
}

// engineLiterals returns every string literal in the module's non-test Go source.
//
// A path the engine's own code names is part of what the engine does even when this
// repository does not carry the file: a file adoption writes into an adopted repository
// (docs/adr/0000-template.md, .agents/agents/repo-gatekeeper.md), or a protocol method
// shaped like a path (the MCP method tools/list). A guide that names one describes the
// engine, not a missing file. Test sources are excluded because a fixture path is not a
// contract, and so are paths the code only assembles at run time: those need a reference the
// code spells out.
func (t *sourceTree) engineLiterals(ctx context.Context) (map[string]bool, error) {
	literals := map[string]bool{}
	for _, dir := range slices.Sorted(maps.Keys(t.files)) {
		decls, err := t.load(ctx, dir)
		if err != nil {
			return nil, err
		}
		for _, decl := range decls {
			for _, literal := range decl.literals {
				literals[literal] = true
			}
		}
	}
	return literals, nil
}

// parse reads and parses one repository file.
func (t *sourceTree) parse(rel string, mode parser.Mode) (*ast.File, error) {
	src, err := util.ReadConfinedLimited(t.root, rel, maxSourceBytes)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}
	file, err := parser.ParseFile(t.fset, rel, src, mode|parser.SkipObjectResolution)
	if err != nil {
		return nil, fmt.Errorf("parse %s: %w", rel, err)
	}
	return file, nil
}

// packageName returns the package clause of a module directory, which is the name an import
// without an alias binds.
func (t *sourceTree) packageName(dir string) string {
	if name, ok := t.names[dir]; ok {
		return name
	}
	name := path.Base(dir)
	if files := t.files[dir]; len(files) > 0 {
		if file, err := t.parse(files[0], parser.PackageClauseOnly); err == nil {
			name = file.Name.Name
		}
	}
	t.names[dir] = name
	return name
}

// imports maps each local name a file binds to a module package directory. Only packages
// below the module root are tracked; the specs and the module boundary are read through
// util.GoImportSpecs and util.ModuleImportDir.
func (t *sourceTree) imports(file *ast.File) map[string]string {
	bound := map[string]string{}
	for _, spec := range util.GoImportSpecs(file) {
		dir, inside := util.ModuleImportDir(spec.Path, t.module)
		if !inside || dir == "." {
			continue
		}
		name := spec.Name
		if name == "" {
			name = t.packageName(dir)
		}
		bound[name] = dir
	}
	return bound
}

// collectFile records every top-level declaration of one file.
func (t *sourceTree) collectFile(ctx context.Context, file *ast.File, decls map[string]*declaration) {
	imports := t.imports(file)
	for _, decl := range file.Decls {
		if ctx.Err() != nil {
			return
		}
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			info := collect(typed, imports)
			key := typed.Name.Name
			if typed.Recv != nil {
				key = methodKey + key
			} else {
				info.function, info.dispatch = true, dispatchOf(typed)
			}
			merge(decls, key, info)
		case *ast.GenDecl:
			collectSpecs(typed, imports, decls)
		}
	}
}

// collectSpecs records the package-level types, variables and constants of one declaration
// group. A type contributes only its name, so a documented pkg.Type resolves; its methods are
// recorded with the functions.
func collectSpecs(group *ast.GenDecl, imports map[string]string, decls map[string]*declaration) {
	for _, spec := range group.Specs {
		switch typed := spec.(type) {
		case *ast.TypeSpec:
			merge(decls, typed.Name.Name, &declaration{})
		case *ast.ValueSpec:
			info := collect(typed, imports)
			if len(typed.Values) == 1 {
				if lit, ok := typed.Values[0].(*ast.CompositeLit); ok {
					info.mapEntries = mapLiteralEntries(lit)
				}
			}
			for _, name := range typed.Names {
				merge(decls, name.Name, info)
			}
		}
	}
}

// merge adds info to the declaration stored under key.
func merge(decls map[string]*declaration, key string, info *declaration) {
	existing := decls[key]
	if existing == nil {
		decls[key] = info
		return
	}
	existing.literals = append(existing.literals, info.literals...)
	existing.compared = append(existing.compared, info.compared...)
	existing.flags = append(existing.flags, info.flags...)
	existing.refs = append(existing.refs, info.refs...)
	existing.methods = append(existing.methods, info.methods...)
}

// collect gathers the literals, flag registrations and references inside one node.
func collect(node ast.Node, imports map[string]string) *declaration {
	info := &declaration{}
	selectors := map[*ast.Ident]bool{}
	ast.Inspect(node, func(n ast.Node) bool {
		switch typed := n.(type) {
		case *ast.BasicLit:
			if value, ok := stringLiteral(typed); ok {
				info.literals = append(info.literals, value)
			}
		case *ast.CallExpr:
			if flag, ok := flagDefinitionOf(typed); ok {
				info.flags = append(info.flags, flag)
			}
		case *ast.BinaryExpr:
			info.comparison(typed.Op, typed.X, typed.Y)
		case *ast.CaseClause:
			info.comparison(token.EQL, typed.List...)
		case *ast.SelectorExpr:
			return info.selector(typed, imports, selectors)
		case *ast.Ident:
			if !selectors[typed] {
				info.refs = append(info.refs, reference{name: typed.Name})
			}
		}
		return true
	})
	return info
}

// comparison records the string literals an == or != comparison, or a case clause, sets an
// argument against.
func (info *declaration) comparison(op token.Token, operands ...ast.Expr) {
	if op != token.EQL && op != token.NEQ {
		return
	}
	for _, operand := range operands {
		if value, ok := stringLiteral(operand); ok {
			info.compared = append(info.compared, value)
		}
	}
}

// selector records pkg.Name as a cross-package reference and value.Name as a method call,
// and reports whether the walk should descend into the expression.
func (info *declaration) selector(expr *ast.SelectorExpr, imports map[string]string, selectors map[*ast.Ident]bool) bool {
	if ident, ok := expr.X.(*ast.Ident); ok {
		if dir, imported := imports[ident.Name]; imported {
			info.refs = append(info.refs, reference{dir: dir, name: expr.Sel.Name})
			return false
		}
	}
	selectors[expr.Sel] = true
	info.methods = append(info.methods, expr.Sel.Name)
	return true
}

// flagDefinitionOf reports the flag a call registers, if it is a flag registration with a
// literal name.
func flagDefinitionOf(call *ast.CallExpr) (flagDefinition, bool) {
	selector, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return flagDefinition{}, false
	}
	method, known := flagMethods[selector.Sel.Name]
	if !known || len(call.Args) <= method.nameArg {
		return flagDefinition{}, false
	}
	literal, ok := call.Args[method.nameArg].(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return flagDefinition{}, false
	}
	name, err := strconv.Unquote(literal.Value)
	if err != nil || name == "" {
		return flagDefinition{}, false
	}
	return flagDefinition{name: name, takesValue: method.takesValue}, true
}
