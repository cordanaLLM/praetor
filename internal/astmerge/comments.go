package astmerge

import (
	"fmt"
	"slices"
	"sort"
	"strings"
)

// anchor is a spec a comment written inside a block renders next to: right after it, or
// right before it when before is set.
type anchor struct {
	key    string
	before bool
}

// blockCommentAnchors maps each comment the inputs write inside a block to the specs it can
// render next to, in order of preference (see anchorsAt). Ranking only the specs of a block
// left its comments at their own merged position, after the whole block, so every clean
// merge moved them past the last spec. The input a comment's place comes from is chosen
// 3-way, like the comment itself: theirs when ours keeps base's place for it, else ours.
func blockCommentAnchors(base, ours, theirs *ParsedAST) map[string][]anchor {
	b, o, t := commentAnchorsOf(base), commentAnchorsOf(ours), commentAnchorsOf(theirs)
	out := make(map[string][]anchor, len(b)+len(o)+len(t))
	for _, from := range []map[string][]anchor{b, o, t} {
		for key := range from {
			out[key] = pickAnchors(key, b, o, t)
		}
	}
	return out
}

// pickAnchors returns the anchors of the input whose place for the comment the merge keeps.
func pickAnchors(key string, b, o, t map[string][]anchor) []anchor {
	ours, inOurs := o[key]
	theirs, inTheirs := t[key]
	switch {
	case inOurs && inTheirs && firstAnchor(ours) == firstAnchor(b[key]):
		return theirs
	case inOurs:
		return ours
	case inTheirs:
		return theirs
	}
	return b[key]
}

func firstAnchor(anchors []anchor) anchor {
	if len(anchors) == 0 {
		return anchor{}
	}
	return anchors[0]
}

// commentAnchorsOf maps each comment one input writes inside a block to its anchors there.
func commentAnchorsOf(p *ParsedAST) map[string][]anchor {
	out := make(map[string][]anchor)
	for _, keys := range p.Blocks {
		for i, key := range keys {
			if isCommentKey(key) {
				out[key] = anchorsAt(keys, i)
			}
		}
	}
	return out
}

// anchorsAt lists the specs of a block the comment at index at can render next to: after
// each spec before it, nearest first, then before each spec after it, nearest first, so a
// block-mate the merge drops passes the comment on to the next one.
func anchorsAt(keys []string, at int) []anchor {
	out := make([]anchor, 0, len(keys))
	for j := at - 1; j >= 0; j-- {
		if !isCommentKey(keys[j]) {
			out = append(out, anchor{key: keys[j]})
		}
	}
	for j := at + 1; j < len(keys); j++ {
		if !isCommentKey(keys[j]) {
			out = append(out, anchor{key: keys[j], before: true})
		}
	}
	return out
}

// attachedComments holds the block comments that render next to a merged spec; anchorOf
// maps each such comment's key to that spec's key.
type attachedComments struct {
	before, after map[string][]DeclItem
	anchorOf      map[string]string
}

// attachComments binds every merged comment written inside a block to the first of its
// anchors the merged items keep as a spec of the same keyword. A comment none of whose
// anchors survives keeps its own merged position.
func attachComments(items []DeclItem, anchors map[string][]anchor, specBlock map[string]string) attachedComments {
	a := attachedComments{
		before:   make(map[string][]DeclItem),
		after:    make(map[string][]DeclItem),
		anchorOf: make(map[string]string),
	}
	for _, item := range items {
		if item.Kind != kindComment || item.Block == "" {
			continue
		}
		i := slices.IndexFunc(anchors[item.Key], func(at anchor) bool { return specBlock[at.key] == item.Block })
		if i < 0 {
			continue
		}
		at := anchors[item.Key][i]
		if at.before {
			a.before[at.key] = append(a.before[at.key], item)
		} else {
			a.after[at.key] = append(a.after[at.key], item)
		}
		a.anchorOf[item.Key] = at.key
	}
	return a
}

// place appends the block's members in order, each with the comments bound to it.
func (a attachedComments) place(out, members []DeclItem) []DeclItem {
	for _, member := range members {
		out = append(out, a.before[member.Key]...)
		out = append(out, member)
		out = append(out, a.after[member.Key]...)
	}
	return out
}

// commentStretch is a run of base's free comments between two declarations every input
// holds; from and to are their keys, "" for the start or the end of the file.
type commentStretch struct {
	from, to string
	keys     []string
	texts    []string
}

// commentSide is one side's view of the free comments: where its items sit and how often it
// writes each comment text anywhere in the file.
type commentSide struct {
	p   *ParsedAST
	pos map[string]int
	all textCounts
}

func newCommentSide(p *ParsedAST) commentSide {
	var texts []string
	for _, key := range p.DeclOrder {
		if item := p.Decls[key]; item.Kind == kindComment {
			texts = append(texts, commentText(item))
		}
	}
	return commentSide{p: p, pos: indexOf(p.DeclOrder), all: countTexts(texts)}
}

