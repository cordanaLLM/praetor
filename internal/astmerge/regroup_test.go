package astmerge

import (
	"go/types"
	"reflect"
	"strings"
	"testing"
)

// blockNames returns the spec names each parenthesized declaration of code holds, in order.
func blockNames(t *testing.T, code string) [][]string {
	t.Helper()
	parsed, err := parseSourceSafe("merged.go", code)
	if err != nil {
		t.Fatalf("merged code failed to re-parse: %v\n%s", err, code)
	}
	var blocks [][]string
	for _, keys := range parsed.Blocks {
		var names []string
		for _, key := range keys {
			if item := parsed.Decls[key]; item.Kind != kindComment {
				names = append(names, item.Name)
			}
		}
		blocks = append(blocks, names)
	}
	return blocks
}

// requireBlocks fails unless the merged code groups its specs exactly as want.
func requireBlocks(t *testing.T, code string, want [][]string) {
	t.Helper()
	if got := blockNames(t, code); !reflect.DeepEqual(got, want) {
		t.Errorf("block grouping = %v, want %v:\n%s", got, want, code)
	}
}

// requireValues fails unless every named constant of pkg holds its wanted value.
func requireValues(t *testing.T, code string, pkg *types.Package, want map[string]int64) {
	t.Helper()
	for name, value := range want {
		if v := constValue(t, pkg, name); v != value {
			t.Errorf("%s = %d, want %d:\n%s", name, v, value, code)
		}
	}
}

// mergeEitherWay runs a merge where one side makes the change and the other only adds an
// unrelated function, once with ours changing and once with theirs changing, and hands
// each clean result to check.
func mergeEitherWay(t *testing.T, base, changed string, check func(t *testing.T, code string, pkg *types.Package)) {
	t.Helper()
	unrelated := base + "\nfunc Unrelated() {}\n"
	for name, sides := range map[string][2]string{
		"ours changes":   {changed, unrelated},
		"theirs changes": {unrelated, changed},
	} {
		t.Run(name, func(t *testing.T) {
			code, pkg := mergeClean(t, base, sides[0], sides[1])
			check(t, code, pkg)
		})
	}
}

const iotaBlockBase = `package regroup

const (
	A = iota
	B
	C
	D
)
`

// splitAfterB splits iotaBlockBase into two blocks that keep every value.
var splitAfterB = strings.Replace(iotaBlockBase, "\tB\n\tC\n", "\tB\n)\n\nconst (\n\tC = iota + 2\n", 1)

// TestMerge_Positive_SplitBlockKeepsTheSplit: linking the specs of every input's blocks
// re-joined a block one side split, so C = iota + 2 moved to index 2 of the joined block
// and evaluated to 4 instead of 2, and the second block's doc was hoisted over the whole.
func TestMerge_Positive_SplitBlockKeepsTheSplit(t *testing.T) {
	split := `package regroup

const (
	A = iota
	B
)

// Upper half.
const (
	C = iota + 2
	D
)
`
	mergeEitherWay(t, iotaBlockBase, split, func(t *testing.T, code string, pkg *types.Package) {
		requireBlocks(t, code, [][]string{{"A", "B"}, {"C", "D"}})
		requireValues(t, code, pkg, map[string]int64{"A": 0, "B": 1, "C": 2, "D": 3})
		if !strings.Contains(code, "\tB\n)\n\n// Upper half.\nconst (\n\tC = iota + 2\n") {
			t.Errorf("the second block's doc must stay directly above the second block:\n%s", code)
		}
	})
}

// TestMerge_Positive_SpecMovedBetweenAdjacentBlocksFollowsTheMove covers a spec moved from
// one block into its neighbour, in both directions: the merge follows the side that moved
// it, and each moved spec keeps the iota value it has on that side.
func TestMerge_Positive_SpecMovedBetweenAdjacentBlocksFollowsTheMove(t *testing.T) {
	base := `package regroup

const (
	A = iota
	B
)

const (
	C = iota
	D
)
`
	intoSecond := strings.Replace(strings.Replace(base, "\tB\n", "", 1), "\tC = iota\n", "\tB = iota\n\tC\n", 1)
	mergeEitherWay(t, base, intoSecond, func(t *testing.T, code string, pkg *types.Package) {
		requireBlocks(t, code, [][]string{{"A"}, {"B", "C", "D"}})
		requireValues(t, code, pkg, map[string]int64{"A": 0, "B": 0, "C": 1, "D": 2})
	})
	intoFirst := strings.Replace(strings.Replace(base, "\tB\n", "\tB\n\tC\n", 1), "\tC = iota\n\tD\n", "\tD = iota\n", 1)
	mergeEitherWay(t, base, intoFirst, func(t *testing.T, code string, pkg *types.Package) {
		requireBlocks(t, code, [][]string{{"A", "B", "C"}, {"D"}})
		requireValues(t, code, pkg, map[string]int64{"A": 0, "B": 1, "C": 2, "D": 0})
	})
}

