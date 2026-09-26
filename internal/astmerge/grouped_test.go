package astmerge

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"
)

// mergeClean merges and fails the test unless the merge is clean and the result
// type-checks; it returns the merged code and its type-checked package.
func mergeClean(t *testing.T, base, ours, theirs string) (string, *types.Package) {
	t.Helper()
	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if !res.Clean {
		t.Fatalf("expected a clean merge, got conflicts: %+v", res.Conflicts)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "merged.go", res.MergedCode, parser.ParseComments)
	if err != nil {
		t.Fatalf("merged code failed to parse: %v\n%s", err, res.MergedCode)
	}
	conf := types.Config{Importer: importer.Default()}
	pkg, err := conf.Check(file.Name.Name, fset, []*ast.File{file}, nil)
	if err != nil {
		t.Fatalf("merged code failed type-checking: %v\n%s", err, res.MergedCode)
	}
	return res.MergedCode, pkg
}

// constValue returns the value of a package-level constant as an int64.
func constValue(t *testing.T, pkg *types.Package, name string) int64 {
	t.Helper()
	obj, ok := pkg.Scope().Lookup(name).(*types.Const)
	if !ok {
		t.Fatalf("constant %s not declared in the merged package", name)
	}
	value, exact := constant.Int64Val(obj.Val())
	if !exact {
		t.Fatalf("constant %s is not an exact integer: %v", name, obj.Val())
	}
	return value
}

// requireConflict fails unless the merge is unclean with a conflict on symbol.
func requireConflict(t *testing.T, base, ours, theirs, symbol string) *MergeResult {
	t.Helper()
	res, err := Merge(base, ours, theirs)
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if res.Clean {
		t.Fatalf("expected a conflict on %s, got a clean merge:\n%s", symbol, res.MergedCode)
	}
	for _, c := range res.Conflicts {
		if c.Symbol == symbol {
			return res
		}
	}
	t.Fatalf("expected a conflict on %s, got %+v", symbol, res.Conflicts)
	return nil
}

const groupedBase = `package grouped

// Limits bound the work.
const (
	MaxItems = 10
	MaxBytes = 20
)

func Use() int { return MaxItems + MaxBytes }
`

// TestMerge_Positive_GroupedBlockRendersOnce is BUG-213: every name of a grouped block
// carried the whole block, so an unrelated addition rendered the block once per name.
func TestMerge_Positive_GroupedBlockRendersOnce(t *testing.T) {
	ours := groupedBase + "\nfunc Ours() {}\n"
	theirs := groupedBase + "\nfunc Theirs() {}\n"
	code, _ := mergeClean(t, groupedBase, ours, theirs)
	if got := strings.Count(code, "const ("); got != 1 {
		t.Errorf("expected the const block once, got %d:\n%s", got, code)
	}
	if got := strings.Count(code, "// Limits bound the work."); got != 1 {
		t.Errorf("expected the block doc once, got %d:\n%s", got, code)
	}
}

// TestMerge_Positive_PerSpecEditsMergeClean is BUG-213's false conflict: an edit to one spec
// on each side is two independent changes, not a conflict on both names.
func TestMerge_Positive_PerSpecEditsMergeClean(t *testing.T) {
	ours := strings.Replace(groupedBase, "MaxItems = 10", "MaxItems = 11", 1)
	theirs := strings.Replace(groupedBase, "MaxBytes = 20", "MaxBytes = 21", 1)
	code, pkg := mergeClean(t, groupedBase, ours, theirs)
	if constValue(t, pkg, "MaxItems") != 11 || constValue(t, pkg, "MaxBytes") != 21 {
		t.Errorf("expected both spec edits in the merged block:\n%s", code)
	}
	if strings.Count(code, "const (") != 1 {
		t.Errorf("expected one const block:\n%s", code)
	}
}

const colorBase = "package color\n\ntype Color int\n\nconst (\n\tRed Color = iota\n\tGreen\n)\n\nfunc Name() string { return \"\" }\n"

