// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	slashpath "path"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/gomanifest"
	"github.com/cordanaLLM/praetor/internal/semver"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The package pass resolves an ioProof against the Go files the scan read and the go.mod that
// owns them. Everything is loaded on demand, one package directory at a time, and only for the
// proofs a scan recorded.
//
// A name the index cannot pin to one declaration resolves to nothing, so its finding stays: a
// function or struct type declared twice in a package (two files for different build tags), a
// package directory holding two package names, a directory with a file that does not parse, an
// import path outside the calling file's module, and a directory the scan did not read.

const (
	// maxIndexedPackages bounds the package directories one pass parses (HISS-02).
	maxIndexedPackages = 512
	// maxModuleDepth bounds the directories walked up to find a go.mod (HISS-02).
	maxModuleDepth = 64
	// maxModuleManifestBytes bounds the go.mod read for a module (HISS-02).
	maxModuleManifestBytes = 1 << 20
)

// ignoredContext is a third-party function that takes a context and performs no I/O with it at
// every version from Min to Max inclusive, read from the source Evidence cites.
type ignoredContext struct {
	// Module is the module path whose required version the calling module's go.mod states.
	Module string
	// Min and Max bound the versions whose source was read.
	Min, Max string
	// With lists the other modules whose source the evidence also read; the go.mod must require
	// each at a version it covers too, because the build selects the highest version of each
	// module independently.
	With []ignoredModule
	// Evidence cites what that source does with the context.
	Evidence string
}

// ignoredModule is a module whose versions from Min to Max inclusive were read.
type ignoredModule struct {
	Module, Min, Max string
}

// contextIgnoredBy lists the third-party functions HISS-02 accepts a deadline-free context for,
// keyed by import path and function name. Each entry names the versions read and what the
// source does; a version outside the range, a module the go.mod replaces, a module inside a Go
// workspace and a call without a go.mod are reported as before (allowedAt).
// docs/standards/hiss-rule-matching.md says how to propose one.
var contextIgnoredBy = map[goFunc]ignoredContext{
	{"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc", "New"}: {
		Module: "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc",
		Min:    "v1.46.0", Max: "v1.46.0",
		With: []ignoredModule{{
			Module: "go.opentelemetry.io/otel/exporters/otlp/otlptrace", Min: "v1.46.0", Max: "v1.46.0",
		}},
		Evidence: "otlptracegrpc exporter.go New passes ctx to otlptrace.New, which lives in the separate " +
			"module go.opentelemetry.io/otel/exporters/otlp/otlptrace (read at v1.46.0, checked beside " +
			"otlptracegrpc): its Exporter.Start hands ctx to client.Start(context.Context) in " +
			"otlptracegrpc client.go, which leaves the parameter unnamed; grpc.NewClient there opens no connection",
	},
	{"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc", "New"}: {
		Module: "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc",
		Min:    "v1.46.0", Max: "v1.46.0",
		Evidence: "exporter.go New passes ctx only to newClient(_ context.Context, cfg) in client.go; " +
			"grpc.NewClient there opens no connection",
	},
	{"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc", "New"}: {
		Module: "go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc",
		Min:    "v0.22.0", Max: "v0.22.0",
		Evidence: "exporter.go declares New(_ context.Context, options ...Option); this release requires " +
			"go.opentelemetry.io/otel v1.46.0",
	},
}

// moduleIndex resolves proofs over the Go files of one scan.
type moduleIndex struct {
	root string
	fset *token.FileSet
	// files maps a slash-separated directory, relative to root, to the scanned non-test Go files
	// in it.
	files    map[string][]string
	parsed   map[string][]parsedFile
	packages map[pkgKey]*pkgIndex
	// modules caches the module that owns each directory looked up; nil means none.
	modules  map[string]*goModule
	analyses map[calleeKey]calleeAnalysis
}

// parsedFile is one Go file of a package directory with the imports it binds.
type parsedFile struct {
	file *ast.File
	im   GoImports
}

// pkgKey names one package: its directory and, for a package a call reaches from inside it, its
// name; an empty name asks for the directory's only package.
type pkgKey struct {
	dir, name string
}

