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
func walkActionUses(workflow string, lines []string, root *yaml.Node) ([]ActionUse, error) {
	stack := []*yaml.Node{root}
	var uses []ActionUse
	for visited := 0; len(stack) > 0 && visited < maxActionYAMLNodes; visited++ {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		found, err := mappingActionUses(workflow, lines, node)
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

// mappingActionUses reads every uses key in a mapping, including flow mapping entries.
func mappingActionUses(workflow string, lines []string, node *yaml.Node) ([]ActionUse, error) {
	if node.Kind != yaml.MappingNode {
		return nil, nil
	}
	var uses []ActionUse
	for i := 0; i+1 < len(node.Content) && i < maxActionYAMLNodes; i += 2 {
		key, value := node.Content[i], node.Content[i+1]
		if key.Kind != yaml.ScalarNode || key.Value != "uses" {
			continue
		}
		use, err := parsedActionUse(workflow, lines, value, node.LineComment)
		if err != nil {
			return nil, err
		}
		uses = append(uses, use)
	}
	return uses, nil
}

// parsedActionUse keeps the decoded value, source span and actual YAML comment together.
func parsedActionUse(workflow string, lines []string, value *yaml.Node, flowComment string) (ActionUse, error) {
	if value.Kind != yaml.ScalarNode {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses value: expected scalar, got YAML kind %d", value.Line, value.Kind)
	}
	start := actionSourceOffset(lines, value.Line, value.Column)
	end, err := actionScalarEnd(workflow, start, value)
	if err != nil {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses source: %w", value.Line, err)
	}
	comment := value.LineComment
	if comment == "" {
		comment = flowComment
	}
	ref := value.Value + actionCommentGap(lines, value.Line, comment)
	pin, shaPinned := ParseSHAPin(ref)
	if value.Value != pin.Action+"@"+pin.SHA {
		pin, shaPinned = PinnedAction{}, false
	}
	return ActionUse{Line: value.Line - 1, Column: value.Column, Start: start, End: end,
		Value: value.Value, Ref: ref, Pin: pin, SHAPinned: shaPinned, Pinned: shaPinned && pin.Release != "", source: value}, nil
}

// actionSourceOffset converts YAML rune coordinates into an absolute byte offset.
func actionSourceOffset(lines []string, line, column int) int {
	offset := 0
	for i := 0; i < line-1 && i < len(lines); i++ {
		offset += len(lines[i]) + 1
	}
	text := lines[line-1]
	for i, col := 0, 1; i < len(text) && col < column; col++ {
		_, width := utf8.DecodeRuneInString(text[i:])
		i += width
		offset += width
	}
	return offset
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
		if size < len(source) && !strings.ContainsRune(" \t\r\n,]}#", rune(source[size])) {
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
func actionCommentGap(lines []string, line int, comment string) string {
	if comment == "" {
		return ""
	}
	text := lines[line-1]
	index := strings.LastIndex(text, comment)
	if index < 0 {
		return ""
	}
	prefix := text[:index]
	return prefix[len(strings.TrimRight(prefix, " \t")):] + strings.TrimRight(comment, " \t\r")
}

// ScanTemplateActionUses parses YAML first, then refuses ambiguous uses-like template lines.
// Unrendered Go templates support only plain block uses: lines parsed independently as YAML.
func ScanTemplateActionUses(workflow string, maxLines int) ([]string, []ActionUse, error) {
	lines, uses, err := ScanActionUses(workflow, maxLines)
	if err == nil {
		return lines, uses, nil
	}
	lines = strings.Split(workflow, "\n")
	if len(lines) > maxLines {
		return nil, nil, err
	}
	uses = nil
	for i := 0; i < len(lines) && i < maxLines; i++ {
		line := strings.TrimSpace(lines[i])
		if !strings.Contains(line, "uses") || strings.HasPrefix(line, "#") {
			continue
		}
		use, parseErr := templateActionLine(lines[i], i)
		if parseErr != nil {
			return nil, nil, parseErr
		}
		offset := actionSourceOffset(lines, i+1, 1)
		use.Start += offset
		use.End += offset
		uses = append(uses, use)
	}
	return lines, uses, nil
}

// templateActionLine requires a plain block key and delegates its scalar parsing to YAML.
func templateActionLine(line string, index int) (ActionUse, error) {
	if !strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(line), "- "), "uses:") {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses-like form %q in unrendered template: only plain block uses: lines supported", index+1, strings.TrimSpace(line))
	}
	_, found, err := ScanActionUses(line, 1)
	if err != nil {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: %w", index+1, err)
	}
	if len(found) != 1 {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses line in unrendered template: expected one scalar", index+1)
	}
	found[0].Line = index
	return found[0], nil
}