// commentConflicts reports every stretch of free comments both sides rewrite differently.
// Comments are keyed by their text, so a comment one side moves, or leaves alone while the
// declarations around it change, still matches base. An edit then reads as a deletion plus
// an addition, and two different edits of one comment merged clean with both texts, such
// as two //go:generate lines where each side had rewritten the one base held. So comments
// are also compared by place: between two declarations every input holds, a base comment
// both sides drop must be replaced by both with the same comments.
func commentConflicts(base, ours, theirs *ParsedAST) []Conflict {
	o, t := newCommentSide(ours), newCommentSide(theirs)
	var conflicts []Conflict
	for _, s := range commentStretches(base, o.pos, t.pos) {
		if conflict := stretchConflict(s, o, t); conflict != nil {
			conflicts = append(conflicts, *conflict)
		}
	}
	return conflicts
}

// commentStretches splits base's free comments at every declaration all inputs hold.
func commentStretches(base *ParsedAST, oursPos, theirsPos map[string]int) []commentStretch {
	var out []commentStretch
	current := commentStretch{}
	for _, key := range base.DeclOrder {
		item := base.Decls[key]
		_, inOurs := oursPos[key]
		_, inTheirs := theirsPos[key]
		switch {
		case item.Kind == kindComment:
			current.keys = append(current.keys, key)
			current.texts = append(current.texts, commentText(item))
		case inOurs && inTheirs:
			current.to = key
			if len(current.texts) > 0 {
				out = append(out, current)
			}
			current = commentStretch{from: key}
		}
	}
	if len(current.texts) > 0 {
		out = append(out, current)
	}
	return out
}

// stretchConflict reports a conflict when both sides drop one of the stretch's base
// comments and do not end with the same comments in its place. Two identical edits merge
// as one; a side that keeps every base comment of the stretch leaves the other's edit.
func stretchConflict(s commentStretch, ours, theirs commentSide) *Conflict {
	baseCounts := countTexts(s.texts)
	oursTexts, oursOK := ours.stretch(s)
	theirsTexts, theirsOK := theirs.stretch(s)
	oursDrop, oursAdd := ours.change(baseCounts, oursTexts, oursOK)
	theirsDrop, theirsAdd := theirs.change(baseCounts, theirsTexts, theirsOK)
	if !shareText(oursDrop, theirsDrop) {
		return nil
	}
	if oursOK && theirsOK && slices.Equal(oursAdd, theirsAdd) {
		return nil
	}
	return &Conflict{
		Symbol: s.keys[0],
		Kind:   kindComment,
		Reason: fmt.Sprintf("Conflicting edits to the comments between %s and %s", stretchEnd(s.from, "the start"), stretchEnd(s.to, "the end")),
		Ours:   commentBodies(oursTexts),
		Theirs: commentBodies(theirsTexts),
		Base:   commentBodies(s.texts),
	}
}

// stretch returns the comment texts the side writes between the stretch's declarations,
// and false when the side orders those declarations the other way round.
func (c commentSide) stretch(s commentStretch) ([]string, bool) {
	start, end := -1, len(c.p.DeclOrder)
	if s.from != "" {
		start = c.pos[s.from]
	}
	if s.to != "" {
		end = c.pos[s.to]
	}
	if start >= end {
		return nil, false
	}
	var texts []string
	for _, key := range c.p.DeclOrder[start+1 : end] {
		if item := c.p.Decls[key]; item.Kind == kindComment {
			texts = append(texts, commentText(item))
		}
	}
	return texts, true
}

// change returns the base comments the side drops from the stretch and the comments it
// writes there that base lacks. Where the side reordered the stretch's declarations, a
// base comment counts as dropped only when the side writes it nowhere.
func (c commentSide) change(baseCounts textCounts, texts []string, ok bool) ([]string, []string) {
	if !ok {
		return baseCounts.minus(c.all), nil
	}
	sideCounts := countTexts(texts)
	return baseCounts.minus(sideCounts), sideCounts.minus(baseCounts)
}

// commentText identifies a free comment by its text and the keyword of the block it is
// written in, "" at top level.
func commentText(item DeclItem) string {
	return item.Block + "\x00" + item.Body
}

func commentBodies(texts []string) string {
	bodies := make([]string, 0, len(texts))
	for _, text := range texts {
		_, body, _ := strings.Cut(text, "\x00")
		bodies = append(bodies, body)
	}
	return strings.Join(bodies, "\n\n")
}

func stretchEnd(key, fileEnd string) string {
	if key == "" {
		return fileEnd + " of the file"
	}
	return key
}

// textCounts counts how often each comment text occurs.
type textCounts map[string]int

func countTexts(texts []string) textCounts {
	counts := make(textCounts, len(texts))
	for _, text := range texts {
		counts[text]++
	}
	return counts
}

// minus returns, sorted, every text a holds more often than b, once per surplus occurrence.
func (a textCounts) minus(b textCounts) []string {
	var out []string
	for text, n := range a {
		for i := b[text]; i < n; i++ {
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return out
}

// shareText reports whether two sorted lists hold a common text.
func shareText(a, b []string) bool {
	for _, text := range a {
		if _, found := slices.BinarySearch(b, text); found {
			return true
		}
	}
	return false
}
