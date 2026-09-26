package astmerge

import (
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/hiss"
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
	// PackageDoc is the doc comment directly attached to the package clause, and
	// BuildConstraints is the leading //go:build (or legacy // +build) comment group.
	// Both are carried through so a clean merge does not silently drop them (BUG-214).
	PackageDoc       string
	BuildConstraints string
	Imports          map[string]ImportItem
	Decls            map[string]DeclItem
	DeclOrder        []string
}

// Merge executes a 3-way semantic AST merge between Base, Ours, and Theirs Go code.
func Merge(baseSrc, oursSrc, theirsSrc string) (*MergeResult, error) {
	if oursSrc == theirsSrc {
		// The shortcut still has to confirm the identical text is valid Go; otherwise two
		// branches that converge on invalid source report a clean merge over code that
		// cannot compile (BUG-477).
		if _, err := parseSourceSafe("identical.go", oursSrc); err != nil {
			return nil, fmt.Errorf("ours and theirs are identical but invalid: %w", err)
		}
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

// contextReader wraps a reader so every Read call observes context cancellation. Checking
// ctx.Done() once before a blocking os.ReadFile leaves the deadline decorative for the
// read itself (BUG-478); this mirrors the pattern already used by internal/contextopt and
// internal/harvester for the same reason.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func readFileWithContext(ctx context.Context, path string) (data []byte, err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// #nosec G304 -- path is a caller-supplied source file to analyze; the content is only
	// parsed as Go source, never executed, and the read is gated on the caller's context.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, file.Close()) }()

	data, err = io.ReadAll(contextReader{ctx: ctx, reader: file})
	if err != nil {
		return nil, err
	}
	return data, ctx.Err()
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
	// A silent truncation at the bound let a large file's tail declarations vanish under
	// a clean report (BUG-212); erroring instead makes the caller decide, rather than
	// merging a partial view of one side.
	if len(file.Decls) > maxASTDeclarations {
		return nil, fmt.Errorf("source has %d top-level declarations, exceeding the %d bound", len(file.Decls), maxASTDeclarations)
	}

	p := &ParsedAST{
		PackageName:      file.Name.Name,
		PackageDoc:       extractDocSource(fset, file, src),
		BuildConstraints: extractBuildConstraints(fset, file, src),
		Imports:          make(map[string]ImportItem),
		Decls:            make(map[string]DeclItem),
		DeclOrder:        make([]string, 0, len(file.Decls)),
	}

	for i, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			processGenDecl(fset, d, src, p, i)
		case *ast.FuncDecl:
			processFuncDecl(fset, d, src, p, i)
		}
	}

	return p, nil
}

// extractDocSource renders the package doc comment (the comment group go/parser attaches
// directly to the package clause) back to source text, or "" when the file has none.
func extractDocSource(fset *token.FileSet, file *ast.File, src string) string {
	if file.Doc == nil {
		return ""
	}
	return extractNodeSource(fset, file.Doc, src)
}

// extractBuildConstraints returns the leading //go:build or // +build comment group, if
// the file has one. go/parser only attaches a comment to File.Doc when it directly
// precedes the package clause with no blank line, so a build-tagged file (which has a
// blank line between the tag and any doc comment, per gofmt convention) needs its own
// lookup; otherwise the tag is dropped by a merge with no diagnostic (BUG-214).
func extractBuildConstraints(fset *token.FileSet, file *ast.File, src string) string {
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		if isBuildConstraintGroup(group) {
			return extractNodeSource(fset, group, src)
		}
	}
	return ""
}

func isBuildConstraintGroup(group *ast.CommentGroup) bool {
	for _, c := range group.List {
		line := strings.TrimSpace(c.Text)
		if strings.HasPrefix(line, "//go:build") || strings.HasPrefix(line, "// +build") || strings.HasPrefix(line, "//+build") {
			return true
		}
	}
	return false
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

// extractReceiverName returns the merge key's receiver component. Before this fix a
// generic receiver (*Set[T]) fell through to the literal string "*unknown" because its
// inner expression is an *ast.IndexExpr, not an *ast.Ident; every generic receiver then
// collided on the same key and a clean merge could silently drop one of them (BUG-819).
func extractReceiverName(expr ast.Expr) string {
	if star, ok := expr.(*ast.StarExpr); ok {
		if name, ok := hiss.ReceiverTypeName(star.X); ok {
			return "*" + name
		}
		return "*unknown"
	}
	if name, ok := hiss.ReceiverTypeName(expr); ok {
		return name
	}
	return "unknown"
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

	buildConstraints := mergeAuxText(base.BuildConstraints, ours.BuildConstraints, theirs.BuildConstraints)
	packageDoc := mergeAuxText(base.PackageDoc, ours.PackageDoc, theirs.PackageDoc)

	code, err := renderGoCode(pkgName, buildConstraints, packageDoc, mergedImports, mergedDecls)
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

// mergeAuxText resolves a 3-way merge for a single auxiliary text blob (package doc,
// build constraints) that carries no conflict-reporting machinery of its own: unless one
// side changed it while the other held it at base, ours wins. The prior behaviour dropped
// this text unconditionally (BUG-214); biasing toward keeping content is the fix, not a
// full conflict model, which the row's fix description does not ask for.
func mergeAuxText(base, ours, theirs string) string {
	if ours == theirs {
		return ours
	}
	if ours == base {
		return theirs
	}
	if theirs == base {
		return ours
	}
	return ours
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
			continue
		}
		// A clean deletion resolves with resBody == "" and resCount == 1; counting
		// resolved only inside the resBody != "" branch undercounted every such
		// deletion (BUG-479), so the accumulation applies to any non-conflicting result.
		resolved += resCount
		if resBody != "" {
			mergedDecls = append(mergedDecls, resBody)
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

func renderGoCode(pkgName, buildConstraints, packageDoc string, imports []ImportItem, decls []string) (string, error) {
	if pkgName == "" {
		pkgName = "main"
	}

	var b strings.Builder
	// The build-constraint comment must precede the package doc comment by a blank line
	// (Go's own convention for //go:build), and the doc comment must directly precede the
	// package clause with no blank line, or go/parser would stop treating it as the
	// package's doc comment on a later merge (BUG-214).
	if buildConstraints != "" {
		b.WriteString(buildConstraints)
		b.WriteString("\n\n")
	}
	if packageDoc != "" {
		b.WriteString(packageDoc)
		b.WriteString("\n")
	}
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
