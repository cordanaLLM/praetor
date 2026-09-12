package astmerge

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"os"
	"sort"
	"strings"
	"time"
)

const (
	maxASTDeclarations = 2000
	maxImportCount     = 500
	defaultFileTimeout = 5 * time.Second
)

// Conflict represents a genuine collision between branches on a specific symbol.
type Conflict struct {
	Symbol string `json:"symbol"`
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	Ours   string `json:"ours,omitempty"`
	Theirs string `json:"theirs,omitempty"`
	Base   string `json:"base,omitempty"`
}

// MergeResult encapsulates the outcome of a semantic 3-way AST merge.
type MergeResult struct {
	MergedCode    string     `json:"merged_code"`
	Conflicts     []Conflict `json:"conflicts,omitempty"`
	Clean         bool       `json:"clean"`
	ResolvedCount int        `json:"resolved_count"`
}

// ImportItem stores path and alias for an import declaration.
type ImportItem struct {
	Path  string
	Alias string
}

// DeclItem represents a top-level AST declaration.
type DeclItem struct {
	Key   string
	Kind  string
	Name  string
	Body  string
	Order int
}

// ParsedAST represents the extracted structural components of a Go file.
type ParsedAST struct {
	PackageName string
	Imports     map[string]ImportItem
	Decls       map[string]DeclItem
	DeclOrder   []string
}

// Merge executes a 3-way semantic AST merge between Base, Ours, and Theirs Go code.
func Merge(baseSrc, oursSrc, theirsSrc string) (*MergeResult, error) {
	if oursSrc == theirsSrc {
		return &MergeResult{
			MergedCode:    oursSrc,
			Clean:         true,
			ResolvedCount: 0,
		}, nil
	}

	baseAST, err := parseSourceSafe("base.go", baseSrc)
	if err != nil && strings.TrimSpace(baseSrc) != "" {
		return nil, fmt.Errorf("failed to parse base source: %w", err)
	}

	oursAST, err := parseSourceSafe("ours.go", oursSrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ours source: %w", err)
	}

	theirsAST, err := parseSourceSafe("theirs.go", theirsSrc)
	if err != nil {
		return nil, fmt.Errorf("failed to parse theirs source: %w", err)
	}

	return resolve3Way(baseAST, oursAST, theirsAST)
}

// MergeFiles reads and executes a 3-way AST merge from file paths.
func MergeFiles(basePath, oursPath, theirsPath string) (*MergeResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), defaultFileTimeout)
	defer cancel()

	baseBytes, err := readFileWithContext(ctx, basePath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("failed reading base file %s: %w", basePath, err)
	}

	oursBytes, err := readFileWithContext(ctx, oursPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading ours file %s: %w", oursPath, err)
	}

	theirsBytes, err := readFileWithContext(ctx, theirsPath)
	if err != nil {
		return nil, fmt.Errorf("failed reading theirs file %s: %w", theirsPath, err)
	}

	return Merge(string(baseBytes), string(oursBytes), string(theirsBytes))
}

func readFileWithContext(ctx context.Context, path string) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	// #nosec G304 -- path is a caller-supplied source file to analyze; the content is only
	// parsed as Go source, never executed, and the read is gated on the caller's context.
	return os.ReadFile(path)
}

func parseSourceSafe(filename, src string) (*ParsedAST, error) {
	if strings.TrimSpace(src) == "" {
		return &ParsedAST{
			PackageName: "",
			Imports:     make(map[string]ImportItem),
			Decls:       make(map[string]DeclItem),
			DeclOrder:   nil,
		}, nil
	}

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("parse error: %w", err)
	}

	return extractASTElements(fset, file, src)
}

func extractASTElements(fset *token.FileSet, file *ast.File, src string) (*ParsedAST, error) {
	p := &ParsedAST{
		PackageName: file.Name.Name,
		Imports:     make(map[string]ImportItem),
		Decls:       make(map[string]DeclItem),
		DeclOrder:   make([]string, 0, len(file.Decls)),
	}

	declsLimit := len(file.Decls)
	if declsLimit > maxASTDeclarations {
		declsLimit = maxASTDeclarations
	}

	for i := 0; i < declsLimit; i++ {
		decl := file.Decls[i]
		switch d := decl.(type) {
		case *ast.GenDecl:
			processGenDecl(fset, d, src, p, i)
		case *ast.FuncDecl:
			processFuncDecl(fset, d, src, p, i)
		}
	}

	return p, nil
}

func processGenDecl(fset *token.FileSet, d *ast.GenDecl, src string, p *ParsedAST, order int) {
	if d.Tok == token.IMPORT {
		processImportSpecs(d.Specs, p)
		return
	}

	body := extractNodeSource(fset, d, src)
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			key := "type:" + s.Name.Name
			p.Decls[key] = DeclItem{Key: key, Kind: "type", Name: s.Name.Name, Body: body, Order: order}
			p.DeclOrder = append(p.DeclOrder, key)
		case *ast.ValueSpec:
			for _, name := range s.Names {
				kind := "var"
				if d.Tok == token.CONST {
					kind = "const"
				}
				key := kind + ":" + name.Name
				p.Decls[key] = DeclItem{Key: key, Kind: kind, Name: name.Name, Body: body, Order: order}
				p.DeclOrder = append(p.DeclOrder, key)
			}
		}
	}
}

