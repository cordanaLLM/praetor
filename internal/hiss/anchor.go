package hiss

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
)

// A finding's line shifts whenever unrelated lines above it change, so a debt baseline keyed
// by line reported an unchanged function as a new infraction once anything was inserted above
// it (#29). The scan therefore gives every finding an anchor, the part of its identity that
// survives a shift, and the baseline keys an entry by rule, file, anchor and the finding's
// rank among the findings sharing all three (baseline.AssignFingerprints).
//
// The anchor is the smallest stable thing the scanner knows about the finding's place:
//
//   - "fn:<name>" for a finding at or inside a function its language scanner located. Go
//     qualifies a method with its receiver type and Python with its class.
//   - "text:<hash>" for a finding no located function holds: a statement at file level, a
//     systemd directive, an Ansible task key. The hash is of the finding's line with its
//     whitespace removed, so re-indenting the line or changing its line ending keeps it.
//   - "file" for a finding whose line the file does not hold.
//
// Renaming the function, or editing the anchored line of a text anchor, changes the anchor
// and so the entry: the ratchet reads the finding as new until the baseline is re-recorded.
const (
	anchorFunction = "fn:"
	anchorText     = "text:"
	anchorFile     = "file"

	// textAnchorHexLen is how much of the line's SHA-256 a text anchor keeps. Two lines of one
	// file and rule that collide only share their ordinals, which stays deterministic.
	textAnchorHexLen = 12
	// maxNotedFunctions bounds the functions one file's scan notes (HISS-02). A finding in a
	// function past the bound is anchored by its Symbol or its line text instead.
	maxNotedFunctions = 1 << 16
)

// funcSpan is one named function a language scanner located in the file it is reading: the
// lines its definition spans, both included.
type funcSpan struct {
	name       string
	start, end int
}

// appendFunction adds a function a scanner located to those of the file being read, within
// maxNotedFunctions. A function without a name anchors nothing.
func appendFunction(functions []funcSpan, name string, start, end int) []funcSpan {
	if name == "" || len(functions) >= maxNotedFunctions {
		return functions
	}
	return append(functions, funcSpan{name: name, start: start, end: end})
}

// findingAnchor is the anchor of a finding at the 1-based line of a file whose scan located
// functions and whose physical lines are lines: the function holding it, else its line's text.
// symbol is the function the finding itself names, if any.
func findingAnchor(functions []funcSpan, lines []string, line int, symbol string) string {
	if name := enclosingFunction(functions, line, symbol); name != "" {
		return anchorFunction + name
	}
	return lineAnchor(lines, line)
}

// enclosingFunction names the function a finding at line belongs to: the innermost noted
// function holding the line. A finding that names its function in symbol belongs to the
// innermost function of that name there, and to symbol itself when the scanner noted none, so
// a function opened on the line its enclosing function starts on never takes the finding.
func enclosingFunction(functions []funcSpan, line int, symbol string) string {
	found := -1
	for i := 0; i < len(functions); i++ {
		f := functions[i]
		if line < f.start || line > f.end || !f.named(symbol) {
			continue
		}
		if found < 0 || f.start > functions[found].start ||
			(f.start == functions[found].start && f.end < functions[found].end) {
			found = i
		}
	}
	if found < 0 {
		return symbol
	}
	return functions[found].name
}

// named reports whether a finding that names symbol can mean this function: symbol is empty,
// the function's name, or that name without its receiver type or class.
func (f funcSpan) named(symbol string) bool {
	return symbol == "" || f.name == symbol || f.name[strings.LastIndexByte(f.name, '.')+1:] == symbol
}

// qualifiedName joins an owner, a Go receiver type or a Python class, and a function name.
func qualifiedName(owner, name string) string {
	if owner == "" || name == "" {
		return name
	}
	return owner + "." + name
}

// lineAnchor is the text anchor of the 1-based line, or the file anchor when the file holds
// no such line.
func lineAnchor(lines []string, line int) string {
	if line < 1 || line > len(lines) {
		return anchorFile
	}
	sum := sha256.Sum256([]byte(strings.Join(strings.Fields(lines[line-1]), "")))
	return anchorText + hex.EncodeToString(sum[:])[:textAnchorHexLen]
}