// TestMerge_Positive_SpecAddedInsideIotaBlockKeepsItsValue places an added spec next to its
// neighbour: appended after every other declaration, it left the block and lost its iota.
func TestMerge_Positive_SpecAddedInsideIotaBlockKeepsItsValue(t *testing.T) {
	ours := strings.Replace(colorBase, "\tGreen\n", "\tGreen\n\tBlue\n", 1)
	theirs := strings.Replace(colorBase, "func Name()", "func Other() {}\n\nfunc Name()", 1)
	code, pkg := mergeClean(t, colorBase, ours, theirs)
	for name, want := range map[string]int64{"Red": 0, "Green": 1, "Blue": 2} {
		if got := constValue(t, pkg, name); got != want {
			t.Errorf("%s = %d, want %d:\n%s", name, got, want, code)
		}
	}
}

// TestMerge_Negative_BothSidesChangingAnIotaBlockConflict: iota counts positions, so a spec
// one side inserts shifts the value of every spec after it. Merged per spec, ours' Blue
// (2 on ours) became 3 once theirs' Black joined the front; the block now merges as one
// unit once both sides change it, and the guard rejects the value independently.
func TestMerge_Negative_BothSidesChangingAnIotaBlockConflict(t *testing.T) {
	ours := strings.Replace(colorBase, "\tGreen\n", "\tGreen\n\tBlue\n", 1)
	theirs := strings.Replace(colorBase, "\tRed Color = iota\n", "\tBlack Color = iota\n\tRed\n", 1)
	res := requireConflict(t, colorBase, ours, theirs, "block:const:Red")
	if res.MergedCode != "" {
		t.Errorf("a conflicted merge must not carry merged code:\n%s", res.MergedCode)
	}
}

// TestMerge_Positive_FreeFloatingCommentsSurvive is BUG-214: comments no declaration owns
// were never captured, so every merge dropped them.
func TestMerge_Positive_FreeFloatingCommentsSurvive(t *testing.T) {
	base := `// Copyright 2026 Example Authors.

//go:build linux

// Package notes keeps its comments.
package notes

// --- configuration ---

const (
	A = 1
	// between specs, owned by nothing

	B = 2
)

func F() {} // trailing note

// closing remark
`
	ours := strings.Replace(base, "func F() {}", "func F() {}\n\nfunc Ours() {}", 1)
	theirs := strings.Replace(base, "\tB = 2\n", "\tB = 3\n", 1)
	code, _ := mergeClean(t, base, ours, theirs)
	for _, want := range []string{
		"// Copyright 2026 Example Authors.", "//go:build linux", "// Package notes keeps its comments.",
		"// --- configuration ---", "// between specs, owned by nothing", "// trailing note",
		"// closing remark", "B = 3", "func Ours()",
	} {
		if strings.Count(code, want) != 1 {
			t.Errorf("expected %q exactly once in:\n%s", want, code)
		}
	}
	between := strings.Index(code, "\t// between specs")
	if a, b := strings.Index(code, "A = 1"), strings.Index(code, "B = 3"); between < a || between > b {
		t.Errorf("the comment between specs must stay between them, indented:\n%s", code)
	}
	reparsed, err := parseSourceSafe("merged.go", code)
	if err != nil {
		t.Fatalf("merged code failed to re-parse: %v", err)
	}
	if item, ok := reparsed.Decls["comment:// between specs, owned by nothing"]; !ok || item.Block != "const" {
		t.Errorf("the comment between specs must stay a free comment of the block on re-parse: %+v", reparsed.Decls)
	}
	if strings.Index(code, "//go:build") > strings.Index(code, "// Copyright") {
		t.Errorf("the build constraint must precede the license header:\n%s", code)
	}
}

// TestMerge_Negative_DivergentHeaderEditsConflict is #392: both sides changing the build
// constraint or the package doc differently merged clean with ours silently kept.
func TestMerge_Negative_DivergentHeaderEditsConflict(t *testing.T) {
	const template = "%s\n\n%spackage aux\n\nfunc F() {}\n%s"
	base := fmt.Sprintf(template, "//go:build linux", "// Package aux is the base.\n", "")
	requireConflict(t, base,
		fmt.Sprintf(template, "//go:build darwin", "// Package aux is the base.\n", ""),
		fmt.Sprintf(template, "//go:build windows", "// Package aux is the base.\n", "\nfunc G() {}\n"),
		"build-constraints")
	requireConflict(t, base,
		fmt.Sprintf(template, "//go:build linux", "// Package aux is ours.\n", ""),
		fmt.Sprintf(template, "//go:build linux", "// Package aux is theirs.\n", "\nfunc G() {}\n"),
		"package-doc")
	requireConflict(t, "// License A.\n\n"+base,
		"// License B.\n\n"+base,
		"// License C.\n\n"+base+"\nfunc G() {}\n",
		"leading-comments")
}