func processImportSpecs(specs []ast.Spec, p *ParsedAST) {
	specsLimit := len(specs)
	for j := 0; j < specsLimit && j < maxImportCount; j++ {
		if imp, ok := specs[j].(*ast.ImportSpec); ok {
			path := imp.Path.Value
			alias := ""
			if imp.Name != nil {
				alias = imp.Name.Name
			}
			p.Imports[path] = ImportItem{Path: path, Alias: alias}
		}
	}
}

func processFuncDecl(fset *token.FileSet, d *ast.FuncDecl, src string, p *ParsedAST, order int) {
	key := "func:" + d.Name.Name
	kind := "func"
	if d.Recv != nil && len(d.Recv.List) > 0 {
		kind = "method"
		recvType := extractReceiverName(d.Recv.List[0].Type)
		key = fmt.Sprintf("method:%s.%s", recvType, d.Name.Name)
	}

	body := extractNodeSource(fset, d, src)
	p.Decls[key] = DeclItem{
		Key:   key,
		Kind:  kind,
		Name:  d.Name.Name,
		Body:  body,
		Order: order,
	}
	p.DeclOrder = append(p.DeclOrder, key)
}

func extractReceiverName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		if ident, ok := t.X.(*ast.Ident); ok {
			return "*" + ident.Name
		}
		return "*unknown"
	default:
		return "unknown"
	}
}

func extractNodeSource(fset *token.FileSet, node ast.Node, src string) string {
	start := fset.Position(node.Pos()).Offset
	end := fset.Position(node.End()).Offset

	if doc := declarationDoc(node); doc != nil {
		docStart := fset.Position(doc.Pos()).Offset
		if docStart < start && docStart >= 0 {
			start = docStart
		}
	}

	if start < 0 {
		start = 0
	}
	if end > len(src) {
		end = len(src)
	}
	if start >= end {
		return ""
	}

	return strings.TrimSpace(src[start:end])
}

func declarationDoc(node ast.Node) *ast.CommentGroup {
	switch d := node.(type) {
	case *ast.GenDecl:
		return d.Doc
	case *ast.FuncDecl:
		return d.Doc
	default:
		return nil
	}
}

func resolve3Way(base, ours, theirs *ParsedAST) (*MergeResult, error) {
	var conflicts []Conflict

	pkgName, pkgConflict := resolvePackageName(base.PackageName, ours.PackageName, theirs.PackageName)
	if pkgConflict != nil {
		conflicts = append(conflicts, *pkgConflict)
	}

	mergedImports, impConflicts := mergeImports3Way(base.Imports, ours.Imports, theirs.Imports)
	conflicts = append(conflicts, impConflicts...)

	mergedDecls, resolvedCount, declConflicts := mergeDecls3Way(base, ours, theirs)
	conflicts = append(conflicts, declConflicts...)

	if len(conflicts) > 0 {
		return &MergeResult{
			MergedCode:    "",
			Conflicts:     conflicts,
			Clean:         false,
			ResolvedCount: resolvedCount,
		}, nil
	}

	code, err := renderGoCode(pkgName, mergedImports, mergedDecls)
	if err != nil {
		return nil, fmt.Errorf("failed to format merged code: %w", err)
	}

	return &MergeResult{
		MergedCode:    code,
		Conflicts:     nil,
		Clean:         true,
		ResolvedCount: resolvedCount,
	}, nil
}

func resolvePackageName(base, ours, theirs string) (string, *Conflict) {
	if ours == theirs {
		return ours, nil
	}
	if ours == base && theirs != "" {
		return theirs, nil
	}
	if theirs == base && ours != "" {
		return ours, nil
	}
	if base == "" {
		if ours != "" && theirs == "" {
			return ours, nil
		}
		if theirs != "" && ours == "" {
			return theirs, nil
		}
	}

	return ours, &Conflict{
		Symbol: "package",
		Kind:   "package",
		Reason: fmt.Sprintf("Conflicting package names: ours=%q, theirs=%q", ours, theirs),
		Ours:   ours,
		Theirs: theirs,
		Base:   base,
	}
}

func mergeImports3Way(base, ours, theirs map[string]ImportItem) ([]ImportItem, []Conflict) {
	allPaths := make(map[string]struct{})
	for p := range base {
		allPaths[p] = struct{}{}
	}
	for p := range ours {
		allPaths[p] = struct{}{}
	}
	for p := range theirs {
		allPaths[p] = struct{}{}
	}

	var merged []ImportItem
	var conflicts []Conflict

	paths := make([]string, 0, len(allPaths))
	for p := range allPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	limit := len(paths)
	for i := 0; i < limit && i < maxImportCount; i++ {
		item, conflict := resolveImport(paths[i], base, ours, theirs)
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		} else if item != nil {
			merged = append(merged, *item)
		}
	}

	return merged, conflicts
}

