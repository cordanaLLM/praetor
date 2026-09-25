package hiss

import (
	"go/ast"
	"path"
	"strconv"
)

// maxGoImports bounds the import specs read from one file (HISS-02).
const maxGoImports = 4096

// goImports records the package names a Go file binds through its import declarations,
// so a rule can ask what an identifier resolves to instead of how it is spelled.
//
// `import u "unsafe"` binds u to unsafe, a dot import binds every exported name of the
// package into the file block, and a blank import binds nothing. A rule that matched the
// spelling "unsafe" was evaded by the alias and never saw the dot-imported call at all.
type goImports struct {
	// byName maps a bound package name to its import path.
	byName map[string]string
	// dot holds the import paths imported with ".".
	dot map[string]struct{}
}

// fileImports reads the file's import specs once per scan. Without type information the
// name of an unrenamed import is its last path element, which is exact for the standard
// library packages the rules resolve (unsafe, context, os/exec, net/http). A spec whose
// path does not unquote comes from a partial AST and binds nothing.
func fileImports(file *ast.File) goImports {
	im := goImports{byName: make(map[string]string), dot: make(map[string]struct{})}
	if file == nil {
		return im
	}
	for i := 0; i < len(file.Imports) && i < maxGoImports; i++ {
		spec := file.Imports[i]
		if spec == nil || spec.Path == nil {
			continue
		}
		importPath, err := strconv.Unquote(spec.Path.Value)
		if err != nil || importPath == "" {
			continue
		}
		name := path.Base(importPath)
		if spec.Name != nil {
			name = spec.Name.Name
		}
		switch name {
		case "_":
		case ".":
			im.dot[importPath] = struct{}{}
		default:
			im.byName[name] = importPath
		}
	}
	return im
}

// binds reports whether name is the file's package name for importPath.
func (im goImports) binds(name, importPath string) bool {
	bound, ok := im.byName[name]
	return ok && bound == importPath
}

// dotImports reports whether importPath is imported with ".", which puts its exported
// names into the file block as bare identifiers.
func (im goImports) dotImports(importPath string) bool {
	_, ok := im.dot[importPath]
	return ok
}