// pkgIndex is one package's declarations. A nil entry marks a name declared more than once.
type pkgIndex struct {
	funcs map[string]*funcEntry
	// methods is keyed by receiver base type and method name, "T.M".
	methods map[string]*funcEntry
	// loggerFields maps a struct type to its fields, each true when declared *slog.Logger.
	loggerFields map[string]map[string]bool
}

// funcEntry is one function or method declaration with the file context it resolves names in.
type funcEntry struct {
	decl     *ast.FuncDecl
	im       GoImports
	dir, pkg string
}

// goModule is what a go.mod declares that the pass needs.
type goModule struct {
	dir      string
	path     string
	requires map[string]string
	replaced map[string]bool
}

// newModuleIndex groups the scanned non-test Go files by directory.
func newModuleIndex(root string, paths []string) *moduleIndex {
	x := &moduleIndex{
		root: root, fset: token.NewFileSet(),
		files: make(map[string][]string), parsed: make(map[string][]parsedFile),
		packages: make(map[pkgKey]*pkgIndex), modules: make(map[string]*goModule),
		analyses: make(map[calleeKey]calleeAnalysis),
	}
	for i := 0; i < len(paths) && i < maxCallGraphFiles; i++ {
		rel, err := filepath.Rel(root, paths[i])
		if err != nil || strings.HasSuffix(paths[i], "_test.go") {
			continue
		}
		dir := slashDir(rel)
		x.files[dir] = append(x.files[dir], paths[i])
	}
	return x
}

// discharges reports whether a proof holds: what the call reaches needs nothing more, or every
// callee parameter it hands a deadline-free context to is bounded (boundedAll).
func (x *moduleIndex) discharges(p ioProof) bool {
	deps, ok := x.classify(p.target, p.args)
	return ok && x.boundedAll(deps)
}

// classify decides what a call to t with deadline-free contexts at args needs: nothing more for
// a log/slog sink on a receiver field or an allow-listed function at a checked version, the
// listed callee parameters to be bounded for a function of the module, or failure.
func (x *moduleIndex) classify(t ioTarget, args []int) ([]calleeKey, bool) {
	if t.Field != "" {
		return nil, x.loggerField(t)
	}
	if allowed, listed := contextIgnoredBy[goFunc{Path: t.ImportPath, Name: t.Name}]; listed {
		return nil, x.allowedAt(t.Dir, allowed)
	}
	entry := x.function(t)
	if entry == nil {
		return nil, false
	}
	deps := make([]calleeKey, 0, len(args))
	for i := 0; i < len(args); i++ {
		deps = append(deps, calleeKey{entry: entry, param: args[i]})
	}
	return deps, true
}

// allowedAt reports whether the module owning dir requires entry's module at a version entry
// covers, with no replace directive for it. Inside a Go workspace the build selects versions
// across every module the go.work uses, so no single go.mod states the version and nothing is
// allowed.
func (x *moduleIndex) allowedAt(dir string, entry ignoredContext) bool {
	mod := x.moduleFor(dir)
	if mod == nil || mod.replaced[entry.Module] || x.inWorkspace(mod.dir) {
		return false
	}
	if !versionWithin(mod.requires[entry.Module], entry.Min, entry.Max) {
		return false
	}
	for i := 0; i < len(entry.With); i++ {
		other := entry.With[i]
		if mod.replaced[other.Module] || !versionWithin(mod.requires[other.Module], other.Min, other.Max) {
			return false
		}
	}
	return true
}

// inWorkspace reports whether a go.work lies at dir or above it within the root. A go.work
// that cannot be checked counts as present.
func (x *moduleIndex) inWorkspace(dir string) bool {
	at := dir
	for depth := 0; depth < maxModuleDepth; depth++ {
		path, err := util.ConfinePath(x.root, slashpath.Join(at, "go.work"))
		if err != nil {
			return true
		}
		if _, statErr := os.Stat(path); !errors.Is(statErr, fs.ErrNotExist) {
			return true
		}
		if at == "." {
			return false
		}
		at = slashpath.Dir(at)
	}
	return true
}

