// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"sort"
	"strings"
	"text/template/parse"
)

// FileLeftDelim and FileRightDelim are shared by the file renderer and template inventory.
const (
	FileLeftDelim  = "<%"
	FileRightDelim = "%>"
)

// maxActionTemplateBranches bounds exhaustive enumeration to 4096 combinations.
const maxActionTemplateBranches = 12

// maxActionTemplateBytes bounds parsing, assembly and source mapping independently of lines.
const maxActionTemplateBytes = 1 << 20

// templateDirectivePlaceholder is reserved while inspecting unrendered scalar values.
const templateDirectivePlaceholder = "praetorTemplateValue"

// actionTemplate retains the real parser's tree, source coordinates and stable branch ids.
type actionTemplate struct {
	source   actionSource
	root     *parse.ListNode
	branches map[parse.Node]int
}

// ScanTemplateActionUses enumerates parser branches and reads every assembly with ScanActionUses.
// File delimiters are the default, matching RenderFile. Pass "", "" for Render's default
// Go delimiters: the renderer selects by entry point, never by inspecting delimiter strings.
// A source span whose value or trailing comment varies between choices is refused.
func ScanTemplateActionUses(workflow string, maxLines int, delimiters ...string) ([]string, []ActionUse, error) {
	tmpl, err := parseActionTemplate(workflow, maxLines, delimiters)
	if err != nil {
		return nil, nil, err
	}
	if templateIsPlainText(tmpl.root, workflow) {
		return ScanActionUses(workflow, maxLines)
	}
	seen := make(map[[2]int]int)
	var uses []ActionUse
	for choice := 0; choice < 1<<len(tmpl.branches); choice++ {
		found, err := tmpl.scanCombination(choice, maxLines)
		if err == nil {
			uses, err = mergeTemplateActionUses(uses, seen, found)
		}
		if err != nil {
			return nil, nil, fmt.Errorf("unsupported uses template combination %0*b (1=body, 0=else/skip): %w", len(tmpl.branches), choice, err)
		}
	}
	sort.Slice(uses, func(i, j int) bool { return uses[i].Start < uses[j].Start })
	return tmpl.source.lines, uses, nil
}

// mergeTemplateActionUses retains each source span once and refuses contradictory branch metadata.
func mergeTemplateActionUses(uses []ActionUse, seen map[[2]int]int, found []ActionUse) ([]ActionUse, error) {
	for _, use := range found {
		span := [2]int{use.Start, use.End}
		if at, exists := seen[span]; exists {
			if uses[at].Value != use.Value || uses[at].Ref != use.Ref {
				return nil, fmt.Errorf("line %d: unsupported uses template: inconsistent uses across combinations at original bytes %d:%d", use.Line+1, use.Start, use.End)
			}
			continue
		}
		seen[span] = len(uses)
		uses = append(uses, use)
	}
	return uses, nil
}

// parseActionTemplate delegates all tokenisation, comments and trim handling to Go's parser.
func parseActionTemplate(workflow string, maxLines int, delimiters []string) (*actionTemplate, error) {
	left, right := FileLeftDelim, FileRightDelim
	if len(delimiters) != 0 && len(delimiters) != 2 {
		return nil, fmt.Errorf("unsupported uses template delimiters: want zero or two strings")
	}
	if len(delimiters) == 2 {
		left, right = delimiters[0], delimiters[1]
	}
	source, err := templateActionSource(workflow, maxLines)
	if err != nil {
		return nil, err
	}
	tree := parse.New("action template")
	tree.Mode = parse.SkipFuncCheck
	trees := make(map[string]*parse.Tree)
	if _, err := tree.Parse(workflow, left, right, trees); err != nil {
		return nil, fmt.Errorf("unsupported uses template parse: %w", err)
	}
	if len(trees) != 1 || trees[tree.Name] != tree {
		return nil, fmt.Errorf("unsupported uses template directive: define or block")
	}
	branches, err := inspectActionTemplate(tree.Root)
	if err != nil {
		return nil, err
	}
	return &actionTemplate{source: source, root: tree.Root, branches: branches}, nil
}

// templateIsPlainText recognises the parser's unchanged text result without scanning delimiters.
func templateIsPlainText(root *parse.ListNode, text string) bool {
	if len(root.Nodes) == 0 {
		return text == ""
	}
	if len(root.Nodes) != 1 {
		return false
	}
	node, ok := root.Nodes[0].(*parse.TextNode)
	return ok && string(node.Text) == text
}

// scanCombination assembles one choice, checks YAML and maps every result to original bytes.
func (tmpl *actionTemplate) scanCombination(choice, maxLines int) ([]ActionUse, error) {
	pieces, err := tmpl.pieces(choice)
	if err != nil {
		return nil, err
	}
	assembly := assembleTemplatePieces(pieces, nil)
	omitted, err := templateBlockOmissions(assembly)
	if err != nil {
		return nil, err
	}
	assembly = assembleTemplatePieces(pieces, omitted)
	_, uses, err := ScanActionUses(assembly.text, maxLines, templateDirectivePlaceholder)
	if err != nil {
		return nil, fmt.Errorf("assembled YAML: %w", err)
	}
	for i := 0; i < len(uses) && i < maxActionYAMLNodes; i++ {
		uses[i], err = tmpl.mapUse(assembly, uses[i])
		if err != nil {
			return nil, err
		}
	}
	return uses, nil
}

// templateActionSource enforces line, byte and reserved-marker bounds before invoking the parsers.
func templateActionSource(workflow string, maxLines int) (actionSource, error) {
	if len(workflow) > maxActionTemplateBytes {
		return actionSource{}, fmt.Errorf("unsupported uses template: exceeds %d bytes", maxActionTemplateBytes)
	}
	if strings.Contains(workflow, templateDirectivePlaceholder) {
		return actionSource{}, fmt.Errorf("unsupported uses template: reserved placeholder present")
	}
	source := newActionSource(workflow)
	if len(source.lines) > maxLines {
		return actionSource{}, fmt.Errorf("workflow has %d lines, want at most %d", len(source.lines), maxLines)
	}
	return source, nil
}
