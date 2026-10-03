package strictjson

import (
	"bytes"
	"path"
	"path/filepath"
)

// Dialect names the syntax one document is written in. Whatever the dialect, the scan applies
// the same bounds and the same refusals: the dialect only decides which bytes outside a string
// are not JSON tokens.
type Dialect uint8

const (
	// StrictJSON is JSON as RFC 8259 defines it and nothing more. It is the zero value.
	StrictJSON Dialect = iota
	// JSONC is JSON with Comments, the mode VS Code reads its configuration files in: a "//"
	// comment running to the end of its line, a "/* */" comment, which does not nest, and one
	// trailing comma after the last member of an object or the last item of an array.
	JSONC
)

// String names the dialect as a refusal or a report words it.
func (d Dialect) String() string {
	if d == JSONC {
		return "JSON with Comments"
	}
	return "strict JSON"
}

// vscodeJSONC lists the workspace files VS Code reads as JSON with Comments, each as its last
// two path elements. VS Code's own configuration-editing extension declares the four file
// names under the language "jsonc" (extensions/configuration-editing/package.json,
// contributes.languages), and its guide "Editing JSON with VS Code", section "JSON with
// Comments", names settings.json, tasks.json and launch.json:
// https://code.visualstudio.com/docs/languages/json. A further path belongs here only when
// its consumer documents the format.
var vscodeJSONC = map[string]bool{
	".vscode/settings.json":   true,
	".vscode/extensions.json": true,
	".vscode/launch.json":     true,
	".vscode/tasks.json":      true,
}

// DialectOf returns the dialect the consumer of file documents for it, from its path alone:
// JSONC for the VS Code files of a .vscode directory (vscodeJSONC), in the repository root or
// in a workspace folder below it, and strict JSON for every other path. It is the one place
// that decision is made, so the flavor audit, `editors generate`, `editors verify` and
// adoption read a path the same way. file is a relative path in either the slash form or the
// host's form.
func DialectOf(file string) Dialect {
	dir, name := path.Split(filepath.ToSlash(file))
	if vscodeJSONC[path.Base(dir)+"/"+name] {
		return JSONC
	}
	return StrictJSON
}

// strict returns raw as the strict JSON text the scan reads: raw itself under StrictJSON, and
// under JSONC raw with every comment and trailing comma blanked (blankJSONC).
func (d Dialect) strict(raw []byte) []byte {
	if d != JSONC {
		return raw
	}
	return blankJSONC(raw)
}

// blankJSONC returns raw with each JSONC comment and trailing comma replaced by spaces, one
// space per byte, so every offset the scanner reports is an offset into raw. It is not a
// parser: it finds where strings start and end, so that "//" inside a string stays data, and
// leaves every token to the one scanner. What it does not recognise it leaves in place for
// that scanner to refuse: a block comment nothing closes, a lone slash, and a comma that
// follows no value or precedes no closing bracket, such as "[,]" and "[1,,]".
//
// Each step consumes at least one byte and no byte is read more than twice, so the pass is
// bounded by the input length, which Validate has already held to MaxBytes. A document
// without comments or trailing commas comes back as raw itself, unallocated.
func blankJSONC(raw []byte) []byte {
	l := jsoncBlanker{raw: raw, comma: -1}
	for at := 0; at < len(raw); {
		next := l.step(at)
		if next <= at {
			break
		}
		at = next
	}
	if l.out == nil {
		return raw
	}
	return l.out
}

type jsoncBlanker struct {
	raw   []byte
	out   []byte // raw with the blanked bytes; nil until the first one
	prev  byte   // last token byte read outside comments; 0 before the first
	comma int    // offset of a comma that follows a value and may prove trailing; -1 for none
}

// step reads what starts at offset at, outside every string and comment, and returns the
// offset of what follows it. It returns at itself to stop the pass at a slash that opens no
// complete comment.
func (l *jsoncBlanker) step(at int) int {
	switch b := l.raw[at]; b {
	case '"':
		l.token(b)
		return l.stringEnd(at)
	case '/':
		return l.comment(at)
	case ' ', '\t', '\n', '\r':
		return at + 1
	case ',':
		l.comma = -1
		if endsValue(l.prev) {
			l.comma = at
		}
		l.prev = b
		return at + 1
	default:
		l.token(b)
		return at + 1
	}
}

// token records one token byte. A closing bracket proves the pending comma trailing; any other
// token proves it a separator.
func (l *jsoncBlanker) token(b byte) {
	if l.comma >= 0 && (b == '}' || b == ']') {
		l.blank(l.comma, l.comma+1)
	}
	l.comma = -1
	l.prev = b
}

// endsValue reports whether prev, the last token byte read, ends a value: the closing quote of
// a string, the last byte of a number or literal, or a closing bracket.
func endsValue(prev byte) bool {
	switch prev {
	case 0, '[', '{', ',', ':':
		return false
	default:
		return true
	}
}

// stringEnd returns the offset after the string that opens at offset at. A backslash escapes
// the byte after it, so an escaped quote does not close the string. A string nothing closes
// runs to the end of the input, where the scanner refuses it.
func (l *jsoncBlanker) stringEnd(at int) int {
	for i := at + 1; i < len(l.raw); i++ {
		switch l.raw[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return len(l.raw)
}

// comment blanks the comment that opens at offset at and returns the offset after it: a line
// comment ends before its line feed or carriage return, a block comment after its first "*/".
// It returns at when no complete comment opens there.
func (l *jsoncBlanker) comment(at int) int {
	rest := l.raw[at:]
	switch {
	case bytes.HasPrefix(rest, []byte("//")):
		end := bytes.IndexAny(rest, "\n\r")
		if end < 0 {
			end = len(rest)
		}
		l.blank(at, at+end)
		return at + end
	case bytes.HasPrefix(rest, []byte("/*")):
		closer := bytes.Index(rest[2:], []byte("*/"))
		if closer < 0 {
			return at
		}
		end := at + 2 + closer + 2
		l.blank(at, end)
		return end
	default:
		return at
	}
}

// blank replaces the bytes from offset from up to offset to with spaces in the copy of raw.
func (l *jsoncBlanker) blank(from, to int) {
	if l.out == nil {
		l.out = bytes.Clone(l.raw)
	}
	for i := from; i < to && i < len(l.out); i++ {
		l.out[i] = ' '
	}
}