// versionWithin reports whether version lies from low to high inclusive under SemVer precedence.
// A version that does not parse lies nowhere.
func versionWithin(version, low, high string) bool {
	v, ok := semver.Parse(version)
	lo, loOK := semver.Parse(low)
	hi, hiOK := semver.Parse(high)
	return ok && loOK && hiOK && semver.Compare(lo, v) <= 0 && semver.Compare(v, hi) <= 0
}

// loggerField reports whether t's field of the struct type t.Struct is declared *slog.Logger.
func (x *moduleIndex) loggerField(t ioTarget) bool {
	p := x.packageAt(t.Dir, t.Pkg)
	if p == nil {
		return false
	}
	fields := p.loggerFields[t.Struct]
	return fields != nil && fields[t.Field]
}

// function resolves t to the one declaration it names in the module, or nil.
func (x *moduleIndex) function(t ioTarget) *funcEntry {
	dir, name := t.Dir, t.Pkg
	if t.ImportPath != "" {
		imported, ok := x.importDir(t.Dir, t.ImportPath)
		if !ok {
			return nil
		}
		dir, name = imported, ""
	}
	p := x.packageAt(dir, name)
	switch {
	case p == nil:
		return nil
	case t.RecvType != "":
		return p.methods[t.RecvType+"."+t.Name]
	default:
		return p.funcs[t.Name]
	}
}

// importDir maps importPath to the directory of the module owning from, when the path lies in
// that module, the directory belongs to it rather than to a nested module, and the scan read it.
func (x *moduleIndex) importDir(from, importPath string) (string, bool) {
	mod := x.moduleFor(from)
	if mod == nil {
		return "", false
	}
	rest, inside := strings.CutPrefix(importPath, mod.path)
	if !inside || (rest != "" && !strings.HasPrefix(rest, "/")) {
		return "", false
	}
	dir := slashpath.Join(mod.dir, strings.TrimPrefix(rest, "/"))
	if _, scanned := x.files[dir]; !scanned || x.moduleFor(dir) != mod {
		return "", false
	}
	return dir, true
}

// moduleFor returns the module whose go.mod is the nearest one at or above dir within the root,
// or nil when there is none or the nearest one cannot be read.
func (x *moduleIndex) moduleFor(dir string) *goModule {
	visited := make([]string, 0, 4)
	var found *goModule
	at := dir
	for depth := 0; depth < maxModuleDepth; depth++ {
		if mod, cached := x.modules[at]; cached {
			found = mod
			break
		}
		visited = append(visited, at)
		mod, stop := x.readModule(at)
		if stop || at == "." {
			found = mod
			break
		}
		at = slashpath.Dir(at)
	}
	for i := 0; i < len(visited); i++ {
		x.modules[visited[i]] = found
	}
	return found
}

// readModule reads dir's go.mod. stop is false only when there is none, so the search continues
// upward; a manifest that cannot be read or declares no module stops it with no module.
func (x *moduleIndex) readModule(dir string) (*goModule, bool) {
	data, err := util.ReadConfinedLimited(x.root, slashpath.Join(dir, "go.mod"), maxModuleManifestBytes)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false
	}
	if err != nil {
		return nil, true
	}
	manifest := gomanifest.ParseManifest(data)
	if manifest.Module == "" {
		return nil, true
	}
	mod := &goModule{dir: dir, path: manifest.Module, requires: make(map[string]string), replaced: make(map[string]bool)}
	for i := 0; i < len(manifest.Requires); i++ {
		mod.requires[manifest.Requires[i].Path] = manifest.Requires[i].Version
	}
	for i := 0; i < len(manifest.Replaces); i++ {
		mod.replaced[manifest.Replaces[i].OldPath] = true
	}
	return mod, true
}

// packageAt returns the index of one package, loading it on first use, or nil when it cannot be
// pinned down or the pass already loaded maxIndexedPackages.
func (x *moduleIndex) packageAt(dir, name string) *pkgIndex {
	key := pkgKey{dir: dir, name: name}
	if p, loaded := x.packages[key]; loaded {
		return p
	}
	if len(x.packages) >= maxIndexedPackages {
		return nil
	}
	p := x.load(dir, name)
	x.packages[key] = p
	return p
}

