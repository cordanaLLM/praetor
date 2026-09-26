package astmerge

import (
	"sort"
	"strings"
)

// blockSets links item keys into sets, mapping a key to its parent; a key without an entry
// is its own root.
type blockSets map[string]string

// isCommentKey reports whether a key names a comment item rather than a spec.
func isCommentKey(key string) bool {
	return strings.HasPrefix(key, kindComment+":")
}

// linkBlocks unites the spec keys of every block of every input, marking the regions whose
// grouping mergeLayout decides together. Comment keys stay out: a comment's key is its
// text, which may recur in unrelated blocks, and linking through it would tie their
// groupings together.
func linkBlocks(inputs ...*ParsedAST) blockSets {
	sets := make(blockSets)
	for _, input := range inputs {
		for _, keys := range input.Blocks {
			first := ""
			for _, key := range keys {
				switch {
				case isCommentKey(key):
				case first == "":
					first = key
				default:
					sets.union(first, key)
				}
			}
		}
	}
	return sets
}

// find returns the root of key's set, pointing key straight at it for later lookups.
func (s blockSets) find(key string) string {
	root := key
	for i := 0; i < 3*maxDeclItems; i++ {
		parent, ok := s[root]
		if !ok || parent == root {
			break
		}
		root = parent
	}
	if root != key {
		s[key] = root
	}
	return root
}

func (s blockSets) union(a, b string) {
	if ra, rb := s.find(a), s.find(b); ra != rb {
		s[rb] = ra
	}
}

// layout is how the merged specs render: which of them share a block, and each spec's
// position in the merged order of its region.
type layout struct {
	sets blockSets
	rank map[string]int
}

// isBlockSpec reports whether the item is a spec written inside a parenthesized block.
func isBlockSpec(item DeclItem) bool {
	return item.Block != "" && item.Kind != kindComment
}

// arrange puts the specs of each merged block together, in the merged order of their
// region, where the earliest of them stands among the merged items; every other item
// keeps its place. Placing the block at the spec its region ranks first instead moved a
// var block past the declarations between the two, which changed the order the
// variables are initialized in.
func (l layout) arrange(items []DeclItem) []DeclItem {
	blocks := make(map[string][]DeclItem)
	for _, item := range items {
		if isBlockSpec(item) {
			root := l.sets.find(item.Key)
			blocks[root] = append(blocks[root], item)
		}
	}
	for _, members := range blocks {
		sort.SliceStable(members, func(i, j int) bool { return l.rank[members[i].Key] < l.rank[members[j].Key] })
	}
	out := make([]DeclItem, 0, len(items))
	placed := make(map[string]bool, len(blocks))
	for _, item := range items {
		if !isBlockSpec(item) {
			out = append(out, item)
			continue
		}
		if root := l.sets.find(item.Key); !placed[root] {
			placed[root] = true
			out = append(out, blocks[root]...)
		}
	}
	return out
}

// renderDecls turns merged items into source chunks. Consecutive items written inside
// blocks of one keyword whose specs the merged grouping puts together render as one block
// again, so a grouped const declaration renders once and its iota sequence stays in one
// block.
func renderDecls(merged []DeclItem, l layout) []string {
	items := l.arrange(merged)
	chunks := make([]string, 0, len(items))
	for i := 0; i < len(items); {
		if items[i].Block == "" {
			chunks = append(chunks, items[i].Body)
			i++
			continue
		}
		end := blockRunEnd(items, i, l.sets)
		chunks = append(chunks, renderBlock(items[i:end]))
		i = end
	}
	return chunks
}

// blockRunEnd returns the index after the last item of the block starting at start: the
// run of items with the same block keyword whose specs share one linked set. Comments
// continue the run they sit in.
func blockRunEnd(items []DeclItem, start int, sets blockSets) int {
	root := ""
	for j := start; j < len(items); j++ {
		item := items[j]
		if item.Block != items[start].Block {
			return j
		}
		if item.Kind == kindComment {
			continue
		}
		switch r := sets.find(item.Key); {
		case root == "":
			root = r
		case r != root:
			return j
		}
	}
	return len(items)
}

// renderBlock renders one parenthesized declaration: the distinct block docs its items
// carry, then the items in order. Blank lines between specs are not kept; gofmt alignment
// of the block may change, its meaning does not.
func renderBlock(items []DeclItem) string {
	var b strings.Builder
	seen := make(map[string]bool)
	for _, item := range items {
		if item.Header != "" && !seen[item.Header] {
			seen[item.Header] = true
			b.WriteString(item.Header)
			b.WriteString("\n")
		}
	}
	b.WriteString(items[0].Block)
	b.WriteString(" (\n")
	for i, item := range items {
		b.WriteString(item.Body)
		b.WriteString("\n")
		// A comment the block owns was not a spec's doc, so a blank line follows it;
		// without one a later parse would attach it to the next spec as that spec's doc.
		if item.Kind == kindComment && i < len(items)-1 {
			b.WriteString("\n")
		}
	}
	b.WriteString(")")
	return b.String()
}