// TestMerge_Negative_Issue392FixtureReportsBothHeaderConflicts replays #392's fixture: both
// sides rewrite both the build constraint and the package doc. Each field conflicts on its
// own and carries the base, ours and theirs text for resolution.
func TestMerge_Negative_Issue392FixtureReportsBothHeaderConflicts(t *testing.T) {
	const template = "//go:build %s\n\n// Package demo is the %s package.\npackage demo\n\nfunc Keep() {}\n"
	res, err := Merge(fmt.Sprintf(template, "linux", "base"), fmt.Sprintf(template, "darwin", "ours"), fmt.Sprintf(template, "windows", "theirs"))
	if err != nil {
		t.Fatalf("Merge failed: %v", err)
	}
	if res.Clean || len(res.Conflicts) != 2 {
		t.Fatalf("expected two header conflicts, got clean=%v %+v", res.Clean, res.Conflicts)
	}
	want := map[string][3]string{
		"build-constraints": {"//go:build linux", "//go:build darwin", "//go:build windows"},
		"package-doc":       {"// Package demo is the base package.", "// Package demo is the ours package.", "// Package demo is the theirs package."},
	}
	for _, c := range res.Conflicts {
		if texts, ok := want[c.Symbol]; !ok || c.Base != texts[0] || c.Ours != texts[1] || c.Theirs != texts[2] {
			t.Errorf("unexpected conflict %+v", c)
		}
	}
}

// TestMerge_Positive_EqualAndOneSidedHeaderEditsMergeClean is #392's clean half: for the
// build constraint, the package doc and a license header alike, an edit both sides make
// alike and an edit only one side makes carry through; base's text does not survive.
func TestMerge_Positive_EqualAndOneSidedHeaderEditsMergeClean(t *testing.T) {
	const template = "%s\n\n//go:build %s\n\n// Package aux is %s.\npackage aux\n\nfunc F() {}\n"
	base := fmt.Sprintf(template, "// License A.", "linux", "the base")
	fields := map[string][2]string{
		"build-constraints": {"//go:build linux", "//go:build darwin"},
		"package-doc":       {"// Package aux is the base.", "// Package aux is changed."},
		"leading-comments":  {"// License A.", "// License B."},
	}
	for field, texts := range fields {
		changed := strings.Replace(base, texts[0], texts[1], 1)
		for name, sides := range map[string][2]string{
			"equal":       {changed, changed + "\nfunc G() {}\n"},
			"ours only":   {changed, base + "\nfunc G() {}\n"},
			"theirs only": {base + "\nfunc G() {}\n", changed},
		} {
			t.Run(field+"/"+name, func(t *testing.T) {
				code, _ := mergeClean(t, base, sides[0], sides[1])
				if !strings.Contains(code, texts[1]) || strings.Contains(code, texts[0]) {
					t.Errorf("expected %q to replace %q:\n%s", texts[1], texts[0], code)
				}
			})
		}
	}
}

// TestMerge_Negative_SameSpecEditedTwiceConflictsAlone keeps real conflicts: both sides
// editing one spec conflict on that spec only, not on the untouched names of its block.
func TestMerge_Negative_SameSpecEditedTwiceConflictsAlone(t *testing.T) {
	ours := strings.Replace(groupedBase, "MaxItems = 10", "MaxItems = 11", 1)
	theirs := strings.Replace(groupedBase, "MaxItems = 10", "MaxItems = 12", 1)
	res := requireConflict(t, groupedBase, ours, theirs, "const:MaxItems")
	if len(res.Conflicts) != 1 {
		t.Errorf("expected exactly one conflict, got %+v", res.Conflicts)
	}
}

