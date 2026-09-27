// Package gomanifest shares lexical Go manifest handling across offline scanners.
// It extracts require-block lines; callers retain their version and policy validation.
package gomanifest

import "strings"

// RequirementLine advances require-block state and identifies a dependency line.
// Block delimiters are consumed; a single-line require has its keyword removed.
func RequirementLine(raw string, inBlock *bool) (string, bool) {
	if inBlock == nil {
		return "", false
	}
	line := strings.TrimSpace(raw)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", false
	}
	if strings.HasPrefix(line, "require (") {
		*inBlock = true
		return "", false
	}
	if *inBlock && line == ")" {
		*inBlock = false
		return "", false
	}
	return strings.TrimPrefix(line, "require "), *inBlock || strings.HasPrefix(line, "require ")
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
