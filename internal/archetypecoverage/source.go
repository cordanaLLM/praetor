// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package archetypecoverage

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/build"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds on one source scan (HISS-02).
const (
	maxWalkEntries  = 1 << 17
	maxPackages     = 2048
	maxSourceBytes  = 4 << 20
	maxTypeErrors   = 8
	maxStructFields = 1024
)

// sourceIndex is what one scan of the module learned: every top-level declaration, every
// struct field of the schema package, and which declarations read which of those fields.
type sourceIndex struct {
	// decls holds every top-level declaration as "<dir>.<Name>" or "<dir>.<Type>.<Method>".
	decls map[string]bool
	// fields maps every schema struct field "Type.Field" to whether it holds a struct value.
	fields map[string]bool
	// types maps every schema struct type to its field names, in declaration order.
	types map[string][]string
	// readers maps a schema field to the declarations that read it. A selector that is only
	// assigned to is a write, not a read.
	readers map[string]map[string]bool
}

// sourcePackage is one parsed package directory of the module.
type sourcePackage struct {
	dir     string
	files   []*ast.File
	imports []string
}

// scanModule type-checks every buildable non-test package of the module at root and indexes
// the reads of schemaDir's struct fields. File selection follows the host's build context
// (go/build.Default), as go build does; the schema readers this check exists for carry no
// build constraint, so every host reaches the same index.
func scanModule(ctx context.Context, root, schemaDir string) (*sourceIndex, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve %s: %w", root, err)
	}
	module, err := gomanifest.ReadModulePath(abs)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	packages, err := parseModule(ctx, abs, module, fset)
	if err != nil {
		return nil, err
	}
	if packages[schemaDir] == nil {
		return nil, fmt.Errorf("schema package %s has no buildable Go source", schemaDir)
	}
	order, err := dependencyOrder(packages)
	if err != nil {
		return nil, err
	}
	return checkModule(ctx, module, schemaDir, fset, packages, order)
}

// parseModule parses every package directory go build would compile, skipping what it skips:
// directories starting with "." or "_", testdata, vendor, node_modules and nested modules.
func parseModule(ctx context.Context, root, module string, fset *token.FileSet) (map[string]*sourcePackage, error) {
	packages := map[string]*sourcePackage{}
	entries := 0
	walkErr := filepath.WalkDir(root, func(file string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entries++; entries > maxWalkEntries {
			return fmt.Errorf("module holds more than %d entries", maxWalkEntries)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return skipDirectory(root, file, entry.Name())
		}
		return addSourceFile(root, module, file, fset, packages)
	})
	if walkErr != nil {
		return nil, fmt.Errorf("read module source: %w", walkErr)
	}
	if len(packages) > maxPackages {
		return nil, fmt.Errorf("module holds more than %d packages", maxPackages)
	}
	return packages, nil
}

// skipDirectory returns fs.SkipDir for a directory go build does not descend into.
func skipDirectory(root, dir, name string) error {
	if dir == root {
		return nil
	}
	if strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") ||
		name == "testdata" || name == "vendor" || name == "node_modules" {
		return fs.SkipDir
	}
	if _, err := os.Lstat(filepath.Join(dir, "go.mod")); err == nil {
		return fs.SkipDir // A nested module is not part of this one.
	}
	return nil
}

// addSourceFile parses one buildable non-test Go file into its package.
func addSourceFile(root, module, file string, fset *token.FileSet, packages map[string]*sourcePackage) error {
	rel, err := filepath.Rel(root, file)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	if !util.IsGoNonTestSource(rel) {
		return nil
	}
	dir, name := filepath.Split(file)
	if match, matchErr := build.Default.MatchFile(dir, name); matchErr != nil || !match {
		return matchErr
	}
	src, err := util.ReadConfinedLimited(root, filepath.FromSlash(rel), maxSourceBytes)
	if err != nil {
		return err
	}
	parsed, err := parser.ParseFile(fset, file, src, parser.SkipObjectResolution)
	if err != nil {
		return err
	}
	pkgDir := path.Dir(rel)
	pkg := packages[pkgDir]
	if pkg == nil {
		pkg = &sourcePackage{dir: pkgDir}
		packages[pkgDir] = pkg
	}
	pkg.add(parsed, module)
	return nil
}

