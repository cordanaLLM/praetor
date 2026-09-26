package astmerge

import (
	"go/types"
	"strings"
	"testing"
)

const orderBase = `package reorder

const (
	A = iota
	B
	C
)
`

// swapBC reorders orderBase's block so C takes position 1 and B position 2.
var swapBC = strings.Replace(orderBase, "\tB\n\tC\n", "\tC\n\tB\n", 1)

// TestMerge_Positive_ReorderedBlockKeepsTheOrder: the merged order always started from
// base's, so a side's reordering of a block was dropped and the merge reported clean with
// B = 1, C = 2 instead of the reordering side's C = 1, B = 2.
func TestMerge_Positive_ReorderedBlockKeepsTheOrder(t *testing.T) {
	mergeEitherWay(t, orderBase, swapBC, func(t *testing.T, code string, pkg *types.Package) {
		requireBlocks(t, code, [][]string{{"A", "C", "B"}})
		requireValues(t, code, pkg, map[string]int64{"A": 0, "C": 1, "B": 2})
	})
}

// TestMerge_Negative_ReorderPlusAdditionInAnIotaBlockConflicts: once both sides change a
// block whose values follow position, the block merges as one unit. Interleaving theirs'
// E (2 on theirs) into ours' reordering gave it 3, a value no side wrote.
func TestMerge_Negative_ReorderPlusAdditionInAnIotaBlockConflicts(t *testing.T) {
	appendD := strings.Replace(orderBase, "\tC\n", "\tC\n\tD\n", 1)
	insertE := strings.Replace(orderBase, "\tB\n", "\tB\n\tE\n", 1)
	for name, added := range map[string]string{"appended": appendD, "inserted": insertE} {
		t.Run(name, func(t *testing.T) {
			requireConflict(t, orderBase, swapBC, added, "block:const:A")
			requireConflict(t, orderBase, added, swapBC, "block:const:A")
		})
	}
}

// TestMerge_Positive_ReorderPlusAdditionInAnExplicitBlock: a block whose specs carry their
// own values does not merge as one unit, since no value follows position: one side's
// reordering and the other side's addition combine, the addition after the spec it
// follows on its side.
func TestMerge_Positive_ReorderPlusAdditionInAnExplicitBlock(t *testing.T) {
	const base = "package reorder\n\nconst (\n\tA = 0\n\tB = 1\n\tC = 2\n)\n"
	swapped := strings.Replace(base, "\tB = 1\n\tC = 2\n", "\tC = 2\n\tB = 1\n", 1)
	added := strings.Replace(base, "\tB = 1\n", "\tB = 1\n\tE = 5\n", 1)
	for name, sides := range map[string][2]string{
		"ours reorders":   {swapped, added},
		"theirs reorders": {added, swapped},
	} {
		t.Run(name, func(t *testing.T) {
			code, pkg := mergeClean(t, base, sides[0], sides[1])
			requireBlocks(t, code, [][]string{{"A", "C", "B", "E"}})
			requireValues(t, code, pkg, map[string]int64{"A": 0, "B": 1, "C": 2, "E": 5})
		})
	}
}

// TestMerge_Positive_SpecMovedToTheEndOfTheNextBlock: moving a block's first spec to the
// end of the next block is a reordering and a regrouping by one side; rendering the moved
// spec at its base position left it alone in a block with no value.
func TestMerge_Positive_SpecMovedToTheEndOfTheNextBlock(t *testing.T) {
	moved := `package regroup

const (
	B = iota
)

const (
	C = iota
	D
	A
)
`
	mergeEitherWay(t, twoBlockBase, moved, func(t *testing.T, code string, pkg *types.Package) {
		requireBlocks(t, code, [][]string{{"B"}, {"C", "D", "A"}})
		requireValues(t, code, pkg, map[string]int64{"B": 0, "C": 0, "D": 1, "A": 2})
	})
}

// TestMerge_Negative_ConflictingReordersConflict: two different reorderings of one block,
// one side reordering a block the other regrouped, and one spec both sides add at
// different places each leave no order that keeps both sides' values.
func TestMerge_Negative_ConflictingReordersConflict(t *testing.T) {
	swapAB := strings.Replace(orderBase, "\tA = iota\n\tB\n", "\tB = iota\n\tA\n", 1)
	splitAfterA := strings.Replace(orderBase, "\tA = iota\n\tB\n", "\tA = iota\n)\n\nconst (\n\tB = iota + 1\n", 1)
	cases := []struct {
		name, ours, theirs string
	}{
		{"both reorder differently", swapBC, swapAB},
		{"ours reorders, theirs regroups", swapBC, splitAfterA},
		{"theirs reorders, ours regroups", splitAfterA, swapBC},
		{"same spec added at different places",
			strings.Replace(orderBase, "\tB\n", "\tX\n\tB\n", 1),
			strings.Replace(orderBase, "\tC\n", "\tC\n\tX\n", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := requireConflict(t, orderBase, tc.ours, tc.theirs, "block:const:A")
			for _, c := range res.Conflicts {
				if c.Symbol == "block:const:A" && (c.Kind != kindBlock || c.Ours == c.Theirs || c.Base == "") {
					t.Errorf("the order conflict must name the block and show every side's order: %+v", c)
				}
			}
		})
	}
}

// TestMerge_Boundary_AgreedOrderStaysClean covers the edges of the order leader: both
// sides making the same reordering agree, and an explicit-valued block neither side
// reorders keeps base's order while both sides add to it. The same two additions to an
// iota block conflict: each shifts the value of the specs after it.
func TestMerge_Boundary_AgreedOrderStaysClean(t *testing.T) {
	code, pkg := mergeClean(t, orderBase, swapBC+"\nfunc Ours() {}\n", swapBC+"\nfunc Theirs() {}\n")
	requireBlocks(t, code, [][]string{{"A", "C", "B"}})
	requireValues(t, code, pkg, map[string]int64{"C": 1, "B": 2})

	const explicit = "package reorder\n\nconst (\n\tA = 0\n\tB = 1\n\tC = 2\n)\n"
	ours := strings.Replace(explicit, "\tA = 0\n", "\tA = 0\n\tX = 7\n", 1)
	theirs := strings.Replace(explicit, "\tC = 2\n", "\tC = 2\n\tY = 8\n", 1)
	code, pkg = mergeClean(t, explicit, ours, theirs)
	requireBlocks(t, code, [][]string{{"A", "X", "B", "C", "Y"}})
	requireValues(t, code, pkg, map[string]int64{"X": 7, "B": 1, "C": 2, "Y": 8})

	ours = strings.Replace(orderBase, "\tA = iota\n", "\tA = iota\n\tX\n", 1)
	theirs = strings.Replace(orderBase, "\tC\n", "\tC\n\tY\n", 1)
	requireConflict(t, orderBase, ours, theirs, "block:const:A")
}
