package agentcontext

import (
	"fmt"
	"strings"
)

// lineAction is what filterLine decides for one line outside a fence.
type lineAction int

const (
	keepLine lineAction = iota
	// dropLine removes the line alone: a paragraph line left without text.
	dropLine
	// dropItem removes a list item with its continuation lines.
	dropItem
)

// filterLines drops what asks a read-only run to mutate, line by line: fenced command lines
// (filterFence), table clauses (filterTableRow), list items, and sentences of rule leads and
// paragraphs (filterLine). A dropped list item takes its continuation lines with it, and the
// first one dropped from a list leaves readOnlyItemNote in its place (dropNotes).
func filterLines(lines []string) []string {
	out := make([]string, 0, len(lines))
	notes := dropNotes{indent: -1}
	dropIndent := -1
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		marker, fence := fenceOpen(line)
		if dropIndent >= 0 && continuesItem(line, dropIndent) {
			if fence {
				i, _ = fenceClose(lines, i, marker)
			}
			continue
		}
		dropIndent = -1
		notes.leave(line)
		if fence {
			end, closed := fenceClose(lines, i, marker)
			out = append(out, filterFence(lines[i:end+1], closed)...)
			i = end
			continue
		}
		kept, action := filterLine(line)
		switch action {
		case keepLine:
			out = append(out, kept)
		case dropItem:
			dropIndent = indentOf(line)
			out = notes.add(out, line)
		}
	}
	return out
}

// dropNotes places one readOnlyItemNote per list that loses items, at the first dropped item,
// so a rule whose steps were dropped says its duties bind write runs only, wherever it sits.
type dropNotes struct{ indent int }

// add appends the note for the dropped item line unless its list already has one.
func (n *dropNotes) add(out []string, line string) []string {
	if n.indent >= 0 {
		return out
	}
	n.indent = indentOf(line)
	prefix := listMarker.FindString(line)
	return append(out, prefix+readOnlyItemNote)
}

// leave ends the noted list at a nonblank line indented less than its items.
func (n *dropNotes) leave(line string) {
	if n.indent >= 0 && !isBlank(line) && indentOf(line) < n.indent {
		n.indent = -1
	}
}

// filterLine renames the verification gate's command and judges one line outside a fence. A
// table row loses the clauses that ask for a mutating command; a list item that asks for one
// is dropped; a rule lead or paragraph loses the sentences that do.
func filterLine(line string) (string, lineAction) {
	line = renameGate(line)
	if len(mutatingAt(line)) == 0 {
		return line, keepLine
	}
	if strings.HasPrefix(strings.TrimSpace(line), "|") {
		return filterTableRow(line), keepLine
	}
	prefix, body, item := splitProse(line)
	if item {
		if textAllowed(body) {
			return line, keepLine
		}
		return "", dropItem
	}
	kept := keepSentences(body)
	if kept == "" && strings.TrimSpace(prefix) == "" {
		return "", dropLine
	}
	return strings.TrimRight(prefix+kept, " "), keepLine
}

// renameGate replaces the verification gate's command with its name.
func renameGate(line string) string {
	line = strings.ReplaceAll(line, "`make verify-all`", gateName)
	return strings.ReplaceAll(line, "make verify-all", gateName)
}

// splitProse splits a prose line into its prefix (indent, list marker, bold rule title) and its
// body; item reports a list item without a bold title, which is dropped whole.
func splitProse(line string) (prefix, body string, item bool) {
	loc := listMarker.FindStringIndex(line)
	if loc == nil {
		trimmed := strings.TrimLeft(line, " \t")
		return line[:len(line)-len(trimmed)], trimmed, false
	}
	prefix, body = line[:loc[1]], line[loc[1]:]
	if title := boldTitle.FindString(body); title != "" {
		return prefix + title, body[len(title):], false
	}
	return prefix, body, true
}

