// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"slices"
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
		if key.Kind != yaml.ScalarNode {
			return nil, fmt.Errorf("line %d: unsupported uses mapping key: expected scalar, got YAML kind %d", key.Line, key.Kind)
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

// parsedActionUse keeps the decoded value, source span and actual YAML comment together.
func parsedActionUse(source actionSource, value *yaml.Node, flowComment string) (ActionUse, error) {
	if value.Kind != yaml.ScalarNode {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses value: expected scalar, got YAML kind %d", value.Line, value.Kind)
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

// content returns line index without any line break.
func (s actionSource) content(index int) string {
	return s.text[s.starts[index]:s.ends[index]]
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

// templateDirectivePlaceholder replaces inline template value directives during single-line YAML checks.
const templateDirectivePlaceholder = "praetorTemplateValue"

// templateLineState tracks multiline template comments and structural YAML block scalar bodies.
type templateLineState struct {
	commentClose string
	blockIndent  int
}

// ScanTemplateActionUses parses YAML first, then reads unrendered templates through a closed
// list of single-line block shapes, refusing any other line by name.
func ScanTemplateActionUses(workflow string, maxLines int) ([]string, []ActionUse, error) {
	lines, uses, err := ScanActionUses(workflow, maxLines)
	if err == nil {
		return lines, uses, nil
	}
	source := newActionSource(workflow)
	if len(source.lines) > maxLines {
		return nil, nil, err
	}
	uses, parseErr := scanTemplateActionLines(source, maxLines)
	if parseErr != nil {
		return nil, nil, parseErr
	}
	return source.lines, uses, nil
}

// scanTemplateActionLines walks an unrendered template line by line against the closed list of
// accepted block YAML and template directive shapes.
func scanTemplateActionLines(source actionSource, maxLines int) ([]ActionUse, error) {
	state := templateLineState{blockIndent: -1}
	var uses []ActionUse
	for i := 0; i < len(source.lines) && i < maxLines; i++ {
		use, hasUse, err := stepTemplateActionLine(source, i, &state)
		if err != nil {
			return nil, err
		}
		if hasUse {
			uses = append(uses, use)
		}
	}
	if state.commentClose != "" {
		return nil, fmt.Errorf("line %d: unsupported uses line in unrendered template: unclosed template comment", len(source.lines))
	}
	return uses, nil
}

// stepTemplateActionLine classifies one template line against the closed list of accepted shapes.
func stepTemplateActionLine(source actionSource, index int, state *templateLineState) (ActionUse, bool, error) {
	raw := source.content(index)
	neutralised, nextClose, ok := neutraliseTemplateLine(raw, state.commentClose)
	if !ok {
		return ActionUse{}, false, fmt.Errorf("line %d: unsupported uses line in unrendered template: unclosed template directive", index+1)
	}
	state.commentClose = nextClose
	if isIgnorableTemplateLine(neutralised, state) {
		return ActionUse{}, false, nil
	}
	if strings.Contains(raw, "uses") {
		use, err := templateActionLine(source, index)
		use.Start += source.starts[index]
		use.End += source.starts[index]
		return use, err == nil, err
	}
	blockIndent, err := templateNonUsesLine(neutralised, raw, index)
	state.blockIndent = blockIndent
	return ActionUse{}, false, err
}

// isIgnorableTemplateLine reports whether a directive-neutralised line is blank, a comment,
// inside an active YAML block scalar body, or a YAML document boundary.
func isIgnorableTemplateLine(neutralised string, state *templateLineState) bool {
	trimmed := strings.TrimSpace(neutralised)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return true
	}
	if inTemplateBlockScalar(neutralised, state) {
		return true
	}
	return trimmed == "---" || trimmed == "..."
}

// inTemplateBlockScalar reports whether neutralised is indented inside an active block scalar body,
// closing the block scalar state once indentation returns to or above the owning key column.
func inTemplateBlockScalar(neutralised string, state *templateLineState) bool {
	if state.blockIndent < 0 {
		return false
	}
	indent := len(neutralised) - len(strings.TrimLeft(neutralised, " "))
	if indent > state.blockIndent {
		return true
	}
	state.blockIndent = -1
	return false
}

// templateNonUsesLine verifies that a non-uses template line decodes as a single-line block
// mapping entry or sequence scalar without a decoded uses key, returning any new block scalar indent.
func templateNonUsesLine(neutralised, raw string, index int) (int, error) {
	var root yaml.Node
	if err := DecodeYAMLDocument([]byte(neutralised), &root, YAMLDocumentOptions{}); err != nil {
		return -1, fmt.Errorf("line %d: unsupported uses line in unrendered template: %w", index+1, err)
	}
	found, err := walkActionUses(newActionSource(neutralised), &root)
	if err != nil {
		return -1, fmt.Errorf("line %d: unsupported uses line in unrendered template: %w", index+1, err)
	}
	blockIndent, ok := templateBlockNode(&root, neutralised)
	if len(found) > 0 || !ok {
		return -1, fmt.Errorf("line %d: unsupported uses-like form %q in unrendered template: only plain block uses: lines supported", index+1, strings.TrimSpace(raw))
	}
	return blockIndent, nil
}

// templateBlockNode checks that root is a single-line block sequence scalar or block mapping
// entry with a scalar key and scalar value, returning the key indentation for block scalar headers.
func templateBlockNode(root *yaml.Node, line string) (int, bool) {
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return -1, false
	}
	node := root.Content[0]
	if node.Kind == yaml.SequenceNode {
		if node.Style != 0 || node.Anchor != "" || len(node.Content) != 1 {
			return -1, false
		}
		node = node.Content[0]
		if node.Kind == yaml.ScalarNode {
			return -1, isPlainTemplateScalar(node)
		}
	}
	return templateMappingNode(node, line)
}

// isPlainTemplateScalar reports whether a sequence item scalar is a non-empty inline scalar without anchors.
func isPlainTemplateScalar(node *yaml.Node) bool {
	return node.Style != yaml.LiteralStyle && node.Style != yaml.FoldedStyle &&
		node.Anchor == "" && node.ShortTag() != "!!null" && node.Value != ""
}

// templateMappingNode checks a single block mapping pair with an inline scalar key and scalar value.
func templateMappingNode(node *yaml.Node, line string) (int, bool) {
	trimmed := strings.TrimLeft(strings.TrimPrefix(strings.TrimLeft(line, " \t"), "-"), " \t")
	if node.Kind != yaml.MappingNode || node.Style != 0 || node.Anchor != "" || len(node.Content) != 2 || strings.HasPrefix(trimmed, "?") {
		return -1, false
	}
	key, value := node.Content[0], node.Content[1]
	if !isTemplateMappingKey(key) || value.Kind != yaml.ScalarNode || value.Anchor != "" {
		return -1, false
	}
	if value.Style == yaml.LiteralStyle || value.Style == yaml.FoldedStyle {
		return key.Column - 1, true
	}
	return -1, true
}

// isTemplateMappingKey reports whether key is a non-empty, unanchored scalar key without template directives.
func isTemplateMappingKey(key *yaml.Node) bool {
	return key.Kind == yaml.ScalarNode && key.Anchor == "" && key.ShortTag() != "!!null" &&
		key.Value != "" && key.Value != "uses" && !strings.Contains(key.Value, templateDirectivePlaceholder)
}

// neutraliseTemplateLine strips template comments and replaces inline directives with a scalar placeholder.
func neutraliseTemplateLine(line, commentClose string) (string, string, bool) {
	var out strings.Builder
	rest := line
	for steps := 0; rest != "" && steps < maxActionScalarBytes; steps++ {
		if commentClose != "" {
			after, closed := closeTemplateComment(rest, commentClose)
			if !closed {
				return out.String(), commentClose, true
			}
			rest, commentClose = after, ""
			continue
		}
		var ok bool
		rest, commentClose, ok = stepTemplateDirective(&out, rest)
		if !ok {
			return "", "", false
		}
	}
	return out.String(), commentClose, true
}

// closeTemplateComment advances past the closing delimiter of a template block comment on rest.
func closeTemplateComment(rest, closeDelim string) (string, bool) {
	closeIdx := strings.Index(rest, closeDelim)
	if closeIdx < 0 {
		return "", false
	}
	before := strings.TrimRight(strings.TrimSuffix(rest[:closeIdx], "-"), " \t")
	if !strings.HasSuffix(before, "*/") {
		return "", false
	}
	return rest[closeIdx+len(closeDelim):], true
}

// stepTemplateDirective processes the next template directive in rest and writes the neutralised prefix to out.
func stepTemplateDirective(out *strings.Builder, rest string) (string, string, bool) {
	openIdx, openDelim, closeDelim := nextTemplateDirective(rest)
	if openIdx < 0 {
		out.WriteString(rest)
		return "", "", true
	}
	out.WriteString(rest[:openIdx])
	body := rest[openIdx+len(openDelim):]
	if strings.HasPrefix(strings.TrimLeft(strings.TrimPrefix(body, "-"), " \t"), "/*") {
		return body, closeDelim, true
	}
	closeIdx := strings.Index(body, closeDelim)
	if closeIdx < 0 {
		return "", "", false
	}
	inner := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(body[:closeIdx], "-"), "-"))
	after := body[closeIdx+len(closeDelim):]
	if !shouldStripTemplateDirective(out.String(), inner, after) {
		out.WriteString(templateDirectivePlaceholder)
	}
	return after, "", true
}