// add files one parsed file and the module packages it imports.
func (p *sourcePackage) add(parsed *ast.File, module string) {
	p.files = append(p.files, parsed)
	for _, spec := range util.GoImportSpecs(parsed) {
		if imported, inside := util.ModuleImportDir(spec.Path, module); inside && !slices.Contains(p.imports, imported) {
			p.imports = append(p.imports, imported)
		}
	}
}

// dependencyOrder sorts the packages so each follows every module package it imports (Kahn's
// algorithm, iterative). Ties break by directory, so the order is the same on every run.
func dependencyOrder(packages map[string]*sourcePackage) ([]string, error) {
	pending, dependents := importEdges(packages)
	var ready []string
	for dir := range packages {
		if pending[dir] == 0 {
			ready = append(ready, dir)
		}
	}
	slices.Sort(ready)
	order := make([]string, 0, len(packages))
	for i := 0; i < len(ready) && i < len(packages); i++ {
		order = append(order, ready[i])
		next := dependents[ready[i]]
		slices.Sort(next)
		for _, dir := range next {
			if pending[dir]--; pending[dir] == 0 {
				ready = append(ready, dir)
			}
		}
	}
	if len(order) != len(packages) {
		return nil, errors.New("module packages import each other in a cycle")
	}
	return order, nil
}

// importEdges counts, per package, the module packages it imports, and lists, per package,
// the packages that import it.
func importEdges(packages map[string]*sourcePackage) (map[string]int, map[string][]string) {
	pending := make(map[string]int, len(packages))
	dependents := map[string][]string{}
	for dir, pkg := range packages {
		for _, imported := range pkg.imports {
			if packages[imported] != nil {
				pending[dir]++
				dependents[imported] = append(dependents[imported], dir)
			}
		}
	}
	return pending, dependents
}

// moduleImporter resolves a module package to the one this scan already checked, and every
// other import through the Go source importer.
type moduleImporter struct {
	module   string
	checked  map[string]*types.Package
	fallback types.ImporterFrom
}

func (m *moduleImporter) Import(importPath string) (*types.Package, error) {
	return m.ImportFrom(importPath, "", 0)
}

func (m *moduleImporter) ImportFrom(importPath, dir string, mode types.ImportMode) (*types.Package, error) {
	if pkg := m.checked[importPath]; pkg != nil {
		return pkg, nil
	}
	if _, inside := util.ModuleImportDir(importPath, m.module); inside {
		return nil, fmt.Errorf("module package %s has no buildable Go source", importPath)
	}
	return m.fallback.ImportFrom(importPath, dir, mode)
}

// checkModule type-checks the packages in dependency order and indexes the schema reads. A type
// error fails the scan: an incomplete check could miss a read and report a key unconsumed.
func checkModule(ctx context.Context, module, schemaDir string, fset *token.FileSet,
	packages map[string]*sourcePackage, order []string) (*sourceIndex, error) {
	fallback, ok := importer.ForCompiler(fset, "source", nil).(types.ImporterFrom)
	if !ok {
		return nil, errors.New("the Go source importer does not resolve by directory")
	}
	imports := &moduleImporter{module: module, checked: map[string]*types.Package{}, fallback: fallback}
	index := &sourceIndex{decls: map[string]bool{}, fields: map[string]bool{},
		types: map[string][]string{}, readers: map[string]map[string]bool{}}
	var schemaFields map[*types.Var]string
	for _, dir := range order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkg := packages[dir]
		info := &types.Info{Selections: map[*ast.SelectorExpr]*types.Selection{}}
		checked, err := checkPackage(imports, fset, module, pkg, info)
		if err != nil {
			return nil, err
		}
		imports.checked[checked.Path()] = checked
		if dir == schemaDir {
			schemaFields = index.recordSchema(checked)
		}
		index.recordPackage(pkg, info, schemaFields)
	}
	return index, nil
}

// checkPackage type-checks one package and fails on its first type errors.
func checkPackage(imports *moduleImporter, fset *token.FileSet, module string, pkg *sourcePackage, info *types.Info) (*types.Package, error) {
	var typeErrors []error
	conf := types.Config{Importer: imports, Error: func(err error) {
		if len(typeErrors) < maxTypeErrors {
			typeErrors = append(typeErrors, err)
		}
	}}
	importPath := module
	if pkg.dir != "." {
		importPath = module + "/" + pkg.dir
	}
	checked, err := conf.Check(importPath, fset, pkg.files, info)
	if len(typeErrors) > 0 {
		return nil, fmt.Errorf("type-check %s: %w", pkg.dir, errors.Join(typeErrors...))
	}
	if err != nil {
		return nil, fmt.Errorf("type-check %s: %w", pkg.dir, err)
	}
	return checked, nil
}