// TestMerge_Positive_IndependentRegroupsMergeClean: each side regrouping a different block
// is two independent changes; the merge keeps both.
func TestMerge_Positive_IndependentRegroupsMergeClean(t *testing.T) {
	base := iotaBlockBase + "\nconst (\n\tW = iota\n\tX\n\tY\n)\n"
	ours := strings.Replace(base, "\tB\n\tC\n", "\tB\n)\n\nconst (\n\tC = iota + 2\n", 1)
	theirs := strings.Replace(base, "\tX\n\tY\n", "\tX\n)\n\nconst (\n\tY = iota + 2\n", 1)
	code, pkg := mergeClean(t, base, ours, theirs)
	requireBlocks(t, code, [][]string{{"A", "B"}, {"C", "D"}, {"W", "X"}, {"Y"}})
	requireValues(t, code, pkg, map[string]int64{"C": 2, "D": 3, "X": 1, "Y": 2})
}

const twoBlockBase = `package regroup

const (
	A = iota
	B
)

const (
	C = iota
	D
)
`

// TestMerge_Negative_ConflictingRegroupsConflict: when the sides group the specs they share
// differently and neither kept base's grouping, when a spec one side adds to a block would
// join blocks the other side split, or when both add one spec to different blocks, no
// grouping is right and the merge must not pick one.
func TestMerge_Negative_ConflictingRegroupsConflict(t *testing.T) {
	splitAfterC := strings.Replace(iotaBlockBase, "\tC\n\tD\n", "\tC\n)\n\nconst (\n\tD = iota + 3\n", 1)
	addToBlock := strings.Replace(iotaBlockBase, "\tD\n", "\tD\n\tE\n", 1)
	cases := []struct {
		name, base, ours, theirs string
	}{
		{"both split differently", iotaBlockBase, splitAfterB, splitAfterC},
		{"theirs adds to the split", iotaBlockBase, splitAfterB, addToBlock},
		{"ours adds to the split", iotaBlockBase, addToBlock, splitAfterC},
		{"same spec added to two blocks", twoBlockBase,
			strings.Replace(twoBlockBase, "\tB\n", "\tB\n\tE = 9\n", 1),
			strings.Replace(twoBlockBase, "\tD\n", "\tD\n\tE = 9\n", 1)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := requireConflict(t, tc.base, tc.ours, tc.theirs, "block:const:A")
			for _, c := range res.Conflicts {
				if c.Symbol == "block:const:A" && (c.Kind != "block" || c.Ours == c.Theirs) {
					t.Errorf("the grouping conflict must name the block and show both groupings: %+v", c)
				}
			}
		})
	}
}

// TestMerge_Boundary_UnchangedGroupingStaysClean covers the edges of the leader choice:
// adjacent blocks neither side regroups stay apart while both sides add specs to them, and
// both sides making the same split agree without a conflict.
func TestMerge_Boundary_UnchangedGroupingStaysClean(t *testing.T) {
	ours := strings.Replace(twoBlockBase, "\tB\n", "\tB\n\tE\n", 1)
	theirs := strings.Replace(twoBlockBase, "\tD\n", "\tD\n\tF\n", 1)
	code, pkg := mergeClean(t, twoBlockBase, ours, theirs)
	requireBlocks(t, code, [][]string{{"A", "B", "E"}, {"C", "D", "F"}})
	requireValues(t, code, pkg, map[string]int64{"E": 2, "C": 0, "F": 2})

	code, pkg = mergeClean(t, iotaBlockBase, splitAfterB, splitAfterB+"\nfunc Theirs() {}\n")
	requireBlocks(t, code, [][]string{{"A", "B"}, {"C", "D"}})
	requireValues(t, code, pkg, map[string]int64{"C": 2, "D": 3})
}