func resolveImport(path string, base, ours, theirs map[string]ImportItem) (*ImportItem, *Conflict) {
	_, inBase := base[path]
	o, inOurs := ours[path]
	t, inTheirs := theirs[path]
	if inOurs && inTheirs {
		if o.Alias == t.Alias {
			return &o, nil
		}
		return nil, &Conflict{
			Symbol: "import:" + path,
			Kind:   "import",
			Reason: fmt.Sprintf("Conflicting import aliases for %s: %q vs %q", path, o.Alias, t.Alias),
			Ours:   o.Alias,
			Theirs: t.Alias,
		}
	}
	if inBase {
		return nil, nil // Omitted when deleted by either side.
	}
	if inOurs {
		return &o, nil
	}
	if inTheirs {
		return &t, nil
	}
	return nil, nil
}

func mergeDecls3Way(base, ours, theirs *ParsedAST) ([]string, int, []Conflict) {
	allKeys := collectOrderedKeys(base, ours, theirs)
	var mergedDecls []string
	var conflicts []Conflict
	resolved := 0

	limit := len(allKeys)
	for i := 0; i < limit && i < maxASTDeclarations; i++ {
		key := allKeys[i]
		b, inB := base.Decls[key]
		o, inO := ours.Decls[key]
		t, inT := theirs.Decls[key]

		resBody, resCount, conflict := resolveSingleSymbol(key, b, inB, o, inO, t, inT)
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		} else if resBody != "" {
			mergedDecls = append(mergedDecls, resBody)
			resolved += resCount
		}
	}

	return mergedDecls, resolved, conflicts
}

func resolveSingleSymbol(key string, b DeclItem, inB bool, o DeclItem, inO bool, t DeclItem, inT bool) (string, int, *Conflict) {
	switch {
	case inO && inT:
		return resolveModifiedSymbol(key, b, inB, o, t)
	case inO && !inT:
		if !inB {
			return o.Body, 1, nil // Disjoint addition in ours
		}
		if o.Body != b.Body {
			return "", 0, &Conflict{
				Symbol: key,
				Kind:   o.Kind,
				Reason: fmt.Sprintf("Conflict: symbol %s modified in ours but deleted in theirs", key),
				Ours:   o.Body,
				Base:   b.Body,
			}
		}
		return "", 1, nil // Deleted cleanly by theirs
	case !inO && inT:
		if !inB {
			return t.Body, 1, nil // Disjoint addition in theirs
		}
		if t.Body != b.Body {
			return "", 0, &Conflict{
				Symbol: key,
				Kind:   t.Kind,
				Reason: fmt.Sprintf("Conflict: symbol %s modified in theirs but deleted in ours", key),
				Theirs: t.Body,
				Base:   b.Body,
			}
		}
		return "", 1, nil // Deleted cleanly by ours
	default:
		return "", 0, nil
	}
}

func resolveModifiedSymbol(key string, b DeclItem, inB bool, o, t DeclItem) (string, int, *Conflict) {
	if o.Body == t.Body {
		return o.Body, 1, nil
	}
	if inB && o.Body == b.Body {
		return t.Body, 1, nil // Changed only in theirs
	}
	if inB && t.Body == b.Body {
		return o.Body, 1, nil // Changed only in ours
	}
	return "", 0, &Conflict{
		Symbol: key,
		Kind:   o.Kind,
		Reason: fmt.Sprintf("Conflicting modifications to declaration %s", key),
		Ours:   o.Body,
		Theirs: t.Body,
		Base:   b.Body,
	}
}

func collectOrderedKeys(base, ours, theirs *ParsedAST) []string {
	seen := make(map[string]bool)
	var order []string

	appendKeys := func(keys []string) {
		limit := len(keys)
		for i := 0; i < limit; i++ {
			k := keys[i]
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}

	appendKeys(base.DeclOrder)
	appendKeys(ours.DeclOrder)
	appendKeys(theirs.DeclOrder)

	return order
}

func renderGoCode(pkgName string, imports []ImportItem, decls []string) (string, error) {
	if pkgName == "" {
		pkgName = "main"
	}

	var b strings.Builder
	fmt.Fprintf(&b, "package %s\n\n", pkgName)

	if len(imports) > 0 {
		b.WriteString("import (\n")
		limit := len(imports)
		for i := 0; i < limit; i++ {
			imp := imports[i]
			if imp.Alias != "" {
				fmt.Fprintf(&b, "\t%s %s\n", imp.Alias, imp.Path)
			} else {
				fmt.Fprintf(&b, "\t%s\n", imp.Path)
			}
		}
		b.WriteString(")\n\n")
	}

	limit := len(decls)
	for i := 0; i < limit; i++ {
		b.WriteString(decls[i])
		b.WriteString("\n\n")
	}

	formatted, err := format.Source([]byte(b.String()))
	if err != nil {
		return b.String(), fmt.Errorf("gofmt format error: %w", err)
	}

	return string(formatted), nil
}
