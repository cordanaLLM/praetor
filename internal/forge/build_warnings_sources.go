package forge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"io/fs"
	"os"
	slashpath "path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/util"
)

// Bounds of the source walk (HISS-02).
const (
	// maxSourceEntries bounds the directory entries one walk visits.
	maxSourceEntries = 200000
	// maxGoHeaderBytes bounds what is read of one Go file: its package clause and imports come
	// first, and parser.ImportsOnly stops after them.
	maxGoHeaderBytes = 1 << 20
)

// cxxUnitSuffixes are the file suffixes, lower-cased, of the C++ translation units gcc and clang
// compile and cgo hands the C++ compiler (".cc, .cpp, or .cxx", go doc cmd/cgo).
var cxxUnitSuffixes = map[string]bool{".cc": true, ".cpp": true, ".cxx": true, ".c++": true}

// nativeSources is what the repository's own files say about the native code its lanes compile:
// a C translation unit (c), a C++ one (cxx), a Go file the go command builds that imports "C"
// (cgo), and a package directory holding both a cgo file and C++ files (cgoCXX).
type nativeSources struct {
	c, cxx, cgo, cgoCXX bool
}

// readNativeSources walks the repository at root. Only the files git reports as the repository's
// own count (hiss.GitVisiblePaths, the set the audit's HISS scan reads), so build output and
// other ignored trees are not entered; outside a work tree every file counts. .git and the trees
// a toolchain writes (util.IsToolchainTreeDir) are skipped. A Go file counts as the go command
// builds it: not a test file, not under testdata, and no path element starting with "." or "_"
// ('go help packages').
func readNativeSources(ctx context.Context, root string) (nativeSources, error) {
	walk := sourceWalk{
		ctx: ctx, root: filepath.Clean(root), visible: hiss.GitVisiblePaths(ctx, root), fset: token.NewFileSet(),
		cgoDirs: map[string]bool{}, cxxDirs: map[string]bool{},
	}
	if err := filepath.WalkDir(walk.root, walk.visit); err != nil {
		return nativeSources{}, fmt.Errorf("read the repository's sources: %w", err)
	}
	walk.found.cgo = len(walk.cgoDirs) > 0
	for dir := range walk.cgoDirs {
		walk.found.cgoCXX = walk.found.cgoCXX || walk.cxxDirs[dir]
	}
	return walk.found, nil
}

// sourceWalk is one readNativeSources walk: what it found so far, and the directories holding a
// cgo file or a C++ file.
type sourceWalk struct {
	ctx              context.Context
	root             string
	visible          *hiss.GitVisibleTree
	fset             *token.FileSet
	visited          int
	found            nativeSources
	cgoDirs, cxxDirs map[string]bool
}

// visit reads one entry of the walk.
func (w *sourceWalk) visit(path string, entry fs.DirEntry, err error) error {
	if err != nil {
		return err
	}
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.visited++; w.visited > maxSourceEntries {
		return fmt.Errorf("the repository holds more than %d entries", maxSourceEntries)
	}
	rel, err := filepath.Rel(w.root, path)
	if err != nil {
		return err
	}
	rel = filepath.ToSlash(rel)
	switch {
	case entry.IsDir() && w.skips(entry.Name(), rel):
		return filepath.SkipDir
	case entry.Type().IsRegular() && w.visible.HasFile(rel):
		return w.file(path, rel)
	}
	return nil
}

// skips reports whether the walk skips the directory name at rel: .git, a toolchain's tree, or
// one holding nothing git reports as the repository's own. The root is always entered.
func (w *sourceWalk) skips(name, rel string) bool {
	return rel != "." && (name == ".git" || util.IsToolchainTreeDir(name) || !w.visible.HasDir(rel))
}

// file records what one regular file of the repository is.
func (w *sourceWalk) file(path, rel string) error {
	dir := slashpath.Dir(rel)
	switch ext := strings.ToLower(slashpath.Ext(rel)); {
	case ext == ".c":
		w.found.c = true
	case cxxUnitSuffixes[ext]:
		w.found.cxx = true
		w.cxxDirs[dir] = true
	case strings.HasSuffix(rel, ".go") && goBuildsFile(rel):
		cgo, err := importsC(w.fset, path)
		if cgo {
			w.cgoDirs[dir] = true
		}
		return err
	}
	return nil
}

// goBuildsFile reports whether the go command builds the Go file at rel: util.IsGoNonTestSource,
// and no element of rel starting with "." or "_", which ./... patterns and the go command skip.
func goBuildsFile(rel string) bool {
	if !util.IsGoNonTestSource(rel) {
		return false
	}
	return !slices.ContainsFunc(strings.Split(rel, "/"), func(element string) bool {
		return strings.HasPrefix(element, ".") || strings.HasPrefix(element, "_")
	})
}

// importsC reports whether the Go file at path imports "C", reading its first maxGoHeaderBytes.
// A file whose imports do not parse is no cgo file: the go command fails on it anyway.
func importsC(fset *token.FileSet, path string) (cgo bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	header, err := io.ReadAll(io.LimitReader(file, maxGoHeaderBytes))
	if err != nil {
		return false, fmt.Errorf("read %s: %w", path, err)
	}
	if !bytes.Contains(header, []byte(`"C"`)) {
		return false, nil
	}
	node, parseErr := parser.ParseFile(fset, path, header, parser.ImportsOnly)
	return parseErr == nil && slices.Contains(util.GoImportPaths(node), "C"), nil
}
