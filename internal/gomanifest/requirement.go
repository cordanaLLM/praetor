// Package gomanifest shares lexical Go manifest handling across offline scanners.
// It extracts require-block lines; callers retain their version and policy validation.
package gomanifest

import (
	"strconv"
	"strings"
)

// RequirementLine advances require-block state and identifies a dependency line.
// Block delimiters are consumed; a single-line require has its keyword removed.
func RequirementLine(raw string, inBlock *bool) (string, bool) {
	return blockDirectiveLine(raw, "require", inBlock)
}

// ToolLine advances tool-block state and identifies a tool directive line, the package path
// of one tool the module runs with go tool. Block delimiters are consumed; a single-line tool
// directive has its keyword removed. A trailing comment stays on the line.
func ToolLine(raw string, inBlock *bool) (string, bool) {
	return blockDirectiveLine(raw, "tool", inBlock)
}

// blockDirectiveLine advances the block state of one go.mod directive that may be written
// on one line ("keyword arg") or as a block ("keyword (" ... ")"), and identifies a line
// carrying that directive's arguments. Block delimiters, blank lines and comment lines are
// consumed; a single-line directive has its keyword removed. A nil state identifies
// nothing. RequirementLine, ToolLine, ReplaceLine and IgnoreLine share it.
func blockDirectiveLine(raw, keyword string, inBlock *bool) (string, bool) {
	if inBlock == nil {
		return "", false
	}
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", false
	}
	if strings.HasPrefix(line, keyword+" (") {
		*inBlock = true
		return "", false
	}
	if *inBlock && line == ")" {
		*inBlock = false
		return "", false
	}
	return strings.TrimPrefix(line, keyword+" "), *inBlock || strings.HasPrefix(line, keyword+" ")
}

// indirectMarker is the comment word the go command writes after a requirement that no
// package of the main module imports.
const indirectMarker = "indirect"

// IsIndirect reports whether a require line carries the go command's indirect marker. It
// applies the rule of golang.org/x/mod/modfile: the line's comment, with its leading "//"
// removed, is exactly the word "indirect", or begins with "indirect;" followed by more
// text. "//indirect" is therefore indirect, while "// indirectly used" and
// "// see indirect" are not.
func IsIndirect(line string) bool {
	_, comment, found := strings.Cut(line, "//")
	if !found {
		return false
	}
	fields := strings.Fields(comment)
	return (len(fields) == 1 && fields[0] == indirectMarker) ||
		(len(fields) > 1 && fields[0] == indirectMarker+";")
}

// Requirement is one go.mod require directive: the module path and the version it
// requires, both unquoted.
type Requirement struct {
	Path    string
	Version string
}

// ParseRequirement splits one require line, block keyword and delimiters already removed
// (see RequirementLine), into its module path and version, dropping a trailing comment.
// Each token is read the way golang.org/x/mod/modfile reads it: a double-quoted token is
// unquoted, while a double-quoted token that does not unquote, and an unquoted token
// that contains a quote character, are refused. It reports false for a line without
// both a path and a version.
func ParseRequirement(line string) (Requirement, bool) {
	code, _, _ := strings.Cut(line, "//")
	fields := strings.Fields(code)
	if len(fields) < 2 {
		return Requirement{}, false
	}
	path, pathOK := unquoteToken(fields[0])
	version, versionOK := unquoteToken(fields[1])
	if !pathOK || !versionOK || path == "" || version == "" {
		return Requirement{}, false
	}
	return Requirement{Path: path, Version: version}, true
}

// unquoteToken applies the string rule of golang.org/x/mod/modfile to one go.mod token:
// a token beginning with a double quote is a Go string literal, and any other token is
// taken as written unless it contains a quote character, which modfile reserves.
func unquoteToken(token string) (string, bool) {
	if strings.HasPrefix(token, `"`) {
		unquoted, err := strconv.Unquote(token)
		return unquoted, err == nil
	}
	return token, !strings.ContainsAny(token, "\"'`")
}