// templateControlKeywords are the Go template control keywords that may lead an indented line.
var templateControlKeywords = []string{"if", "else", "end", "range", "with", "define", "template", "block"}

// shouldStripTemplateDirective reports whether a directive stands alone on the line or leads indentation as a control branch.
func shouldStripTemplateDirective(prefix, inner, after string) bool {
	if strings.TrimSpace(prefix) != "" {
		return false
	}
	if strings.TrimSpace(after) == "" {
		return true
	}
	word, _, _ := strings.Cut(inner, " ")
	return slices.Contains(templateControlKeywords, word) && (after[0] == ' ' || after[0] == '\t')
}

// nextTemplateDirective locates the earliest Go template opening delimiter ("<%" or non-expression "{{") in text.
func nextTemplateDirective(text string) (int, string, string) {
	for i := 0; i+1 < len(text) && i < maxActionScalarBytes; i++ {
		if text[i] == '<' && text[i+1] == '%' {
			return i, "<%", "%>"
		}
		if text[i] == '{' && text[i+1] == '{' && (i == 0 || text[i-1] != '$') {
			return i, "{{", "}}"
		}
	}
	return -1, "", ""
}

// templateActionLine requires a plain block key and delegates its scalar parsing to YAML. It refuses
// a header whose value is null or empty, carries a template directive, or continues on the next line,
// rather than read an empty reference that would drop the action from the inventory without an error.
func templateActionLine(source actionSource, index int) (ActionUse, error) {
	line := source.content(index)
	if !strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(line), "- "), "uses:") {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses-like form %q in unrendered template: only plain block uses: lines supported", index+1, strings.TrimSpace(line))
	}
	if openIdx, _, _ := nextTemplateDirective(line); openIdx >= 0 {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: template directives are not supported in uses values", index+1)
	}
	_, found, err := ScanActionUses(line, 1)
	if err != nil {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: %w", index+1, err)
	}
	if len(found) != 1 {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: expected one scalar", index+1)
	}
	if found[0].Value == "" || found[0].source.Tag == "!!null" || templateContinues(source, index, strings.Index(line, "uses:")) {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: the value is null or empty, or continues on the next line: write the whole reference on the uses: line", index+1)
	}
	found[0].Line = index
	return found[0], nil
}

// templateContinues reports whether the next non-blank, non-comment line after a uses header is
// indented deeper than its key, which continues the header's value on that line.
func templateContinues(source actionSource, index, keyIndent int) bool {
	for next := index + 1; next < len(source.lines) && next-index <= maxActionYAMLNodes; next++ {
		text := source.content(next)
		trimmed := strings.TrimSpace(text)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		return len(text)-len(strings.TrimLeft(text, " ")) > keyIndent
	}
	return false
}
