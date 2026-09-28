// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"bytes"
	"fmt"
	"strings"
)

// MaxDiffLines bounds each side UnifiedDiff aligns line by line (HISS-02). The alignment table
// holds (before+1)*(after+1) cells, so a side above this is rendered as a whole replacement
// instead: every old line removed, every new line added.
const MaxDiffLines = 1000

// noNewlineMarker follows a diff line whose file ends without a final newline, as diff(1)
// writes it, so a change to the final newline alone still shows.
const noNewlineMarker = "\\ No newline at end of file\n"

// UnifiedDiff renders the change from before to after at path as one unified diff hunk that
// carries every line: "--- a/<path>", "+++ b/<path>", "@@ -<range> +<range> @@", then each line
// prefixed with ' ' (kept), '-' (removed) or '+' (added). Every line is context because the
// files it shows are short configuration, where the lines around a change say which entry
// changed. Identical input renders "". Lines are compared with their terminators, so a
// CRLF checkout or a lost final newline shows as a change rather than as no difference.
func UnifiedDiff(path string, before, after []byte) string {
	if bytes.Equal(before, after) {
		return ""
	}
	old, updated := diffLines(before), diffLines(after)
	var out strings.Builder
	fmt.Fprintf(&out, "--- a/%s\n+++ b/%s\n@@ -%s +%s @@\n", path, path, hunkRange(len(old)), hunkRange(len(updated)))
	edits := lineEdits(old, updated)
	for i := 0; i < len(edits); i++ {
		out.WriteByte(edits[i].op)
		out.WriteString(edits[i].line)
		if !strings.HasSuffix(edits[i].line, "\n") {
			out.WriteString("\n" + noNewlineMarker)
		}
	}
	return out.String()
}

// lineEdit is one line of a diff and whether it is kept, removed or added.
type lineEdit struct {
	op   byte
	line string
}

// diffLines splits text after each newline, keeping the terminators; the text after the last
// newline, when there is any, is the final line.
func diffLines(text []byte) []string {
	lines := strings.SplitAfter(string(text), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// hunkRange is a unified diff range over a whole side of n lines: "1,n", or "0,0" for none.
func hunkRange(n int) string {
	if n == 0 {
		return "0,0"
	}
	return fmt.Sprintf("1,%d", n)
}

// lineEdits aligns old with updated on their longest common subsequence of lines, or replaces
// one with the other when either is above MaxDiffLines.
func lineEdits(old, updated []string) []lineEdit {
	if len(old) > MaxDiffLines || len(updated) > MaxDiffLines {
		return replacementEdits(old, updated)
	}
	return walkAlignment(old, updated, alignmentTable(old, updated))
}

// alignmentTable holds, at cell i*(len(updated)+1)+j, the length of the longest common
// subsequence of old[i:] and updated[j:].
func alignmentTable(old, updated []string) []int {
	width := len(updated) + 1
	table := make([]int, (len(old)+1)*width)
	for i := len(old) - 1; i >= 0; i-- {
		for j := len(updated) - 1; j >= 0; j-- {
			if old[i] == updated[j] {
				table[i*width+j] = table[(i+1)*width+j+1] + 1
				continue
			}
			table[i*width+j] = max(table[(i+1)*width+j], table[i*width+j+1])
		}
	}
	return table
}

// walkAlignment reads the edits off the table, a removal ahead of the addition that replaces
// it. Every step consumes a line of old or of updated, so the walk ends within their sum.
func walkAlignment(old, updated []string, table []int) []lineEdit {
	width := len(updated) + 1
	edits := make([]lineEdit, 0, len(old)+len(updated))
	i, j := 0, 0
	for step := 0; step < len(old)+len(updated) && (i < len(old) || j < len(updated)); step++ {
		switch {
		case i < len(old) && j < len(updated) && old[i] == updated[j]:
			edits = append(edits, lineEdit{op: ' ', line: old[i]})
			i, j = i+1, j+1
		case i < len(old) && (j == len(updated) || table[(i+1)*width+j] >= table[i*width+j+1]):
			edits = append(edits, lineEdit{op: '-', line: old[i]})
			i++
		default:
			edits = append(edits, lineEdit{op: '+', line: updated[j]})
			j++
		}
	}
	return edits
}

// replacementEdits removes every line of old and adds every line of updated.
func replacementEdits(old, updated []string) []lineEdit {
	edits := make([]lineEdit, 0, len(old)+len(updated))
	for i := 0; i < len(old); i++ {
		edits = append(edits, lineEdit{op: '-', line: old[i]})
	}
	for j := 0; j < len(updated); j++ {
		edits = append(edits, lineEdit{op: '+', line: updated[j]})
	}
	return edits
}
