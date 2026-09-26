package astmerge

import (
	"fmt"
	"go/ast"
	"go/token"
	"strconv"
	"strings"

	"github.com/cordanaLLM/praetor/internal/hiss"
)

// maxDeclItems bounds the merge items one file yields: top-level declarations, the specs
// of parenthesized blocks and free-floating comments. Exceeding it is an error rather than
// a silent truncation (BUG-212).
const maxDeclItems = 20000

// kindComment is the DeclItem kind of a comment group no declaration owns.
const kindComment = "comment"

// extractor turns one parsed file into merge items in source order. It walks the file's
// comment groups alongside its declarations, so a comment that no declaration owns becomes
// an item of its own rather than being dropped (BUG-214).
type extractor struct {
	fset     *token.FileSet
	src      string
	p        *ParsedAST
	comments []*ast.CommentGroup
	// next is the index of the first comment group not yet emitted or skipped.
	next int
	// seen counts the items added per base key, so a repeated key gets the next suffix.
	seen map[string]int
	err  error
}

func extractASTElements(fset *token.FileSet, file *ast.File, src string) (*ParsedAST, error) {
	// A silent truncation at the bound let a large file's tail declarations vanish under
	// a clean report (BUG-212); erroring instead makes the caller decide, rather than
	// merging a partial view of one side.
	if len(file.Decls) > maxASTDeclarations {
		return nil, fmt.Errorf("source has %d top-level declarations, exceeding the %d bound", len(file.Decls), maxASTDeclarations)
	}

	build, leading := headerGroups(file)
	p := &ParsedAST{
		PackageName:      file.Name.Name,
		PackageDoc:       groupSource(fset, src, file.Doc),
		BuildConstraints: groupSource(fset, src, build),
		LeadingComments:  joinGroupSource(fset, src, leading),
		Imports:          make(map[string]ImportItem),
		Decls:            make(map[string]DeclItem),
		DeclOrder:        make([]string, 0, len(file.Decls)),
		positional:       make(map[string]bool),
	}

	x := &extractor{fset: fset, src: src, p: p, comments: file.Comments, seen: make(map[string]int)}
	x.skipComments(file.Name.End())
	for i, decl := range file.Decls {
		start, end := declSpan(decl)
		x.freeComments(start, "", i)
		x.addDecl(decl, i, start, end)
		x.skipComments(end)
	}
	x.freeComments(file.FileEnd, "", len(file.Decls))
	if x.err != nil {
		return nil, x.err
	}
	return p, nil
}

// headerGroups splits the comment groups before the package clause, other than the package
// doc, into the build constraint and the rest, such as a license header. A group attached
// as the package doc is documentation even when it reads like a constraint: without the
// blank line before the package clause, the go command does not treat it as one.
func headerGroups(file *ast.File) (*ast.CommentGroup, []*ast.CommentGroup) {
	var build *ast.CommentGroup
	var leading []*ast.CommentGroup
	for _, group := range file.Comments {
		if group.Pos() >= file.Package {
			break
		}
		switch {
		case group == file.Doc:
		case build == nil && isBuildConstraintGroup(group):
			build = group
		default:
			leading = append(leading, group)
		}
	}
	return build, leading
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

// groupSource returns a comment group's source text, "" for no group.
func groupSource(fset *token.FileSet, src string, group *ast.CommentGroup) string {
	if group == nil {
		return ""
	}
	return sourceSpan(fset, src, group.Pos(), group.End())
}

// joinGroupSource returns the groups' source texts separated by blank lines.
func joinGroupSource(fset *token.FileSet, src string, groups []*ast.CommentGroup) string {
	texts := make([]string, 0, len(groups))
	for _, group := range groups {
		texts = append(texts, groupSource(fset, src, group))
	}
	return strings.Join(texts, "\n\n")
}

// sourceSpan returns the trimmed source between two positions, clamped to the source.
func sourceSpan(fset *token.FileSet, src string, from, to token.Pos) string {
	start := max(fset.Position(from).Offset, 0)
	end := min(fset.Position(to).Offset, len(src))
	if start >= end {
		return ""
	}
	return strings.TrimSpace(src[start:end])
}

// declSpan returns where a declaration's source starts, its doc comment included, and
// where it ends, the line comment of a single unparenthesized spec included.
func declSpan(decl ast.Decl) (token.Pos, token.Pos) {
	start, end := decl.Pos(), decl.End()
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Doc != nil {
			start = d.Doc.Pos()
		}
	case *ast.GenDecl:
		if d.Doc != nil {
			start = d.Doc.Pos()
		}
		if !d.Lparen.IsValid() && len(d.Specs) == 1 {
			_, specEnd := specSpan(d.Specs[0])
			end = max(end, specEnd)
		}
	}
	return start, end
}

