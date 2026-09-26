package astmerge

import (
	"context"
	"errors"
	"fmt"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
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

// DeclItem represents one mergeable unit of a Go file: a function or method, the spec of
// a const, var or type declaration, or a comment group no declaration owns.
type DeclItem struct {
	Key   string
	Kind  string
	Name  string
	Body  string
	Order int
	// Block is the keyword (const, var or type) of the parenthesized declaration the item
	// is written in, or "" for an item at top level.
	Block string
	// Header is the doc comment of the block, carried by the block's first item.
	Header string
}

// ParsedAST represents the extracted structural components of a Go file.
type ParsedAST struct {
	PackageName string
	// PackageDoc is the doc comment directly attached to the package clause, and
	// BuildConstraints is the leading //go:build (or legacy // +build) comment group.
	// LeadingComments holds any other comment groups before the package clause, such as a
	// license header. All three are carried through so a clean merge does not silently drop
	// them (BUG-214).
	PackageDoc       string
	BuildConstraints string
	LeadingComments  string
	Imports          map[string]ImportItem
	Decls            map[string]DeclItem
	DeclOrder        []string
	// Blocks lists the item keys of each parenthesized declaration in source order, so
	// the merge can render the items of one block as one block again.
	Blocks [][]string
	// positional holds the keys of const specs whose value depends on their position in
	// their block: an implicit repetition of the spec before, or a use of iota.
	positional map[string]bool
}

// Merge executes a 3-way semantic AST merge between Base, Ours, and Theirs Go code. A
// result is Clean only once the merged file passes the post-merge guard (guardResult):
// anything it cannot verify is reported as a Conflict rather than merged.
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

	res, err := resolve3Way(baseAST, oursAST, theirsAST)
	if err != nil || !res.Clean {
		return res, err
	}
	return guardResult(res, baseSrc, oursSrc, theirsSrc), nil
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

func resolve3Way(base, ours, theirs *ParsedAST) (*MergeResult, error) {
	var conflicts []Conflict

	pkgName, pkgConflict := resolvePackageName(base.PackageName, ours.PackageName, theirs.PackageName)
	if pkgConflict != nil {
		conflicts = append(conflicts, *pkgConflict)
	}

	header, headerConflicts := mergeHeader(base, ours, theirs)
	conflicts = append(conflicts, headerConflicts...)

	mergedImports, impConflicts := mergeImports3Way(base.Imports, ours.Imports, theirs.Imports)
	conflicts = append(conflicts, impConflicts...)

	mergedItems, resolvedCount, declConflicts := mergeDecls3Way(base, ours, theirs)
	conflicts = append(conflicts, declConflicts...)
	conflicts = append(conflicts, commentConflicts(base, ours, theirs)...)

	blocks, blockConflicts := mergeLayout(base, ours, theirs)
	conflicts = append(conflicts, blockConflicts...)

	if len(conflicts) > 0 {
		return &MergeResult{
			MergedCode:    "",
			Conflicts:     conflicts,
			Clean:         false,
			ResolvedCount: resolvedCount,
		}, nil
	}

	decls := renderDecls(mergedItems, blocks)
	code, err := renderGoCode(pkgName, header, mergedImports, decls)
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

// fileHeader is the merged text before the package clause.
type fileHeader struct {
	buildConstraints string
	leadingComments  string
	packageDoc       string
}

// mergeHeader merges the build constraint, the other leading comments and the package doc.
func mergeHeader(base, ours, theirs *ParsedAST) (fileHeader, []Conflict) {
	var h fileHeader
	var conflicts []Conflict
	parts := []struct {
		symbol           string
		dst              *string
		base, ours, thrs string
	}{
		{"build-constraints", &h.buildConstraints, base.BuildConstraints, ours.BuildConstraints, theirs.BuildConstraints},
		{"leading-comments", &h.leadingComments, base.LeadingComments, ours.LeadingComments, theirs.LeadingComments},
		{"package-doc", &h.packageDoc, base.PackageDoc, ours.PackageDoc, theirs.PackageDoc},
	}
	for _, part := range parts {
		merged, conflict := mergeAuxText(part.symbol, part.base, part.ours, part.thrs)
		*part.dst = merged
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
		}
	}
	return h, conflicts
}

// mergeAuxText resolves a 3-way merge for a single auxiliary text blob (package doc, build
// constraints, leading comments): a side that holds it at base takes the other side's
// text. When both sides changed it differently the merge reports a conflict; returning
// ours there silently discarded theirs, so a divergent //go:build line merged "clean"
// under the wrong constraint (#392).
func mergeAuxText(symbol, base, ours, theirs string) (string, *Conflict) {
	switch {
	case ours == theirs, theirs == base:
		return ours, nil
	case ours == base:
		return theirs, nil
	}
	return ours, &Conflict{
		Symbol: symbol,
		Kind:   kindComment,
		Reason: fmt.Sprintf("Conflicting edits to the %s", strings.ReplaceAll(symbol, "-", " ")),
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

// resolveImport merges one import path: kept by both sides, it takes the name
// mergeImportName resolves; deleted by either side, it is dropped; added by one side, it
// is kept.
func resolveImport(path string, base, ours, theirs map[string]ImportItem) (*ImportItem, *Conflict) {
	b, inBase := base[path]
	o, inOurs := ours[path]
	t, inTheirs := theirs[path]
	switch {
	case inOurs && inTheirs:
		return mergeImportName(path, b, inBase, o, t)
	case inBase:
		return nil, nil // Omitted when deleted by either side.
	case inOurs:
		return &o, nil
	case inTheirs:
		return &t, nil
	}
	return nil, nil
}

// mergeImportName resolves the name an import both sides keep is written under, 3-way: the
// name both sides agree on, or the one side's where the other kept base's. Comparing ours
// with theirs alone reported renaming an import on one branch as a conflict with the
// branch that left it alone. A rename under code the other side added that still uses the
// old name is caught by the post-merge guard as a type error the merge introduces.
func mergeImportName(path string, b ImportItem, inBase bool, o, t ImportItem) (*ImportItem, *Conflict) {
	baseName := absentValue
	if inBase {
		baseName = b.Alias
	}
	if name, decided := expect3(baseName, o.Alias, t.Alias); decided {
		return &ImportItem{Path: path, Alias: name}, nil
	}
	return nil, &Conflict{
		Symbol: "import:" + path,
		Kind:   "import",
		Reason: fmt.Sprintf("Conflicting import aliases for %s: %q vs %q", path, o.Alias, t.Alias),
		Ours:   o.Alias,
		Theirs: t.Alias,
		Base:   b.Alias,
	}
}

// mergeDecls3Way resolves every item key of the three inputs and returns the items to
// render in merged order.
func mergeDecls3Way(base, ours, theirs *ParsedAST) ([]DeclItem, int, []Conflict) {
	dm := newDeclMerge(base, ours, theirs)
	allKeys := collectOrderedKeys(base, ours, theirs)
	var merged []DeclItem
	var conflicts []Conflict
	resolved := 0

	limit := len(allKeys)
	for i := 0; i < limit && i < 3*maxDeclItems; i++ {
		item, resCount, conflict := dm.resolve(allKeys[i])
		if conflict != nil {
			conflicts = append(conflicts, *conflict)
			continue
		}
		// A clean deletion resolves with no item and resCount == 1; counting resolved only
		// when an item is kept undercounted every such deletion (BUG-479), so the
		// accumulation applies to any non-conflicting result.
		resolved += resCount
		if item != nil {
			merged = append(merged, *item)
		}
	}

	return merged, resolved, conflicts
}

// declMerge resolves each item key across the three inputs. Its place maps hold, per
// ordered pair of inputs, where each spec sits in the first input relative to the second
// (see placesOf), so that moving a spec counts as changing it.
type declMerge struct {
	base, ours, theirs         *ParsedAST
	oursVsBase, baseVsOurs     map[string]string
	theirsVsBase, baseVsTheirs map[string]string
	oursVsTheirs, theirsVsOurs map[string]string
}

func newDeclMerge(base, ours, theirs *ParsedAST) declMerge {
	return declMerge{
		base: base, ours: ours, theirs: theirs,
		oursVsBase: placesOf(ours, base), baseVsOurs: placesOf(base, ours),
		theirsVsBase: placesOf(theirs, base), baseVsTheirs: placesOf(base, theirs),
		oursVsTheirs: placesOf(ours, theirs), theirsVsOurs: placesOf(theirs, ours),
	}
}

// placesOf maps the spec keys of a to where they sit in a: "top" for a spec at top level,
// or, for a spec in a parenthesized block that b also holds, the block-mates b also holds
// and the spec's rank among them in a's order. Two versions of a spec with the same place
// share their block and their index in it; a neighbour one version adds or drops does not
// move the spec, but moving it to another block, or past a block-mate, does.
func placesOf(a, b *ParsedAST) map[string]string {
	places := make(map[string]string, len(a.DeclOrder))
	for _, key := range a.DeclOrder {
		if item := a.Decls[key]; item.Block == "" && isSpecKind(item.Kind) {
			places[key] = "top"
		}
	}
	for _, keys := range a.Blocks {
		var mates []string
		for _, key := range keys {
			if _, held := b.Decls[key]; held && !isCommentKey(key) {
				mates = append(mates, key)
			}
		}
		members := strings.Join(slices.Sorted(slices.Values(mates)), "\x00")
		for rank, key := range mates {
			places[key] = "block\x00" + members + "\x00#" + strconv.Itoa(rank)
		}
	}
	return places
}

// isSpecKind reports whether an item kind is a const, var or type spec.
func isSpecKind(kind string) bool {
	return kind == token.CONST.String() || kind == token.VAR.String() || kind == token.TYPE.String()
}

// sameItem reports whether two versions of an item are the same: the same text under the
// same block doc, in the same kind of block, at the same place (see placesOf). Comparing
// the text alone let one side's move of a spec slip past the other side's edit or
// deletion of it.
func sameItem(a, b DeclItem, placeA, placeB string) bool {
	return a.Body == b.Body && a.Block == b.Block && a.Header == b.Header && placeA == placeB
}

// itemSource is the item's text as a conflict reports it.
func itemSource(d DeclItem) string {
	if d.Header == "" {
		return d.Body
	}
	return d.Header + "\n" + d.Body
}

// resolve merges one key: the version both sides agree on, the one side's change, a clean
// addition or deletion, or a conflict.
func (dm declMerge) resolve(key string) (*DeclItem, int, *Conflict) {
	b, inB := dm.base.Decls[key]
	o, inO := dm.ours.Decls[key]
	t, inT := dm.theirs.Decls[key]
	switch {
	case inO && inT:
		return dm.resolveModified(key, b, inB, o, t)
	case inO:
		return resolveKept(key, b, inB, o, sameItem(o, b, dm.oursVsBase[key], dm.baseVsOurs[key]), "ours", "theirs")
	case inT:
		return resolveKept(key, b, inB, t, sameItem(t, b, dm.theirsVsBase[key], dm.baseVsTheirs[key]), "theirs", "ours")
	default:
		return nil, 0, nil
	}
}

// resolveKept resolves a key only one side holds: that side's addition when base lacks
// it, a clean deletion by the other side when the holding side left it as base had it,
// and a conflict when the holding side changed or moved what the other side deleted.
func resolveKept(key string, b DeclItem, inB bool, kept DeclItem, unchanged bool, keptSide, deletedSide string) (*DeclItem, int, *Conflict) {
	if !inB {
		return &kept, 1, nil
	}
	if unchanged {
		return nil, 1, nil
	}
	conflict := &Conflict{
		Symbol: key,
		Kind:   kept.Kind,
		Reason: fmt.Sprintf("Conflict: symbol %s modified in %s but deleted in %s", key, keptSide, deletedSide),
		Base:   itemSource(b),
	}
	if keptSide == "ours" {
		conflict.Ours = itemSource(kept)
	} else {
		conflict.Theirs = itemSource(kept)
	}
	return nil, 0, conflict
}

func (dm declMerge) resolveModified(key string, b DeclItem, inB bool, o, t DeclItem) (*DeclItem, int, *Conflict) {
	if sameItem(o, t, dm.oursVsTheirs[key], dm.theirsVsOurs[key]) {
		return &o, 1, nil
	}
	if inB && sameItem(o, b, dm.oursVsBase[key], dm.baseVsOurs[key]) {
		return &t, 1, nil // Changed only in theirs
	}
	if inB && sameItem(t, b, dm.theirsVsBase[key], dm.baseVsTheirs[key]) {
		return &o, 1, nil // Changed only in ours
	}
	return nil, 0, &Conflict{
		Symbol: key,
		Kind:   o.Kind,
		Reason: fmt.Sprintf("Conflicting modifications to declaration %s", key),
		Ours:   itemSource(o),
		Theirs: itemSource(t),
		Base:   itemSource(b),
	}
}

// collectOrderedKeys orders the union of the three inputs' keys after the side that
// reordered the declarations both sides kept, with the other side's additions placed
// after the key that precedes them on that side. Starting from base's order dropped one
// side's move of a declaration, which changed the order variable initializers run in.
// When both sides reorder, base's order leads and the guard rejects any initialization
// order the result gets wrong.
func collectOrderedKeys(base, ours, theirs *ParsedAST) []string {
	basePos, oursPos, theirsPos := indexOf(base.DeclOrder), indexOf(ours.DeclOrder), indexOf(theirs.DeclOrder)
	shared := heldBy(base.DeclOrder, oursPos, theirsPos)
	baseOrder := orderOf(basePos, shared)
	oursKept := slices.Equal(orderOf(oursPos, shared), baseOrder)
	theirsKept := slices.Equal(orderOf(theirsPos, shared), baseOrder)
	switch {
	case theirsKept:
		return interleaveKeys(interleaveKeys(nil, ours.DeclOrder), theirs.DeclOrder)
	case oursKept:
		return interleaveKeys(interleaveKeys(nil, theirs.DeclOrder), ours.DeclOrder)
	}
	order := interleaveKeys(nil, base.DeclOrder)
	order = interleaveKeys(order, ours.DeclOrder)
	return interleaveKeys(order, theirs.DeclOrder)
}

// interleaveKeys inserts the keys of side that order lacks. A run of new keys goes right
// after the latest key order places among those side holds before the run, past any keys
// side does not hold (the other side's additions or this side's deletions), so a side's
// additions precede the other's at the same place. Anchoring on the nearest preceding key
// instead put an addition before a key side had placed ahead of it whenever order moved
// that key back. A run after the last key both hold goes to the end: where order is a
// reordering of side, its last shared key may stand mid-order, and inserting there would
// shift the position of every key order places after it.
func interleaveKeys(order, side []string) []string {
	index := indexOf(order)
	inSide := make(map[string]bool, len(side))
	last := -1
	for i, key := range side {
		inSide[key] = true
		if _, ok := index[key]; ok {
			last = i
		}
	}
	inserts := make(map[int][]string)
	anchor := -1
	for i, key := range side {
		if at, ok := index[key]; ok {
			anchor = max(anchor, at)
			continue
		}
		slot := len(order)
		if i < last {
			slot = insertSlot(order, inSide, anchor)
		}
		inserts[slot] = append(inserts[slot], key)
	}
	merged := make([]string, 0, len(order)+len(side))
	for i, key := range order {
		merged = append(merged, inserts[i]...)
		merged = append(merged, key)
	}
	return append(merged, inserts[len(order)]...)
}

// insertSlot returns the position after anchor past every key side does not hold.
func insertSlot(order []string, inSide map[string]bool, anchor int) int {
	slot := anchor + 1
	for slot < len(order) && !inSide[order[slot]] {
		slot++
	}
	return slot
}

// indexOf maps each key to its position in keys.
func indexOf(keys []string) map[string]int {
	index := make(map[string]int, len(keys))
	for i, key := range keys {
		index[key] = i
	}
	return index
}

func renderGoCode(pkgName string, header fileHeader, imports []ImportItem, decls []string) (string, error) {
	if pkgName == "" {
		pkgName = "main"
	}

	var b strings.Builder
	// The build-constraint comment must precede the package doc comment by a blank line
	// (Go's own convention for //go:build), and the doc comment must directly precede the
	// package clause with no blank line, or go/parser would stop treating it as the
	// package's doc comment on a later merge (BUG-214). Leading comments such as a license
	// header follow the constraint: the go command honours a //go:build line only when
	// blank lines and line comments alone precede it, so a block-comment header placed
	// first would disable it.
	for _, text := range []string{header.buildConstraints, header.leadingComments} {
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}
	if header.packageDoc != "" {
		b.WriteString(header.packageDoc)
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
