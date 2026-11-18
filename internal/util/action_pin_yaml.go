// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"
)

// maxActionYAMLNodes bounds the iterative node walk without following alias edges.
const maxActionYAMLNodes = 1000000

// maxActionScalarBytes bounds source-range recovery for encoded or multiline scalars.
const maxActionScalarBytes = 65536

// walkActionUses visits mapping pairs in a bounded, nonrecursive YAML tree walk.
func walkActionUses(source actionSource, root *yaml.Node) ([]ActionUse, error) {
	stack := []*yaml.Node{root}
	var uses []ActionUse
	for visited := 0; len(stack) > 0 && visited < maxActionYAMLNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		found, err := mappingActionUses(source, node)
		if err != nil {
			return nil, err
		}
		uses = append(uses, found...)
		if len(stack)+len(node.Content) > maxActionYAMLNodes {
			return nil, fmt.Errorf("workflow exceeds %d YAML nodes", maxActionYAMLNodes)
		}
		stack = append(stack, node.Content...)
	}
	if len(stack) != 0 {
		return nil, fmt.Errorf("workflow exceeds %d YAML nodes", maxActionYAMLNodes)
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].Start < uses[j].Start })
	return uses, nil
}

// mappingActionUses reads every uses key in a mapping, refusing any non-scalar mapping key.
func mappingActionUses(source actionSource, node *yaml.Node) ([]ActionUse, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	var uses []ActionUse
	for i := 0; i+1 < len(node.Content) && i < maxActionYAMLNodes; i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if err := actionScalarTag(key, "mapping key"); err != nil {
			return nil, err
		}
		if key.Value != "uses" {
			continue
		}
		use, err := parsedActionUse(source, value, node.LineComment)
		if err != nil {
			return nil, err
		}
		uses = append(uses, use)
	}
	return uses, nil
}

// actionScalarTag allows scalar text and explicit string tags, refusing decoded tag semantics.
func actionScalarTag(node *yaml.Node, role string) error {
	if node.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: unsupported uses %s: expected scalar, got YAML kind %d", node.Line, role, node.Kind)
	}
	if node.Style&yaml.TaggedStyle != 0 && node.ShortTag() != "!!str" {
		return fmt.Errorf("line %d: unsupported uses %s explicit tag %q", node.Line, role, node.Tag)
	}
	return nil
}

// parsedActionUse keeps the decoded value, source span and actual YAML comment together.
func parsedActionUse(source actionSource, value *yaml.Node, flowComment string) (ActionUse, error) {
	if err := actionScalarTag(value, "value"); err != nil {
		return ActionUse{}, err
	}
	start, err := source.offset(value.Line, value.Column)
	if err != nil {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses source: %w", value.Line, err)
	}
	end, err := actionScalarEnd(source.text, start, value)
	if err != nil {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses source: %w", value.Line, err)
	}
	comment := value.LineComment
	if comment == "" {
		comment = flowComment
	}
	ref := value.Value + actionCommentGap(source, value.Line, comment)
	pin, shaPinned := ParseSHAPin(ref)
	if value.Value != pin.Action+"@"+pin.SHA {
		pin, shaPinned = PinnedAction{}, false
	}
	return ActionUse{Line: value.Line - 1, Column: value.Column, Start: start, End: end,
		Value: value.Value, Ref: ref, Pin: pin, SHAPinned: shaPinned, Pinned: shaPinned && pin.Release != "", source: value}, nil
}

// actionSource is a workflow split at the line breaks yaml/v3 counts, so a parsed line number
// indexes lines and starts whatever the file's line endings are (LF, CRLF or bare CR).
type actionSource struct {
	text string
	// lines keeps each line without its LF; a CRLF line keeps its CR.
	lines []string
	// starts and ends are the byte offsets where each line begins and where its break begins.
	starts, ends []int
}

// yamlBreakWidth returns the byte width of the line break text begins with, or 0: the set
// yaml/v3's scanner counts as one line (CR LF, CR, LF, NEL, LS and PS).
func yamlBreakWidth(text string) int {
	if strings.HasPrefix(text, "\r\n") {
		return 2
	}
	if text == "" {
		return 0
	}
	if text[0] == '\r' || text[0] == '\n' {
		return 1
	}
	if r, width := utf8.DecodeRuneInString(text); r == '\u0085' || r == '\u2028' || r == '\u2029' {
		return width
	}
	return 0
}

// newActionSource is the one line-offset builder shared by the tree reader and the template rewrite.
func newActionSource(text string) actionSource {
	source := actionSource{text: text, starts: []int{0}}
	for i := 0; i < len(text); {
		width := yamlBreakWidth(text[i:])
		if width == 0 {
			i++
			continue
		}
		source.ends = append(source.ends, i)
		i += width
		source.starts = append(source.starts, i)
	}
	source.ends = append(source.ends, len(text))
	for i, start := range source.starts {
		next := len(text)
		if i+1 < len(source.starts) {
			next = source.starts[i+1]
		}
		source.lines = append(source.lines, strings.TrimSuffix(text[start:next], "\n"))
	}
	return source
}

// offset converts YAML rune coordinates into an absolute byte offset, or refuses a coordinate
// outside the lines read, so a source the parser and this reader disagree on never panics.
func (s actionSource) offset(line, column int) (int, error) {
	if line < 1 || line > len(s.starts) || column < 1 {
		return 0, fmt.Errorf("source coordinate line %d column %d is outside the %d lines read", line, column, len(s.starts))
	}
	offset, end := s.starts[line-1], s.ends[line-1]
	for col := 1; col < column && offset < end; col++ {
		_, width := utf8.DecodeRuneInString(s.text[offset:end])
		offset += width
	}
	return offset, nil
}

// actionScalarEnd asks YAML to verify encoded scalar prefixes; plain references need no decoding.
func actionScalarEnd(workflow string, start int, value *yaml.Node) (int, error) {
	source := workflow[start:]
	if value.Style == 0 && value.Anchor == "" && strings.HasPrefix(source, value.Value) {
		return start + len(value.Value), nil
	}
	return encodedActionScalarEnd(source, start, value.Value)
}

// encodedActionScalarEnd finds a complete scalar prefix by decoding candidates at YAML boundaries.
func encodedActionScalarEnd(source string, start int, value string) (int, error) {
	for size := 1; size <= len(source) && size <= maxActionScalarBytes; size++ {
		if size < len(source) && yamlBreakWidth(source[size:]) == 0 && !strings.ContainsRune(" \t,]}#", rune(source[size])) {
			continue
		}
		if matchesActionScalar(source[:size], value) {
			return start + size, nil
		}
	}
	return 0, fmt.Errorf("scalar source exceeds %d bytes or cannot be isolated", maxActionScalarBytes)
}

// matchesActionScalar verifies a candidate span with the same YAML decoder as the document reader.
func matchesActionScalar(source, value string) bool {
	var scalar yaml.Node
	if err := DecodeYAMLDocument([]byte(source), &scalar, YAMLDocumentOptions{}); err != nil {
		return false
	}
	return len(scalar.Content) == 1 && scalar.Content[0].Kind == yaml.ScalarNode && scalar.Content[0].Value == value
}

// actionCommentGap preserves the exact whitespace before a parsed trailing comment.
func actionCommentGap(source actionSource, line int, comment string) string {
	if comment == "" {
		return ""
	}
	text := source.lines[line-1]
	index := strings.LastIndex(text, comment)
	if index < 0 {
		return ""
	}
	prefix := text[:index]
	return prefix[len(strings.TrimRight(prefix, " \t")):] + strings.TrimRight(comment, " \t\r")
}