// specSpan returns a spec's source span, its doc and line comments included.
func specSpan(spec ast.Spec) (token.Pos, token.Pos) {
	var doc, line *ast.CommentGroup
	switch s := spec.(type) {
	case *ast.ValueSpec:
		doc, line = s.Doc, s.Comment
	case *ast.TypeSpec:
		doc, line = s.Doc, s.Comment
	case *ast.ImportSpec:
		doc, line = s.Doc, s.Comment
	}
	start, end := spec.Pos(), spec.End()
	if doc != nil {
		start = min(start, doc.Pos())
	}
	if line != nil {
		end = max(end, line.End())
	}
	return start, end
}

func (x *extractor) span(from, to token.Pos) string {
	return sourceSpan(x.fset, x.src, from, to)
}

func (x *extractor) addDecl(decl ast.Decl, order int, start, end token.Pos) {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		item := funcItem(d)
		item.Order, item.Body = order, x.span(start, end)
		x.add(item)
	case *ast.GenDecl:
		switch {
		case d.Tok == token.IMPORT:
			processImportSpecs(d.Specs, x.p)
		case d.Lparen.IsValid():
			x.addBlock(d, order)
		case len(d.Specs) == 1:
			item := specItem(d.Tok, d.Specs[0])
			item.Order, item.Body = order, x.span(start, end)
			x.addSpec(item, d.Tok, d.Specs[0])
		}
	}
}

// addBlock adds one item per spec of a parenthesized declaration and one per comment
// between its specs. Keying the whole block under every name it declares rendered a
// grouped block once per name and turned an edit to one spec into a conflict on all of
// them (BUG-213). The block's doc comment travels as the Header of its first item.
func (x *extractor) addBlock(d *ast.GenDecl, order int) {
	kind := d.Tok.String()
	x.skipComments(d.Lparen)
	var keys []string
	for _, spec := range d.Specs {
		start, end := specSpan(spec)
		keys = append(keys, x.freeComments(start, kind, order)...)
		item := specItem(d.Tok, spec)
		item.Block, item.Order, item.Body = kind, order, x.span(start, end)
		if key, ok := x.addSpec(item, d.Tok, spec); ok {
			keys = append(keys, key)
		}
		x.skipComments(end)
	}
	keys = append(keys, x.freeComments(d.Rparen, kind, order)...)
	if d.Doc != nil {
		x.attachHeader(keys, x.span(d.Doc.Pos(), d.Doc.End()), order)
	}
	x.p.Blocks = append(x.p.Blocks, keys)
}

// attachHeader carries a block's doc comment on its first item; the doc of an empty block
// is kept as a free-floating comment.
func (x *extractor) attachHeader(keys []string, header string, order int) {
	if len(keys) == 0 {
		x.add(DeclItem{Key: kindComment + ":" + header, Kind: kindComment, Body: header, Order: order})
		return
	}
	first := x.p.Decls[keys[0]]
	first.Header = header
	x.p.Decls[keys[0]] = first
}

// freeComments adds every comment group that starts before the position as an item keyed
// by its text, inside the named block keyword or at top level for "", and returns the keys.
func (x *extractor) freeComments(before token.Pos, block string, order int) []string {
	var keys []string
	for ; x.next < len(x.comments) && x.comments[x.next].Pos() < before; x.next++ {
		group := x.comments[x.next]
		body := x.span(group.Pos(), group.End())
		item := DeclItem{Key: kindComment + ":" + body, Kind: kindComment, Body: body, Block: block, Order: order}
		if key, ok := x.add(item); ok {
			keys = append(keys, key)
		}
	}
	return keys
}

