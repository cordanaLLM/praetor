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

// layout is how the merged specs render: which of them share a block, each spec's
// position in the merged order of its region, and the specs each comment written inside a
// block renders next to (see blockCommentAnchors).
type layout struct {
	sets    blockSets
	rank    map[string]int
	anchors map[string][]anchor
}

// isBlockSpec reports whether the item is a spec written inside a parenthesized block.
func isBlockSpec(item DeclItem) bool {
	return item.Block != "" && item.Kind != kindComment
}

// arrange puts the specs of each merged block together, in the merged order of their
// region, where the earliest of them stands among the merged items; a comment written
// inside a block travels with the spec it is anchored to, and every other item keeps its
// place. Placing the block at the spec its region ranks first instead moved a var block
// past the declarations between the two, which changed the order the variables are
// initialized in. It also returns, for each comment it moved, the spec it moved it to.
func (l layout) arrange(items []DeclItem) ([]DeclItem, map[string]string) {
	blocks := make(map[string][]DeclItem)
	specBlock := make(map[string]string)
	for _, item := range items {
		if isBlockSpec(item) {
			root := l.sets.find(item.Key)
			blocks[root] = append(blocks[root], item)
			specBlock[item.Key] = item.Block
		}
	}
	for _, members := range blocks {
		sort.SliceStable(members, func(i, j int) bool { return l.rank[members[i].Key] < l.rank[members[j].Key] })
	}
	comments := attachComments(items, l.anchors, specBlock)
	out := make([]DeclItem, 0, len(items))
	placed := make(map[string]bool, len(blocks))
	for _, item := range items {
		root := l.sets.find(item.Key)
		_, attached := comments.anchorOf[item.Key]
		switch {
		case attached:
		case !isBlockSpec(item):
			out = append(out, item)
		case !placed[root]:
			placed[root] = true
			out = comments.place(out, blocks[root])
		}
	}
	return out, comments.anchorOf
}

// renderDecls turns merged items into source chunks. Consecutive items written inside
// blocks of one keyword whose specs the merged grouping puts together render as one block
// again, so a grouped const declaration renders once and its iota sequence stays in one
// block.
func renderDecls(merged []DeclItem, l layout) []string {
	items, anchorOf := l.arrange(merged)
	chunks := make([]string, 0, len(items))
	for i := 0; i < len(items); {
		if items[i].Block == "" {
			chunks = append(chunks, items[i].Body)
			i++
			continue
		}
		end := blockRunEnd(items, i, l.sets, anchorOf)
		chunks = append(chunks, renderBlock(items[i:end]))
		i = end
	}
	return chunks
}

// blockRunEnd returns the index after the last item of the block starting at start: the
// run of items with the same block keyword whose specs share one linked set. A comment
// arrange anchored to a spec belongs to that spec's block, so a comment leading the second
// of two adjacent blocks does not extend the first; any other comment continues the run it
// sits in.
func blockRunEnd(items []DeclItem, start int, sets blockSets, anchorOf map[string]string) int {
	root := ""
	for j := start; j < len(items); j++ {
		item := items[j]
		if item.Block != items[start].Block {
			return j
		}
		key := item.Key
		if item.Kind == kindComment {
			if key = anchorOf[item.Key]; key == "" {
				continue
			}
		}
		switch r := sets.find(key); {
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
		// A comment keeps the indentation its first line lost to the source span: gofmt
		// leaves a comment that starts at column 1 before the closing parenthesis there.
		if item.Kind == kindComment {
			b.WriteString("\t")
		}
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