// TestMerge_Negative_DivergentBlockDocConflicts treats the block doc as part of the block:
// two different rewrites of it conflict rather than one silently winning.
func TestMerge_Negative_DivergentBlockDocConflicts(t *testing.T) {
	ours := strings.Replace(groupedBase, "// Limits bound the work.", "// Limits cap the work.", 1)
	theirs := strings.Replace(groupedBase, "// Limits bound the work.", "// Limits restrict the work.", 1)
	requireConflict(t, groupedBase, ours, theirs, "const:MaxItems")

	oneSide := strings.Replace(groupedBase, "// Limits bound the work.", "// Limits cap the work.", 1)
	code, _ := mergeClean(t, groupedBase, oneSide, groupedBase+"\nfunc G() {}\n")
	if !strings.Contains(code, "// Limits cap the work.") || strings.Contains(code, "bound the work") {
		t.Errorf("a one-sided block doc edit must carry through:\n%s", code)
	}
}

// TestMerge_Boundary_RepeatedAndMultiNameSpecsSurvive covers keys that recur in one file:
// several init functions and blank declarations each render once, and a spec declaring two
// names renders once rather than once per name.
func TestMerge_Boundary_RepeatedAndMultiNameSpecsSurvive(t *testing.T) {
	base := `package repeat

var first, second = 1, 2

var _ = first

var _ = second

func init() { first++ }

func init() { second++ }
`
	ours := base + "\nfunc Ours() {}\n"
	theirs := strings.Replace(base, "func init() { second++ }", "func init() { second += 2 }", 1)
	code, _ := mergeClean(t, base, ours, theirs)
	for want, count := range map[string]int{
		"var first, second = 1, 2": 1, "var _ = first": 1, "var _ = second": 1,
		"func init() { first++ }": 1, "func init() { second += 2 }": 1, "func init()": 2,
	} {
		if got := strings.Count(code, want); got != count {
			t.Errorf("expected %q %d times, got %d:\n%s", want, count, got, code)
		}
	}
}

// TestMerge_Boundary_BlockShapes covers a single-spec parenthesized block, an empty block
// whose doc must survive, and a spec moved from top level into a block by one side.
func TestMerge_Boundary_BlockShapes(t *testing.T) {
	base := "package shapes\n\nconst Solo = 1\n\n// Nothing yet.\nvar ()\n\ntype (\n\tOnly int\n)\n"
	ours := strings.Replace(base, "const Solo = 1", "const (\n\tSolo = 1\n\tDuo  = 2\n)", 1)
	theirs := base + "\nfunc F() {}\n"
	code, pkg := mergeClean(t, base, ours, theirs)
	if constValue(t, pkg, "Duo") != 2 || strings.Count(code, "const (") != 1 {
		t.Errorf("expected Solo and Duo in one const block:\n%s", code)
	}
	if !strings.Contains(code, "// Nothing yet.") || !strings.Contains(code, "type (\n\tOnly int\n)") {
		t.Errorf("expected the empty block's doc and the single-spec block:\n%s", code)
	}
}

// TestMerge_Boundary_DeclItemBound errors at one item over maxDeclItems and accepts exactly
// maxDeclItems, counting block specs rather than top-level declarations.
func TestMerge_Boundary_DeclItemBound(t *testing.T) {
	block := func(specs int) string {
		var b strings.Builder
		b.WriteString("package big\n\nconst (\n")
		for i := 0; i < specs; i++ {
			fmt.Fprintf(&b, "\tC%d = %d\n", i, i)
		}
		b.WriteString(")\n")
		return b.String()
	}
	parsed, err := parseSourceSafe("exact.go", block(maxDeclItems))
	if err != nil {
		t.Fatalf("exactly %d items must be accepted: %v", maxDeclItems, err)
	}
	if len(parsed.DeclOrder) != maxDeclItems || len(parsed.Blocks) != 1 {
		t.Errorf("expected %d items in one block, got %d in %d", maxDeclItems, len(parsed.DeclOrder), len(parsed.Blocks))
	}
	if _, err := parseSourceSafe("over.go", block(maxDeclItems+1)); err == nil {
		t.Fatalf("expected an error one item over the %d bound", maxDeclItems)
	}
}