// load indexes the files of dir that declare package name, or the directory's only package
// when name is empty.
func (x *moduleIndex) load(dir, name string) *pkgIndex {
	files, ok := x.parseDir(dir)
	if !ok {
		return nil
	}
	if name == "" {
		name = soleName(files)
	}
	if name == "" {
		return nil
	}
	p := &pkgIndex{funcs: map[string]*funcEntry{}, methods: map[string]*funcEntry{}, loggerFields: map[string]map[string]bool{}}
	for i := 0; i < len(files); i++ {
		if files[i].file.Name.Name == name {
			p.add(files[i], dir, name)
		}
	}
	return p
}

// parseDir parses every scanned non-test Go file of dir once. A file that does not parse fails
// the whole directory: a declaration it holds could make another resolution wrong.
func (x *moduleIndex) parseDir(dir string) ([]parsedFile, bool) {
	if files, done := x.parsed[dir]; done {
		return files, len(files) > 0
	}
	paths := x.files[dir]
	files := make([]parsedFile, 0, len(paths))
	for i := 0; i < len(paths) && i < maxCallGraphFiles; i++ {
		file, err := parser.ParseFile(x.fset, paths[i], nil, parser.SkipObjectResolution)
		if err != nil || file == nil || file.Name == nil {
			x.parsed[dir] = nil
			return nil, false
		}
		files = append(files, parsedFile{file: file, im: FileImports(file)})
	}
	x.parsed[dir] = files
	return files, len(files) > 0
}

// soleName returns the one package name the files declare, or "" for none or several.
func soleName(files []parsedFile) string {
	name := ""
	for i := 0; i < len(files); i++ {
		switch declared := files[i].file.Name.Name; {
		case name == "":
			name = declared
		case declared != name:
			return ""
		}
	}
	return name
}

// add indexes one file's functions, methods and struct types.
func (p *pkgIndex) add(pf parsedFile, dir, name string) {
	decls := pf.file.Decls
	for i := 0; i < len(decls); i++ {
		switch d := decls[i].(type) {
		case *ast.FuncDecl:
			p.addFunc(&funcEntry{decl: d, im: pf.im, dir: dir, pkg: name})
		case *ast.GenDecl:
			p.addTypes(d, pf.im)
		}
	}
}

// addFunc indexes a function under its name, or a method under "T.M".
func (p *pkgIndex) addFunc(entry *funcEntry) {
	decl := entry.decl
	if decl.Name == nil {
		return
	}
	table, key := p.funcs, decl.Name.Name
	if decl.Recv != nil {
		if len(decl.Recv.List) == 0 {
			return
		}
		recv, ok := ReceiverTypeName(decl.Recv.List[0].Type)
		if !ok {
			return
		}
		table, key = p.methods, recv+"."+decl.Name.Name
	}
	if _, seen := table[key]; seen {
		table[key] = nil
		return
	}
	table[key] = entry
}

// addTypes indexes the struct types of a type declaration with the fields each declares
// *slog.Logger. An embedded *slog.Logger is the field Logger.
func (p *pkgIndex) addTypes(gen *ast.GenDecl, im GoImports) {
	if gen.Tok != token.TYPE {
		return
	}
	for i := 0; i < len(gen.Specs); i++ {
		spec, ok := gen.Specs[i].(*ast.TypeSpec)
		if !ok || spec.Name == nil {
			continue
		}
		if _, seen := p.loggerFields[spec.Name.Name]; seen {
			p.loggerFields[spec.Name.Name] = nil
			continue
		}
		p.loggerFields[spec.Name.Name] = structLoggerFields(spec, im)
	}
}

// structLoggerFields maps each named field of a struct type to whether it is *slog.Logger.
func structLoggerFields(spec *ast.TypeSpec, im GoImports) map[string]bool {
	fields := map[string]bool{}
	st, ok := spec.Type.(*ast.StructType)
	if !ok || st.Fields == nil {
		return fields
	}
	for i := 0; i < len(st.Fields.List); i++ {
		field := st.Fields.List[i]
		logger := isLoggerPointer(im, field.Type)
		if len(field.Names) == 0 && logger {
			fields["Logger"] = true
		}
		for j := 0; j < len(field.Names); j++ {
			fields[field.Names[j].Name] = logger
		}
	}
	return fields
}
