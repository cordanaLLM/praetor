package astmerge

import (
	"strings"
	"testing"
)

// slotHeader starts a file whose var initializers record the order they run in.
const slotHeader = "package p\n\nvar log []string\n\nfunc rec(s string) int { log = append(log, s); return len(log) }\n"

// slotBase holds two shared var declarations with a function between them.
const slotBase = slotHeader + "\nvar a = rec(\"a\")\n\nfunc F() {}\n\nvar b = rec(\"b\")\n"

// TestMerge_Negative_VarsBothSidesAddAtOnePlaceConflict: each side adds a variable at the
// same place, so no input orders the two initializers. The merge used to run ours first; it
// now fails closed, as the same additions inside one var block already did.
func TestMerge_Negative_VarsBothSidesAddAtOnePlaceConflict(t *testing.T) {
	cases := map[string][2]string{
		"appended at the end": {slotBase + "\nvar x = rec(\"x\")\n", slotBase + "\nvar y = rec(\"y\")\n"},
		"between two shared": {
			strings.Replace(slotBase, "\nfunc F() {}\n", "\nvar x = rec(\"x\")\n\nfunc F() {}\n", 1),
			strings.Replace(slotBase, "\nfunc F() {}\n", "\nvar y = rec(\"y\")\n\nfunc F() {}\n", 1),
		},
		"new blocks at the start": {
			strings.Replace(slotBase, "\nvar a =", "\nvar (\n\tx = rec(\"x\")\n)\n\nvar a =", 1),
			strings.Replace(slotBase, "\nvar a =", "\nvar y = rec(\"y\")\n\nvar a =", 1),
		},
	}
	for name, sides := range cases {
		t.Run(name, func(t *testing.T) {
			requireConflict(t, slotBase, sides[0], sides[1], "init-order")
		})
	}
}

// TestMerge_Positive_VarsAddedAtDifferentPlacesMerge: additions the shared declarations
// order, including one written into a shared var block and one written after that block,
// merge clean and run where their sides put them.
func TestMerge_Positive_VarsAddedAtDifferentPlacesMerge(t *testing.T) {
	ours := strings.Replace(slotBase, "\nfunc F() {}\n", "\nfunc F() {}\n\nvar x = rec(\"x\")\n", 1)
	theirs := slotBase + "\nvar y = rec(\"y\")\n"
	requireInitOrder(t, slotBase, ours, theirs, "a,x,b,y")

	block := slotHeader + "\nvar (\n\ta = rec(\"a\")\n)\n"
	intoBlock := slotHeader + "\nvar (\n\ta = rec(\"a\")\n\tt = rec(\"t\")\n)\n"
	requireInitOrder(t, block, block+"\nvar o = rec(\"o\")\n", intoBlock, "a,t,o")
}

// TestMerge_Boundary_AdditionsThatAreNotInitializersMerge: at one place, a variable beside
// a function, or beside a variable with no initializer, leaves no pair of initializers
// unordered, so the merge stays clean.
func TestMerge_Boundary_AdditionsThatAreNotInitializersMerge(t *testing.T) {
	withX := slotBase + "\nvar x = rec(\"x\")\n"
	requireInitOrder(t, slotBase, withX, slotBase+"\nfunc G() {}\n", "a,b,x")
	requireInitOrder(t, slotBase, withX, slotBase+"\nvar y int\n", "a,b,x")
}

// requireInitOrder merges clean and fails unless the merged initializers run in order.
func requireInitOrder(t *testing.T, base, ours, theirs, order string) {
	t.Helper()
	code, _ := mergeClean(t, base, ours, theirs)
	if got := strings.Join(collectFacts(code).init, ","); got != order {
		t.Fatalf("expected initialization order %s, got %s\n%s", order, got, code)
	}
}

// TestAnchorsSlot_Boundary_Placement covers every placement a slot names: no shared
// declaration at all, inside the declaration of the shared one before, inside the one of
// the shared one after, and on its own between them.
func TestAnchorsSlot_Boundary_Placement(t *testing.T) {
	empty := semanticFacts{}
	if got := anchorsOf(empty, empty, empty).slot(initPlace{}); got != "\x00\x00between" {
		t.Errorf("expected no anchors for an empty layout, got %q", got)
	}
	side := semanticFacts{layout: []layoutEntry{{name: "a", decl: 0}, {name: "x", decl: 1}, {name: "b", decl: 2}}}
	shared := semanticFacts{decls: map[string]string{"a": "var", "b": "var"}}
	a := anchorsOf(side, shared, shared)
	for place, want := range map[initPlace]string{
		{before: 1, decl: 0}: "a\x00b\x00in-prev",
		{before: 1, decl: 2}: "a\x00b\x00in-next",
		{before: 1, decl: 1}: "a\x00b\x00between",
		{before: 3, decl: 3}: "b\x00\x00between",
		{before: 0, decl: 0}: "\x00a\x00in-next",
	} {
		if got := a.slot(place); got != want {
			t.Errorf("slot(%+v) = %q, want %q", place, got, want)
		}
	}
}

// TestLayoutOf_Positive_PlacesInitializers places each initializer among the package-level
// declarations and in its top-level declaration, a blank one included.
func TestLayoutOf_Positive_PlacesInitializers(t *testing.T) {
	facts := collectFacts(slotHeader + "\nvar (\n\ta = rec(\"a\")\n\t_ = rec(\"blank\")\n)\n\nvar b = rec(\"b\")\n")
	var names []string
	for _, entry := range facts.layout {
		names = append(names, entry.name)
	}
	if got := strings.Join(names, ","); got != "log,rec,a,b" {
		t.Fatalf("expected the layout log,rec,a,b, got %s", got)
	}
	want := map[string]initPlace{
		"a":                  {before: 2, decl: 2},
		"_ = rec(\"blank\")": {before: 3, decl: 2},
		"b":                  {before: 3, decl: 3},
	}
	for key, place := range want {
		if got, ok := facts.places[key]; !ok || got != place {
			t.Errorf("place of %q = %+v (%v), want %+v", key, got, ok, place)
		}
	}
}
