// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package util

import (
	"fmt"
	"sort"
	"strings"
	"text/template/parse"
	"unicode/utf8"
)

// templateTextSpan maps unchanged assembled text to the parser's original text position.
type templateTextSpan struct{ start, end, original int }

// templatePrintSlot records a printing directive's placeholder and piece index.
type templatePrintSlot struct {
	start, end, piece int
	action            *parse.ActionNode
}

// templateAssembly retains emitted text, literal source spans and printing positions.
type templateAssembly struct {
	text  string
	spans []templateTextSpan
	slots []templatePrintSlot
}

// assembleTemplatePieces concatenates adjacent text as the engine would, preserving text positions.
func assembleTemplatePieces(pieces []templatePiece, omitted map[int]bool) templateAssembly {
	var out strings.Builder
	var assembly templateAssembly
	for i := 0; i < len(pieces) && i < maxActionYAMLNodes; i++ {
		piece := pieces[i]
		start := out.Len()
		if piece.action == nil {
			out.WriteString(piece.text)
			assembly.spans = append(assembly.spans, templateTextSpan{start: start, end: out.Len(), original: piece.original})
			continue
		}
		if !omitted[i] {
			out.WriteString(templateDirectivePlaceholder)
			assembly.slots = append(assembly.slots, templatePrintSlot{start: start, end: out.Len(), piece: i, action: piece.action})
		}
	}
	assembly.text = out.String()
	return assembly
}

// templateBlockOmissions permits only proven block functions as stand-alone printing directives.
func templateBlockOmissions(assembly templateAssembly) (map[int]bool, error) {
	omitted := make(map[int]bool)
	source := newActionSource(assembly.text)
	for i := 0; i < len(assembly.slots) && i < maxActionYAMLNodes; i++ {
		slot := assembly.slots[i]
		line := sort.Search(len(source.starts), func(i int) bool { return source.starts[i] > slot.start }) - 1
		if strings.TrimSpace(source.text[source.starts[line]:slot.start]) != "" || strings.TrimSpace(source.text[slot.end:source.ends[line]]) != "" {
			continue
		}
		if !provenTemplateBlock(slot.action.Pipe) {
			return nil, fmt.Errorf("unsupported uses template stand-alone printing directive %q", slot.action.Pipe.String())
		}
		omitted[slot.piece] = true
	}
	return omitted, nil
}

// provenTemplateBlock accepts only a direct, argument-free call to a tested block function.
func provenTemplateBlock(pipe *parse.PipeNode) bool {
	if len(pipe.Decl) != 0 || len(pipe.Cmds) != 1 || len(pipe.Cmds[0].Args) != 1 {
		return false
	}
	name, ok := pipe.Cmds[0].Args[0].(*parse.IdentifierNode)
	if !ok {
		return false
	}
	switch name.Ident {
	case "hostedGateOn", "hostedGateScheduledOn", "hostedGateConcurrency", "hostedGateDraftStep":
		return true
	default:
		return false
	}
}

// mapUse refuses noncontiguous or mismatched original scalar bytes instead of guessing a range.
func (tmpl *actionTemplate) mapUse(assembly templateAssembly, use ActionUse) (ActionUse, error) {
	if use.Value == "" || use.source.ShortTag() == "!!null" {
		return ActionUse{}, fmt.Errorf("line %d: unsupported uses template value: null or empty scalar", use.Line+1)
	}
	for i := 0; i < len(assembly.spans) && i < maxActionYAMLNodes; i++ {
		span := assembly.spans[i]
		if use.Start < span.start || use.End > span.end {
			continue
		}
		start, end, err := tmpl.originalUseRange(assembly.text, span, use)
		if err != nil {
			return ActionUse{}, err
		}
		line := sort.Search(len(tmpl.source.starts), func(i int) bool { return tmpl.source.starts[i] > start }) - 1
		use.Start, use.End, use.Line = start, end, line
		use.Column = utf8.RuneCountInString(tmpl.source.text[tmpl.source.starts[line]:start]) + 1
		return use, nil
	}
	return ActionUse{}, fmt.Errorf("unsupported uses template source mapping: scalar crosses text nodes")
}

// originalUseRange verifies parser positions against both the emitted bytes and decoded scalar.
func (tmpl *actionTemplate) originalUseRange(text string, span templateTextSpan, use ActionUse) (int, int, error) {
	start := span.original + use.Start - span.start
	end := span.original + use.End - span.start
	if start < 0 || end > len(tmpl.source.text) || start > end {
		return 0, 0, fmt.Errorf("unsupported uses template source mapping: range outside original text")
	}
	original := tmpl.source.text[start:end]
	if original != text[use.Start:use.End] || !matchesActionScalar(original, use.Value) {
		return 0, 0, fmt.Errorf("unsupported uses template source mapping: scalar bytes mismatch")
	}
	return start, end, nil
}
