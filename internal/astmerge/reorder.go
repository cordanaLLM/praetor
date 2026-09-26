package astmerge

import (
	"slices"
	"sort"
)

// mergeOrder ranks the region's specs in merged order: the leading side's order, with the
// specs only the other side holds inserted next to their neighbours on that side. The
// merged order started from base's for every block, so a side's reordering of a block was
// dropped under a clean report: const (A = iota; C; B) merged as A, B, C and swapped the
// values of B and C. Order decides the value of every spec that repeats an implicit
// expression, not only iota, so any reordering counts. It reports false when no side leads.
func (m groupMerge) mergeOrder(keys []string) bool {
	lead, other, ok := m.orderLeader(keys)
	if !ok {
		return false
	}
	merged := interleaveKeys(orderOf(lead, keys), orderOf(other, keys))
	for i, key := range merged {
		m.rank[key] = i
	}
	return true
}

// orderLeader picks whose order of the region's specs the merge follows. Where ours and
// theirs order the specs they both hold alike, ours does. Where they differ, the side that
// kept base's order yields to the side that changed it, unless the yielding side regrouped
// the region: its blocks under the other side's order would hold neither side's values.
// A difference over a spec base lacks, or both sides reordering, leaves no leader.
func (m groupMerge) orderLeader(keys []string) (map[string]int, map[string]int, bool) {
	shared := heldBy(keys, m.oursPos, m.theirsPos)
	oursOrder, theirsOrder := orderOf(m.oursPos, shared), orderOf(m.theirsPos, shared)
	if slices.Equal(oursOrder, theirsOrder) {
		return m.oursPos, m.theirsPos, true
	}
	if len(heldBy(shared, m.basePos)) < len(shared) {
		return nil, nil, false
	}
	baseOrder := orderOf(m.basePos, shared)
	switch {
	case slices.Equal(theirsOrder, baseOrder) && !m.regroups(m.theirs, keys):
		return m.oursPos, m.theirsPos, true
	case slices.Equal(oursOrder, baseOrder) && !m.regroups(m.ours, keys):
		return m.theirsPos, m.oursPos, true
	}
	return nil, nil, false
}

// heldBy returns the keys every given input holds, in the order of keys.
func heldBy(keys []string, inputs ...map[string]int) []string {
	out := make([]string, 0, len(keys))
	for _, key := range keys {
		if heldByAll(key, inputs) {
			out = append(out, key)
		}
	}
	return out
}

func heldByAll(key string, inputs []map[string]int) bool {
	for _, pos := range inputs {
		if _, held := pos[key]; !held {
			return false
		}
	}
	return true
}

// orderOf returns the keys the input holds, in the input's order.
func orderOf(pos map[string]int, keys []string) []string {
	out := heldBy(keys, pos)
	sort.Slice(out, func(i, j int) bool { return pos[out[i]] < pos[out[j]] })
	return out
}
