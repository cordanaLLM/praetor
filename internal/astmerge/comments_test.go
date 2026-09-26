package astmerge

import (
	"strings"
	"testing"
)

// requireOrder fails unless every text occurs in code exactly once, in the given order.
func requireOrder(t *testing.T, code string, texts ...string) {
	t.Helper()
	last := -1
	for _, text := range texts {
		if strings.Count(code, text) != 1 {
			t.Fatalf("expected %q exactly once in:\n%s", text, code)
		}
		at := strings.Index(code, text)
		if at < last {
			t.Fatalf("expected %q after %q in:\n%s", text, texts, code)
		}
		last = at
	}
}

// TestMerge_Positive_BlockCommentKeepsItsPlace: a free comment between two specs of a block
// stays there, indented, when neither side touches the block. Ranking only the block's
// specs left the comment at its own merged position, so it landed after the last spec at
// column 1.
func TestMerge_Positive_BlockCommentKeepsItsPlace(t *testing.T) {
	base := "package p\n\nconst (\n\tA = 1\n\t// Deprecated values below.\n\n\tB = 2\n\tC = 3\n)\n"
	code, _ := mergeClean(t, base, base+"\nfunc Ours() {}\n", base+"\nfunc Theirs() {}\n")
	requireOrder(t, code, "A = 1", "\t// Deprecated values below.\n", "B = 2", "C = 3", "func Ours()", "func Theirs()")
}

// TestMerge_Positive_BlockCommentsFollowTheirAnchors: ours deletes the spec a comment
// follows and theirs appends a spec before the block's closing comment. Each comment
// renders next to the nearest block-mate the merge keeps, on the side that moved it.
func TestMerge_Positive_BlockCommentsFollowTheirAnchors(t *testing.T) {
	base := "package p\n\nconst (\n\t// leading note\n\n\tA = 1\n\t// between A and B\n\n\tB = 2\n\t// trailing note\n)\n"
	ours := strings.Replace(base, "\tA = 1\n", "", 1)
	theirs := strings.Replace(base, "\tB = 2\n", "\tB = 2\n\tC = 3\n", 1)
	code, _ := mergeClean(t, base, ours, theirs)
	requireOrder(t, code, "// leading note", "// between A and B", "B = 2", "C = 3", "// trailing note", ")")
	if strings.Contains(code, "A = 1") {
		t.Errorf("ours deleted A:\n%s", code)
	}
}

// TestMerge_Boundary_BlockCommentStaysInItsOwnBlock: a comment leading the second of two
// adjacent blocks of one keyword stays in that block, and a comment none of whose
// block-mates survives keeps its own place in an otherwise empty block.
func TestMerge_Boundary_BlockCommentStaysInItsOwnBlock(t *testing.T) {
	base := "package p\n\nconst (\n\tA = 1\n\tB = 2\n)\n\nconst (\n\t// second block\n\n\tC = 3\n)\n"
	code, _ := mergeClean(t, base, base+"\nfunc Ours() {}\n", base+"\nfunc Theirs() {}\n")
	requireOrder(t, code, "B = 2", ")\n\nconst (\n\t// second block", "C = 3")

	lone := "package p\n\nconst (\n\tA = 1\n\t// lone note\n\n\tB = 2\n)\n"
	ours := "package p\n\nconst (\n\t// lone note\n)\n"
	code, _ = mergeClean(t, lone, ours, lone+"\nfunc Theirs() {}\n")
	// gofmt writes the comment of a block with no spec at column 1.
	requireOrder(t, code, "const (\n", "// lone note\n)", "func Theirs()")
}

