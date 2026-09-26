package astmerge

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
)

// layoutEntry is one package-level declaration of a version, in source order: the name it
// declares and the index of the top-level declaration it is written in.
type layoutEntry struct {
	name string
	decl int
}

// initPlace is where an initializer is written: how many layout entries precede it, and
// the index of the top-level declaration it is written in.
type initPlace struct {
	before, decl int
}

// layoutOf lists a version's package-level declarations in source order and places each
// initializer among them, keyed as initializerKey names it. Methods are not package-level
// names and stay out.
func layoutOf(file *ast.File, scope *types.Scope, order []*types.Initializer) ([]layoutEntry, map[string]initPlace) {
	starts := make([]token.Pos, 0, len(file.Decls))
	for _, decl := range file.Decls {
		starts = append(starts, decl.Pos())
	}
	names := scope.Names()
	objs := make([]types.Object, 0, len(names))
	for _, name := range names {
		objs = append(objs, scope.Lookup(name))
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Pos() < objs[j].Pos() })
	layout := make([]layoutEntry, 0, len(objs))
	for _, obj := range objs {
		layout = append(layout, layoutEntry{name: obj.Name(), decl: declIndex(starts, obj.Pos())})
	}
	places := make(map[string]initPlace, len(order))
	for _, in := range order {
		if len(in.Lhs) == 0 {
			continue
		}
		pos := in.Lhs[0].Pos()
		before := sort.Search(len(objs), func(i int) bool { return objs[i].Pos() >= pos })
		places[initializerKey(in)] = initPlace{before: before, decl: declIndex(starts, pos)}
	}
	return layout, places
}

// declIndex returns the index of the top-level declaration that holds pos, given the
// declarations' start positions in source order.
func declIndex(starts []token.Pos, pos token.Pos) int {
	return sort.Search(len(starts), func(i int) bool { return starts[i] > pos }) - 1
}

// declares reports whether a version declares a package-level name of any kind.
func declares(facts semanticFacts, name string) bool {
	_, isConst := facts.consts[name]
	_, isDecl := facts.decls[name]
	return isConst || isDecl
}

// anchors indexes a side's layout by the declarations base and the other side share: for
// every layout position k, prev[k] is the last shared entry before it and next[k] the
// first shared entry at or after it, -1 for none.
type anchors struct {
	layout     []layoutEntry
	prev, next []int
}

func anchorsOf(side, base, other semanticFacts) anchors {
	n := len(side.layout)
	a := anchors{layout: side.layout, prev: make([]int, n+1), next: make([]int, n+1)}
	a.prev[0], a.next[n] = -1, -1
	shared := make([]bool, n)
	for k, entry := range side.layout {
		shared[k] = declares(base, entry.name) && declares(other, entry.name)
		a.prev[k+1] = a.prev[k]
		if shared[k] {
			a.prev[k+1] = k
		}
	}
	for k := n - 1; k >= 0; k-- {
		a.next[k] = a.next[k+1]
		if shared[k] {
			a.next[k] = k
		}
	}
	return a
}

// slot names where an initializer stands among the shared declarations: the shared
// declaration before it and the one after it, and whether it is written in the same
// declaration as the one before ("in-prev"), as the one after ("in-next"), or on its own
// between them ("between"). An initializer written into a shared var block and one written
// after that block have different slots, since the block's end orders them.
func (a anchors) slot(place initPlace) string {
	prev, next := a.prev[place.before], a.next[place.before]
	where := "between"
	switch {
	case prev >= 0 && a.layout[prev].decl == place.decl:
		where = "in-prev"
	case next >= 0 && a.layout[next].decl == place.decl:
		where = "in-next"
	}
	return a.name(prev) + "\x00" + a.name(next) + "\x00" + where
}

// name returns the name of layout entry k, "" for none.
func (a anchors) name(k int) string {
	if k < 0 {
		return ""
	}
	return a.layout[k].name
}

// initSlots maps each merged initializer, by merged index, to its slot in a side (see
// anchors.slot), or "" when the side does not place it. Two initializers with one slot
// stand at the same place among everything base and both sides share.
func initSlots(merged []string, side, base, other semanticFacts) []string {
	a := anchorsOf(side, base, other)
	out := make([]string, len(merged))
	for i, key := range merged {
		if place, ok := side.places[key]; ok {
			out[i] = a.slot(place)
		}
	}
	return out
}