// keepSentences returns body without the sentences that ask for a mutating command.
func keepSentences(body string) string {
	var b strings.Builder
	for _, sentence := range segments(body, ". ") {
		if textAllowed(sentence) {
			b.WriteString(sentence)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

// filterTableRow drops, in every cell, the clauses that ask for a mutating command; a cell
// left without any clause reads droppedCell.
func filterTableRow(line string) string {
	cells := splitCells(line)
	for i := 1; i < len(cells)-1; i++ {
		cells[i] = filterCell(cells[i])
	}
	return strings.Join(cells, "|")
}

func filterCell(cell string) string {
	text := strings.TrimSpace(cell)
	if len(mutatingAt(text)) == 0 {
		return cell
	}
	var kept []string
	for _, clause := range segments(text, "; ") {
		if clauseAllowed(clause) {
			kept = append(kept, strings.TrimSuffix(clause, "; "))
		}
	}
	if len(kept) == 0 {
		kept = []string{droppedCell}
	}
	return " " + strings.Join(kept, "; ") + " "
}

// splitCells splits a table row on every pipe a backslash does not escape.
func splitCells(line string) []string {
	var cells []string
	start := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '|' && (i == 0 || line[i-1] != '\\') {
			cells = append(cells, line[start:i])
			start = i + 1
		}
	}
	return append(cells, line[start:])
}

// filterFence drops, from one fenced block, every line naming a mutating command with the
// comment lines and blank lines directly above it. A block left with no command line is
// dropped whole; a block without a mutating line is returned unchanged.
func filterFence(block []string, closed bool) []string {
	body := fenceBody(block, closed)
	kept := make([]string, 0, len(body))
	dropped := false
	for _, line := range body {
		if len(mutatingAt(line)) == 0 {
			kept = append(kept, line)
			continue
		}
		dropped = true
		kept = trimTrailingComments(kept)
	}
	if !dropped {
		return block
	}
	if len(commandLines(kept)) == 0 {
		return nil
	}
	out := append([]string{block[0]}, kept...)
	if closed {
		out = append(out, block[len(block)-1])
	}
	return out
}

func trimTrailingComments(lines []string) []string {
	for len(lines) > 0 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "#") {
		lines = lines[:len(lines)-1]
	}
	for len(lines) > 0 && isBlank(lines[len(lines)-1]) {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// fenceOpen reports whether line opens a fenced block and returns its fence marker.
func fenceOpen(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	for _, char := range []string{"`", "~"} {
		marker := strings.Repeat(char, 3)
		if strings.HasPrefix(trimmed, marker) {
			return trimmed[:len(trimmed)-len(strings.TrimLeft(trimmed, char))], true
		}
	}
	return "", false
}

// fenceClose returns the index of the line closing the fence opened at open, or the last line
// and false when the fence runs to the end of the text.
func fenceClose(lines []string, open int, marker string) (int, bool) {
	for i := open + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if strings.HasPrefix(trimmed, marker) && strings.Trim(trimmed, marker[:1]) == "" {
			return i, true
		}
	}
	return len(lines) - 1, false
}

// fenceBody returns the lines between a fenced block's opening and its closing line.
func fenceBody(block []string, closed bool) []string {
	if closed && len(block) >= 2 {
		return block[1 : len(block)-1]
	}
	return block[1:]
}

func indentOf(line string) int { return len(line) - len(strings.TrimLeft(line, " \t")) }

// continuesItem reports whether line belongs to the list item indented by indent: a nonblank
// line indented deeper than the item's marker.
func continuesItem(line string, indent int) bool {
	return !isBlank(line) && indentOf(line) > indent
}

// collapseBlankRuns keeps one blank line of every run of blank lines outside a fence, so a
// dropped block leaves no gap twice as wide as the source's.
func collapseBlankRuns(lines []string) []string {
	out := make([]string, 0, len(lines))
	marker := ""
	for _, line := range lines {
		if marker == "" && isBlank(line) && len(out) > 0 && isBlank(out[len(out)-1]) {
			continue
		}
		out = append(out, line)
		marker = nextFenceState(line, marker)
	}
	return out
}

// nextFenceState returns the fence marker open after line, given the marker open before it.
func nextFenceState(line, marker string) string {
	if marker == "" {
		if opened, ok := fenceOpen(line); ok {
			return opened
		}
		return ""
	}
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, marker) && strings.Trim(trimmed, marker[:1]) == "" {
		return ""
	}
	return marker
}

// segments splits text after every delim outside a code span, each part keeping its delim,
// so joining the parts restores text.
func segments(text, delim string) []string {
	var parts []string
	start, inCode := 0, false
	for i := 0; i < len(text); i++ {
		if text[i] == '`' {
			inCode = !inCode
			continue
		}
		if !inCode && strings.HasPrefix(text[i:], delim) {
			parts = append(parts, text[start:i+len(delim)])
			start = i + len(delim)
			i = start - 1
		}
	}
	return append(parts, text[start:])
}

// mutatingAt returns the offset of every mutating command text names.
func mutatingAt(text string) []int {
	var starts []int
	for _, loc := range mutatingCommand.FindAllStringIndex(text, -1) {
		if !readOnlySubcommand(text[loc[0]:loc[1]]) {
			starts = append(starts, loc[0])
		}
	}
	return starts
}

// readOnlySubcommand reports a `state task` match that names the read-only `list` subcommand.
func readOnlySubcommand(match string) bool {
	fields := strings.Fields(match)
	return len(fields) == 3 && fields[0] == "state" && fields[2] == "list"
}

// clauseAllowed reports whether every mutating command clause names follows a negation in it,
// so the clause forbids the command rather than asking for it.
func clauseAllowed(clause string) bool {
	for _, at := range mutatingAt(clause) {
		if !negation.MatchString(clause[:at]) {
			return false
		}
	}
	return true
}

// textAllowed reports whether every clause of every sentence of text is allowed.
func textAllowed(text string) bool {
	for _, sentence := range segments(text, ". ") {
		for _, clause := range segments(sentence, "; ") {
			if !clauseAllowed(clause) {
				return false
			}
		}
	}
	return true
}

// assertReadOnly fails when text names a mutating command anywhere but in a prohibition: in a
// fenced line, a table cell or a prose clause.
func assertReadOnly(text string) error {
	marker := ""
	for index, line := range strings.Split(text, "\n") {
		inFence := marker != ""
		marker = nextFenceState(line, marker)
		if lineAllowed(line, inFence) {
			continue
		}
		at := mutatingAt(line)[0]
		match := mutatingCommand.FindString(line[at:])
		return fmt.Errorf("read-only projection line %d asks for mutating command %q", index+1, match)
	}
	return nil
}

func lineAllowed(line string, inFence bool) bool {
	if len(mutatingAt(line)) == 0 {
		return true
	}
	if inFence {
		return false
	}
	if !strings.HasPrefix(strings.TrimSpace(line), "|") {
		return textAllowed(line)
	}
	for _, cell := range splitCells(line) {
		if !textAllowed(cell) {
			return false
		}
	}
	return true
}

// IsReadOnlyRole reports whether role describes a read-only agent (e.g. audit, review, research).
func IsReadOnlyRole(role string) bool {
	r := strings.ToLower(strings.TrimSpace(role))
	if r == "" {
		return false
	}
	switch r {
	case "research", "researcher", "review", "reviewer", "audit", "auditor", "praetor-auditor", "read-only", "readonly":
		return true
	}
	return strings.Contains(r, "audit") || strings.Contains(r, "review") || strings.Contains(r, "research") || strings.Contains(r, "readonly")
}