// skipComments passes over the comment groups that start before the position: they sit
// inside a declaration whose source text already carries them.
func (x *extractor) skipComments(before token.Pos) {
	for x.next < len(x.comments) && x.comments[x.next].Pos() < before {
		x.next++
	}
}

// addSpec adds a const, var or type spec's item and records whether the spec's value
// depends on its position in its block.
func (x *extractor) addSpec(item DeclItem, tok token.Token, spec ast.Spec) (string, bool) {
	key, ok := x.add(item)
	if ok && isPositionalSpec(tok, spec) {
		x.p.positional[key] = true
	}
	return key, ok
}

// isPositionalSpec reports whether a spec's value depends on where it stands: a const spec
// that repeats the expression of the spec before it, or one that counts with iota.
func isPositionalSpec(tok token.Token, spec ast.Spec) bool {
	value, ok := spec.(*ast.ValueSpec)
	if !ok || tok != token.CONST {
		return false
	}
	return len(value.Values) == 0 || usesIota(value.Values)
}

// usesIota reports whether any of the expressions mentions iota.
func usesIota(exprs []ast.Expr) bool {
	found := false
	for _, expr := range exprs {
		ast.Inspect(expr, func(node ast.Node) bool {
			if ident, ok := node.(*ast.Ident); ok && ident.Name == "iota" {
				found = true
			}
			return !found
		})
	}
	return found
}

// add records an item under a key no earlier item of this file holds and reports the key.
func (x *extractor) add(item DeclItem) (string, bool) {
	if x.err != nil {
		return "", false
	}
	if len(x.p.DeclOrder) >= maxDeclItems {
		x.err = fmt.Errorf("source has more than %d declarations, block specs and comments", maxDeclItems)
		return "", false
	}
	item.Key = x.uniqueKey(item.Key)
	x.p.Decls[item.Key] = item
	x.p.DeclOrder = append(x.p.DeclOrder, item.Key)
	return item.Key, true
}

// uniqueKey suffixes a key an earlier item of the same file holds. init functions,
// blank-named declarations and repeated comments legitimately recur, and a shared key let
// the last occurrence silently replace the others. The nth occurrence is keyed #n in every
// input, so occurrences still correlate across the merge by position.
func (x *extractor) uniqueKey(key string) string {
	candidate := key
	for n := x.seen[key] + 1; n <= maxDeclItems+1; n++ {
		if n > 1 {
			candidate = key + "#" + strconv.Itoa(n)
		}
		if _, taken := x.p.Decls[candidate]; !taken {
			x.seen[key] = n
			return candidate
		}
	}
	return candidate
}

// specItem keys a const, var or type spec by its keyword and every name it declares, so
// `var a, b = 1, 2` is one item rather than two that each carry the whole spec.
func specItem(tok token.Token, spec ast.Spec) DeclItem {
	kind := tok.String()
	name := ""
	switch s := spec.(type) {
	case *ast.TypeSpec:
		name = s.Name.Name
	case *ast.ValueSpec:
		names := make([]string, 0, len(s.Names))
		for _, ident := range s.Names {
			names = append(names, ident.Name)
		}
		name = strings.Join(names, ",")
	}
	return DeclItem{Key: kind + ":" + name, Kind: kind, Name: name}
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

func funcItem(d *ast.FuncDecl) DeclItem {
	if d.Recv != nil && len(d.Recv.List) > 0 {
		recvType := extractReceiverName(d.Recv.List[0].Type)
		return DeclItem{Key: fmt.Sprintf("method:%s.%s", recvType, d.Name.Name), Kind: "method", Name: d.Name.Name}
	}
	return DeclItem{Key: "func:" + d.Name.Name, Kind: "func", Name: d.Name.Name}
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