// TestMerge_Negative_DivergentCommentEditsConflict: both sides rewriting one free comment
// differently conflict, at top level and inside a block, and so does one side editing a
// comment the other deletes. Keying free comments by their text alone turned each edit
// into a deletion plus an addition, so both texts merged clean: two //go:generate lines
// that each ran a generator neither side wrote.
func TestMerge_Negative_DivergentCommentEditsConflict(t *testing.T) {
	gen := "package p\n\n//go:generate stringer -type=A\n\ntype A int\n"
	res := requireConflict(t, gen,
		strings.Replace(gen, "-type=A", "-type=B", 1),
		strings.Replace(gen, "-type=A", "-type=C", 1),
		"comment://go:generate stringer -type=A")
	for _, c := range res.Conflicts {
		if c.Kind == kindComment && (c.Ours != "//go:generate stringer -type=B" || c.Theirs != "//go:generate stringer -type=C" || c.Base != "//go:generate stringer -type=A") {
			t.Errorf("expected each side's and base's comment, got %+v", c)
		}
	}
	block := "package p\n\nconst (\n\tA = 1\n\t// note\n\n\tB = 2\n)\n"
	requireConflict(t, block,
		strings.Replace(block, "// note", "// note ours", 1),
		strings.Replace(block, "// note", "// note theirs", 1),
		"comment:// note")
	requireConflict(t, gen,
		strings.Replace(gen, "-type=A", "-type=B", 1),
		strings.Replace(gen, "//go:generate stringer -type=A\n\n", "", 1),
		"comment://go:generate stringer -type=A")
}

// TestMerge_Negative_DivergentEditsOfARepeatedComment: comments repeating one text are
// told apart by place, not by their numbered keys, which shift when an earlier copy is
// edited.
func TestMerge_Negative_DivergentEditsOfARepeatedComment(t *testing.T) {
	base := "package p\n\n// ---\n\nfunc A() {}\n\n// ---\n\nfunc B() {}\n"
	requireConflict(t, base,
		strings.Replace(base, "// ---", "// ===", 1),
		strings.Replace(base, "// ---", "// +++", 1),
		"comment:// ---")
}

// TestMerge_Positive_CommentEditsThatAgreeMerge: one side's edit, identical edits on both
// sides, a deletion on both sides and independent additions all merge clean.
func TestMerge_Positive_CommentEditsThatAgreeMerge(t *testing.T) {
	gen := "package p\n\n//go:generate stringer -type=A\n\ntype A int\n"
	edited := strings.Replace(gen, "-type=A", "-type=B", 1)
	code, _ := mergeClean(t, gen, edited, gen+"\nfunc F() {}\n")
	requireOrder(t, code, "//go:generate stringer -type=B", "type A int", "func F()")
	if strings.Contains(code, "-type=A") {
		t.Errorf("ours replaced the directive:\n%s", code)
	}
	code, _ = mergeClean(t, gen, edited, edited+"\nfunc F() {}\n")
	requireOrder(t, code, "//go:generate stringer -type=B", "type A int", "func F()")
	deleted := strings.Replace(gen, "//go:generate stringer -type=A\n\n", "", 1)
	code, _ = mergeClean(t, gen, deleted, deleted+"\nfunc F() {}\n")
	if strings.Contains(code, "go:generate") {
		t.Errorf("both sides deleted the directive:\n%s", code)
	}
	code, _ = mergeClean(t, gen,
		strings.Replace(gen, "type A int", "// ours note\n\ntype A int", 1),
		strings.Replace(gen, "type A int", "// theirs note\n\ntype A int", 1))
	requireOrder(t, code, "//go:generate stringer -type=A", "// ours note", "// theirs note", "type A int")
}

// TestMerge_Boundary_CommentEditBesideChangedDeclarations: the stretch a comment belongs to
// widens past declarations one side deletes, and a side that reorders the declarations
// around a comment it keeps leaves the other side's edit of it.
func TestMerge_Boundary_CommentEditBesideChangedDeclarations(t *testing.T) {
	base := "package p\n\n// about A\n\nfunc A() {}\n\nfunc B() {}\n"
	edited := strings.Replace(base, "// about A", "// about A, edited", 1)
	code, _ := mergeClean(t, base, edited, strings.Replace(base, "func A() {}\n\n", "", 1))
	requireOrder(t, code, "// about A, edited", "func B()")

	around := "package p\n\nfunc A() {}\n\n// between\n\nfunc B() {}\n"
	swapped := "package p\n\nfunc B() {}\n\nfunc A() {}\n\n// between\n"
	code, _ = mergeClean(t, around, swapped, strings.Replace(around, "// between", "// between, edited", 1))
	if !strings.Contains(code, "// between, edited") || strings.Contains(code, "// between\n") {
		t.Errorf("expected theirs's edit of the comment ours only moved:\n%s", code)
	}
}
