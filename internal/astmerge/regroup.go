package astmerge

import (
	"fmt"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

// kindBlock is the Conflict kind of two incompatible groupings of block specs.
const kindBlock = "block"

// partition maps every spec key an input holds to the block it is written in. A spec at
// top level is a block of its own; comment items stay out, as in linkBlocks.
type partition map[string]string

func partitionOf(p *ParsedAST) partition {
	part := make(partition, len(p.DeclOrder))
	for _, key := range p.DeclOrder {
		if !isCommentKey(key) {
			part[key] = "top:" + key
		}
	}
	for i, keys := range p.Blocks {
		id := "block:" + strconv.Itoa(i)
		for _, key := range keys {
			if !isCommentKey(key) {
				part[key] = id
			}
		}
	}
	return part
}

// groupMerge decides the grouping and the order of the merged specs, one region at a time:
// a region is a set of specs some input wrote in one block, closed over every input.
type groupMerge struct {
	base, ours, theirs partition
	// basePos, oursPos and theirsPos map each key an input holds to its position there.
	basePos, oursPos, theirsPos map[string]int
	// inputs are base, ours and theirs, in that order.
	inputs [3]*ParsedAST
	sets   blockSets
	rank   map[string]int
}

// mergeLayout returns which merged specs render in one block and in what order, and a
// conflict for every region the sides regroup or reorder incompatibly. Uniting the specs
// every input wrote in one block re-joined a block one side split, so a clean merge shifted
// the iota values of the second half and hoisted its doc over the whole block; the
// grouping is merged 3-way instead, and so is the order (see mergeOrder).
func mergeLayout(base, ours, theirs *ParsedAST) (layout, []Conflict) {
	m := groupMerge{
		base: partitionOf(base), ours: partitionOf(ours), theirs: partitionOf(theirs),
		basePos: indexOf(base.DeclOrder), oursPos: indexOf(ours.DeclOrder), theirsPos: indexOf(theirs.DeclOrder),
		inputs: [3]*ParsedAST{base, ours, theirs},
		sets:   make(blockSets), rank: make(map[string]int),
	}
	var conflicts []Conflict
	for _, keys := range regions(base, ours, theirs) {
		switch {
		case m.positional(keys) && m.bothChange(keys):
			conflicts = append(conflicts, m.blockConflict(keys, "Conflicting changes to the parenthesized declarations holding %s, whose values follow position (iota or an implicit repetition)"))
		case isVarRegion(keys) && m.bothChange(keys) && m.ambiguousVarOrder(keys):
			conflicts = append(conflicts, m.blockConflict(keys, "Conflicting changes to the order of the var declarations holding %s, which decides the order they are initialized in"))
		case !m.mergeRegion(keys):
			conflicts = append(conflicts, m.blockConflict(keys, "Conflicting regrouping of the parenthesized declarations holding %s"))
		case !m.mergeOrder(keys):
			conflicts = append(conflicts, m.blockConflict(keys, "Conflicting reordering of the parenthesized declarations holding %s"))
		}
	}
	return layout{sets: m.sets, rank: m.rank, anchors: blockCommentAnchors(base, ours, theirs)}, conflicts
}

// positional reports whether some input writes a spec of the region whose value follows
// its position: iota counts it, or it repeats the expression of the spec before it.
func (m groupMerge) positional(keys []string) bool {
	for _, key := range keys {
		for _, input := range m.inputs {
			if input.positional[key] {
				return true
			}
		}
	}
	return false
}

// bothChange reports whether ours and theirs each change the region from base, and not in
// the same way. Such a region merges as one unit, as a whole block did before blocks were
// split per spec: inserting, deleting, moving or editing any spec shifts the value of every
// positional spec after it, so combining one side's specs with the other's order produced
// values neither side wrote.
func (m groupMerge) bothChange(keys []string) bool {
	base := regionSignature(m.inputs[0], m.base, m.basePos, keys)
	ours := regionSignature(m.inputs[1], m.ours, m.oursPos, keys)
	theirs := regionSignature(m.inputs[2], m.theirs, m.theirsPos, keys)
	return ours != base && theirs != base && ours != theirs
}

// isVarRegion reports whether the region's specs declare variables. A region never mixes
// keywords: it links only specs written in one block, and a block has one keyword.
func isVarRegion(keys []string) bool {
	return len(keys) > 0 && strings.HasPrefix(keys[0], token.VAR.String()+":")
}

// ambiguousVarOrder reports whether the sides' changes to a var region leave the order of
// its initializers undecided: one side regroups or reorders the specs while the other
// adds, regroups or reorders too, or both sides add specs at the same place. Either way the
// merge would have to pick an order no side wrote, which a whole block, merged as one
// unit, reported as a conflict.
func (m groupMerge) ambiguousVarOrder(keys []string) bool {
	oursMoves := m.restructures(m.ours, m.oursPos, keys)
	theirsMoves := m.restructures(m.theirs, m.theirsPos, keys)
	oursGaps := m.additionGaps(m.oursPos, m.theirsPos, keys)
	theirsGaps := m.additionGaps(m.theirsPos, m.oursPos, keys)
	switch {
	case oursMoves && (theirsMoves || len(theirsGaps) > 0):
		return true
	case theirsMoves && len(oursGaps) > 0:
		return true
	}
	for gap := range theirsGaps {
		if oursGaps[gap] {
			return true
		}
	}
	return false
}

// restructures reports whether the side regroups the region's specs it shares with base or
// orders them otherwise.
func (m groupMerge) restructures(side partition, sidePos map[string]int, keys []string) bool {
	if m.regroups(side, keys) {
		return true
	}
	shared := heldBy(keys, m.basePos, sidePos)
	return !slices.Equal(orderOf(sidePos, shared), orderOf(m.basePos, shared))
}

// additionGaps returns where the side adds specs to the region: for each spec neither base
// nor the other side holds, the spec all three hold that precedes it on the side, or ""
// at the start of the region.
func (m groupMerge) additionGaps(side, other map[string]int, keys []string) map[string]bool {
	gaps := make(map[string]bool)
	anchor := ""
	for _, key := range orderOf(side, keys) {
		_, inBase := m.basePos[key]
		_, inOther := other[key]
		switch {
		case inBase && inOther:
			anchor = key
		case !inBase && !inOther:
			gaps[anchor] = true
		}
	}
	return gaps
}

// regionSignature renders what an input writes in the region: its specs in order, each
// with the block it shares, numbered by first appearance, and its source text.
func regionSignature(p *ParsedAST, part partition, pos map[string]int, keys []string) string {
	blockIDs := make(map[string]int)
	var b strings.Builder
	for _, key := range orderOf(pos, keys) {
		id, seen := blockIDs[part[key]]
		if !seen {
			id = len(blockIDs)
			blockIDs[part[key]] = id
		}
		item := p.Decls[key]
		fmt.Fprintf(&b, "%d\x00%s\x00%s\x01", id, item.Header, item.Body)
	}
	return b.String()
}

// regions lists the spec keys of each region in the order the inputs first write them.
func regions(inputs ...*ParsedAST) [][]string {
	sets := linkBlocks(inputs...)
	index := make(map[string]int)
	var out [][]string
	for _, input := range inputs {
		for _, keys := range input.Blocks {
			out = appendRegionKeys(out, index, sets, keys)
		}
	}
	return out
}

// appendRegionKeys adds each spec key not yet listed to its region, opening the region at
// its first key; index maps a key, and a region's root, to the region's position.
func appendRegionKeys(out [][]string, index map[string]int, sets blockSets, keys []string) [][]string {
	for _, key := range keys {
		if _, listed := index[key]; listed || isCommentKey(key) {
			continue
		}
		root := "root:" + sets.find(key)
		i, open := index[root]
		if !open {
			i = len(out)
			index[root] = i
			out = append(out, nil)
		}
		index[key] = i
		out[i] = append(out[i], key)
	}
	return out
}

// mergeRegion groups the region's specs after the side that leads it: all its blocks, plus
// every block of the other side that holds a spec base lacks, so that side's additions stay
// with their neighbours. It reports false when no side leads or when such an addition would
// join two blocks of the leading side.
func (m groupMerge) mergeRegion(keys []string) bool {
	lead, other, ok := m.leadingSide(keys)
	if !ok {
		return false
	}
	m.link(keys, lead, false)
	m.link(keys, other, true)
	return !m.joinsBlocks(keys, lead)
}

// leadingSide picks whose grouping of the region the merge follows. Where ours and theirs
// group the specs they share alike, either does. Where they differ, the side that kept
// base's grouping yields to the side that changed it; a difference over a spec base lacks,
// or both sides changing base's grouping, leaves no leader.
func (m groupMerge) leadingSide(keys []string) (partition, partition, bool) {
	differ := regrouped(m.ours, m.theirs, keys)
	if len(differ) == 0 {
		return m.ours, m.theirs, true
	}
	for _, key := range differ {
		if _, inBase := m.base[key]; !inBase {
			return nil, nil, false
		}
	}
	oursMoved, theirsMoved := m.regroups(m.ours, keys), m.regroups(m.theirs, keys)
	switch {
	case oursMoved && !theirsMoved:
		return m.ours, m.theirs, true
	case theirsMoved && !oursMoved:
		return m.theirs, m.ours, true
	}
	return nil, nil, false
}

// regroups reports whether the side groups the region keys it shares with base otherwise.
func (m groupMerge) regroups(side partition, keys []string) bool {
	return len(regrouped(m.base, side, keys)) > 0
}

// regrouped returns the region keys both partitions hold whose blocks do not correspond one
// to one: some spec shares a block with the key in one partition but not in the other.
func regrouped(a, b partition, keys []string) []string {
	aToB := make(map[string]map[string]bool)
	bToA := make(map[string]map[string]bool)
	var shared []string
	for _, key := range keys {
		blockA, inA := a[key]
		blockB, inB := b[key]
		if inA && inB {
			shared = append(shared, key)
			relate(aToB, blockA, blockB)
			relate(bToA, blockB, blockA)
		}
	}
	var out []string
	for _, key := range shared {
		if len(aToB[a[key]]) > 1 || len(bToA[b[key]]) > 1 {
			out = append(out, key)
		}
	}
	return out
}

func relate(rel map[string]map[string]bool, from, to string) {
	if rel[from] == nil {
		rel[from] = make(map[string]bool)
	}
	rel[from][to] = true
}

// link unites the region keys the side writes in one block; with onlyAdded set, only the
// blocks that hold a spec base lacks.
func (m groupMerge) link(keys []string, side partition, onlyAdded bool) {
	added := make(map[string]bool)
	for _, key := range keys {
		block, held := side[key]
		if _, inBase := m.base[key]; held && !inBase {
			added[block] = true
		}
	}
	first := make(map[string]string)
	for _, key := range keys {
		block, held := side[key]
		if !held || (onlyAdded && !added[block]) {
			continue
		}
		if head, seen := first[block]; seen {
			m.sets.union(head, key)
		} else {
			first[block] = key
		}
	}
}

// joinsBlocks reports whether the merged grouping puts specs of two different blocks of
// the leading side into one block.
func (m groupMerge) joinsBlocks(keys []string, lead partition) bool {
	blockOf := make(map[string]string)
	for _, key := range keys {
		block, held := lead[key]
		if !held {
			continue
		}
		root := m.sets.find(key)
		if seen, ok := blockOf[root]; ok && seen != block {
			return true
		}
		blockOf[root] = block
	}
	return false
}

// blockConflict reports a region whose grouping, order or positional values the sides
// change incompatibly, with each input's blocks in that input's order; reason is a format
// that names the region's first spec.
func (m groupMerge) blockConflict(keys []string, reason string) Conflict {
	return Conflict{
		Symbol: kindBlock + ":" + keys[0],
		Kind:   kindBlock,
		Reason: fmt.Sprintf(reason, keys[0]),
		Ours:   grouping(m.ours, orderOf(m.oursPos, keys)),
		Theirs: grouping(m.theirs, orderOf(m.theirsPos, keys)),
		Base:   grouping(m.base, orderOf(m.basePos, keys)),
	}
}

// grouping describes how a partition groups the keys, one block per line.
func grouping(part partition, keys []string) string {
	var order []string
	members := make(map[string][]string)
	for _, key := range keys {
		block, held := part[key]
		if !held {
			continue
		}
		if _, seen := members[block]; !seen {
			order = append(order, block)
		}
		members[block] = append(members[block], key)
	}
	lines := make([]string, 0, len(order))
	for _, block := range order {
		lines = append(lines, "("+strings.Join(members[block], ", ")+")")
	}
	return strings.Join(lines, "\n")
}