// recordSchema indexes every field of every struct type the schema package declares and
// returns the lookup from a field's object to its "Type.Field" name.
func (s *sourceIndex) recordSchema(pkg *types.Package) map[*types.Var]string {
	lookup := map[*types.Var]string{}
	scope := pkg.Scope()
	for _, name := range scope.Names() {
		typeName, ok := scope.Lookup(name).(*types.TypeName)
		if !ok {
			continue
		}
		structure, ok := typeName.Type().Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := 0; i < structure.NumFields() && i < maxStructFields; i++ {
			field := structure.Field(i)
			id := name + "." + field.Name()
			_, nested := field.Type().Underlying().(*types.Struct)
			s.fields[id] = nested
			s.types[name] = append(s.types[name], field.Name())
			lookup[field] = id
		}
	}
	return lookup
}

// recordPackage indexes one package's declarations and the schema fields each one reads.
func (s *sourceIndex) recordPackage(pkg *sourcePackage, info *types.Info, schemaFields map[*types.Var]string) {
	for _, file := range pkg.files {
		for _, decl := range file.Decls {
			for _, id := range declarationIDs(pkg.dir, decl) {
				s.decls[id] = true
			}
			ids := declarationIDs(pkg.dir, decl)
			if len(ids) == 0 || len(schemaFields) == 0 {
				continue
			}
			s.recordReads(ids[0], decl, info, schemaFields)
		}
	}
}

// recordReads files every schema field decl selects, except a selector only assigned to.
func (s *sourceIndex) recordReads(id string, decl ast.Decl, info *types.Info, schemaFields map[*types.Var]string) {
	written := assignedSelectors(decl)
	ast.Inspect(decl, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok || written[selector] {
			return true
		}
		if name, known := selectedField(info, selector, schemaFields); known {
			if s.readers[name] == nil {
				s.readers[name] = map[string]bool{}
			}
			s.readers[name][id] = true
		}
		return true
	})
}

// assignedSelectors returns every selector decl assigns to directly.
func assignedSelectors(decl ast.Decl) map[*ast.SelectorExpr]bool {
	written := map[*ast.SelectorExpr]bool{}
	ast.Inspect(decl, func(node ast.Node) bool {
		assign, ok := node.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for _, target := range assign.Lhs {
			if selector, ok := target.(*ast.SelectorExpr); ok {
				written[selector] = true
			}
		}
		return true
	})
	return written
}

// selectedField returns the schema field name a selector reads, when it selects one.
func selectedField(info *types.Info, selector *ast.SelectorExpr, schemaFields map[*types.Var]string) (string, bool) {
	selection := info.Selections[selector]
	if selection == nil || selection.Kind() != types.FieldVal {
		return "", false
	}
	field, ok := selection.Obj().(*types.Var)
	if !ok {
		return "", false
	}
	name, known := schemaFields[field.Origin()]
	return name, known
}

// declarationIDs names a top-level declaration as the documentation does: "<dir>.<Func>",
// "<dir>.<Type>.<Method>", or "<dir>.<Name>" for each name a var, const or type declares.
func declarationIDs(dir string, decl ast.Decl) []string {
	switch typed := decl.(type) {
	case *ast.FuncDecl:
		if typed.Recv == nil || len(typed.Recv.List) == 0 {
			return []string{dir + "." + typed.Name.Name}
		}
		return []string{dir + "." + receiverName(typed.Recv.List[0].Type) + "." + typed.Name.Name}
	case *ast.GenDecl:
		var ids []string
		for _, spec := range typed.Specs {
			switch named := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range named.Names {
					ids = append(ids, dir+"."+name.Name)
				}
			case *ast.TypeSpec:
				ids = append(ids, dir+"."+named.Name.Name)
			}
		}
		return ids
	}
	return nil
}

// receiverName strips the pointer and type parameters from a method receiver's type.
func receiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}
	switch typed := expr.(type) {
	case *ast.IndexExpr:
		expr = typed.X
	case *ast.IndexListExpr:
		expr = typed.X
	}
	if ident, ok := expr.(*ast.Ident); ok {
		return ident.Name
	}
	return "?"
}
