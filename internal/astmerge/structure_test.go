package astmerge

import (
	"reflect"
	"strings"
	"testing"
)

const explicitPair = "package place\n\nconst (\n\tA = 1\n\tB = 2\n\tC = 3\n)\n\nconst (\n\tX = 10\n)\n"

// movedB moves B out of the first block into the second, with its value.
var movedB = strings.Replace(strings.Replace(explicitPair, "\tB = 2\n", "", 1), "\tX = 10\n", "\tX = 10\n\tB = 2\n", 1)

// TestMerge_Negative_MovedSpecEditedOrDeletedConflicts: sameItem compared a spec's text
// alone, so one side's move of B slipped past the other side's edit or deletion of it and
// merged clean with one side's intent silently gone.
func TestMerge_Negative_MovedSpecEditedOrDeletedConflicts(t *testing.T) {
	edited := strings.Replace(explicitPair, "B = 2", "B = 20", 1)
	requireConflict(t, explicitPair, movedB, edited, "const:B")
	requireConflict(t, explicitPair, edited, movedB, "const:B")
	deleted := strings.Replace(explicitPair, "\tB = 2\n", "", 1)
	requireConflict(t, explicitPair, movedB, deleted, "const:B")
	requireConflict(t, explicitPair, deleted, movedB, "const:B")
}

// TestMerge_Positive_NeighbourChangesDoNotMoveASpec: a spec another spec was added before,
// or removed from before, keeps its place, so an edit to it on the other side still merges.
func TestMerge_Positive_NeighbourChangesDoNotMoveASpec(t *testing.T) {
	edited := strings.Replace(explicitPair, "C = 3", "C = 30", 1)
	for name, other := range map[string]string{
		"insert before": strings.Replace(explicitPair, "\tA = 1\n", "\tA = 1\n\tN = 5\n", 1),
		"delete before": strings.Replace(explicitPair, "\tB = 2\n", "", 1),
	} {
		t.Run(name, func(t *testing.T) {
			code, pkg := mergeClean(t, explicitPair, other, edited)
			requireValues(t, code, pkg, map[string]int64{"C": 30})
		})
	}
}

// TestPlacesOf_Boundary_TopLevelBlockAndComments covers every kind of item placesOf sees:
// a top-level spec, a block spec whose block-mate the other input lacks (and which has no
// place of its own, since only specs both inputs hold are compared), a function and a
// comment, which have no place.
func TestPlacesOf_Boundary_TopLevelBlockAndComments(t *testing.T) {
	a, err := parseSourceSafe("a.go", "package p\n\nconst Top = 1\n\nconst (\n\tA = 1\n\t// note\n\n\tB = 2\n)\n\nfunc F() {}\n")
	if err != nil {
		t.Fatal(err)
	}
	b, err := parseSourceSafe("b.go", "package p\n\nconst (\n\tB = 2\n)\n")
	if err != nil {
		t.Fatal(err)
	}
	places := placesOf(a, b)
	want := map[string]string{"const:Top": "top", "const:B": "block\x00const:B\x00#0"}
	if !reflect.DeepEqual(places, want) {
		t.Errorf("placesOf = %q, want %q", places, want)
	}
}

// TestInterleaveKeys_Positive_AnchorsAfterTheLatestPredecessor: when order moved a key back,
// anchoring an addition on its nearest predecessor put it before a key its side placed
// ahead of it; it now goes after the latest of its predecessors.
func TestInterleaveKeys_Positive_AnchorsAfterTheLatestPredecessor(t *testing.T) {
	got := interleaveKeys([]string{"b", "a", "c"}, []string{"a", "b", "n", "c"})
	if want := []string{"b", "a", "n", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("interleaveKeys = %v, want %v", got, want)
	}
}

// TestInterleaveKeys_Boundary_EmptyAndTrailing covers an empty order, an addition before
// every shared key, and additions after the last one.
func TestInterleaveKeys_Boundary_EmptyAndTrailing(t *testing.T) {
	cases := []struct {
		order, side, want []string
	}{
		{nil, []string{"a", "b"}, []string{"a", "b"}},
		{[]string{"a", "b"}, []string{"n", "a", "b"}, []string{"n", "a", "b"}},
		{[]string{"a", "x", "b"}, []string{"a", "b", "n"}, []string{"a", "x", "b", "n"}},
	}
	for _, tc := range cases {
		if got := interleaveKeys(tc.order, tc.side); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("interleaveKeys(%v, %v) = %v, want %v", tc.order, tc.side, got, tc.want)
		}
	}
}

const varBlock = "package v\n\nfunc rec(s string) int { return len(s) }\n\nvar (\n\ta = rec(\"a\")\n\tb = rec(\"b\")\n\tc = rec(\"c\")\n)\n"

// TestMerge_Positive_VarBlockEditsAndDistinctAdditionsMerge: per-spec edits and additions
// at different places of one var block combine; each keeps the order its side gave it.
func TestMerge_Positive_VarBlockEditsAndDistinctAdditionsMerge(t *testing.T) {
	ours := strings.Replace(strings.Replace(varBlock, "rec(\"a\")", "rec(\"a2\")", 1), "\tb = rec(\"b\")\n", "\tb = rec(\"b\")\n\tx = rec(\"x\")\n", 1)
	theirs := strings.Replace(varBlock, "\tc = rec(\"c\")\n", "\tc = rec(\"c2\")\n\ty = rec(\"y\")\n", 1)
	code, _ := mergeClean(t, varBlock, ours, theirs)
	requireBlocks(t, code, [][]string{{"a", "b", "x", "c", "y"}})
	if !strings.Contains(code, "rec(\"a2\")") || !strings.Contains(code, "rec(\"c2\")") {
		t.Errorf("expected both edits:\n%s", code)
	}
}

// TestMerge_Negative_AmbiguousVarBlockOrderConflicts: when one side reorders a var block the
// other adds to, or both add at the same place, no side wrote the merged initialization
// order, which a whole block merged as one unit reported as a conflict.
func TestMerge_Negative_AmbiguousVarBlockOrderConflicts(t *testing.T) {
	reordered := strings.Replace(varBlock, "\ta = rec(\"a\")\n\tb = rec(\"b\")\n", "\tb = rec(\"b\")\n\ta = rec(\"a\")\n", 1)
	addX := strings.Replace(varBlock, "\tc = rec(\"c\")\n", "\tc = rec(\"c\")\n\tx = rec(\"x\")\n", 1)
	addY := strings.Replace(varBlock, "\tc = rec(\"c\")\n", "\tc = rec(\"c\")\n\ty = rec(\"y\")\n", 1)
	requireConflict(t, varBlock, reordered, addX, "block:var:a")
	requireConflict(t, varBlock, addX, reordered, "block:var:a")
	requireConflict(t, varBlock, addX, addY, "block:var:a")
}

// TestMerge_Boundary_VarBlockReorderWithOtherSideEditsOnly: a reordering merges with the
// other side's edit to a spec the reordering did not move; the merged order is the
// reordering side's.
func TestMerge_Boundary_VarBlockReorderWithOtherSideEditsOnly(t *testing.T) {
	reordered := strings.Replace(varBlock, "\ta = rec(\"a\")\n\tb = rec(\"b\")\n", "\tb = rec(\"b\")\n\ta = rec(\"a\")\n", 1)
	edited := strings.Replace(varBlock, "rec(\"c\")", "rec(\"c2\")", 1)
	code, _ := mergeClean(t, varBlock, reordered, edited)
	requireBlocks(t, code, [][]string{{"b", "a", "c"}})
}
