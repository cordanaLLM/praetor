package hiss

import (
	"go/ast"
	"path"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// maxGoImports bounds the import specs read from one file (HISS-02).
const maxGoImports = 4096

// GoImports records the package names a Go file binds through its import declarations,
// so a rule can ask what an identifier resolves to instead of how it is spelled.
//
// `import u "unsafe"` binds u to unsafe, a dot import binds every exported name of the
// package into the file block, and a blank import binds nothing. A rule that matched the
// spelling "unsafe" was evaded by the alias and never saw the dot-imported call at all.
type GoImports struct {
	// byName maps a bound package name to its import path.
	byName map[string]string
	// dot holds the import paths imported with ".".
	dot map[string]struct{}
}

// FileImports reads the file's import specs once. Without type information the name of
// an unrenamed import is derived from its path by DefaultImportName. A spec whose path
// does not unquote comes from a partial AST and binds nothing.
func FileImports(file *ast.File) GoImports {
	im := GoImports{byName: make(map[string]string), dot: make(map[string]struct{})}
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
		name := DefaultImportName(importPath)
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

// DefaultImportName returns the name an unrenamed import binds, derived from its path:
// the last element, skipping a module major-version element (example.com/mod/v2 binds
// mod) and dropping a gopkg.in version suffix (gopkg.in/yaml.v3 binds yaml). It is exact
// for the standard library; for any other package the declared package clause decides,
// which only type information can read.
func DefaultImportName(importPath string) string {
	name := path.Base(importPath)
	if util.IsGoMajorVersionElement(name) && strings.Contains(importPath, "/") {
		name = path.Base(path.Dir(importPath))
	}
	if cut := strings.LastIndex(name, ".v"); cut > 0 && util.IsGoMajorVersionElement(name[cut+1:]) {
		name = name[:cut]
	}
	return name
}

// Path returns the import path name is bound to, and whether it is bound at all.
func (im GoImports) Path(name string) (string, bool) {
	bound, ok := im.byName[name]
	return bound, ok
}

// Binds reports whether name is the file's package name for importPath.
func (im GoImports) Binds(name, importPath string) bool {
	bound, ok := im.byName[name]
	return ok && bound == importPath
}

// DotImports reports whether importPath is imported with ".", which puts its exported
// names into the file block as bare identifiers.
func (im GoImports) DotImports(importPath string) bool {
	_, ok := im.dot[importPath]
	return ok
}
