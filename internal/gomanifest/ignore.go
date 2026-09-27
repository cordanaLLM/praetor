package gomanifest

import (
	"path/filepath"
	"strings"
)

// ignoreKeyword is the go.mod directive (Go 1.25+) that removes directories from the
// go command's package patterns.
const ignoreKeyword = "ignore"

// IgnoreLine advances ignore-block state and identifies an ignore directive line, the same
// way RequirementLine does for require lines. Block delimiters are consumed; a single-line
// ignore has its keyword removed.
func IgnoreLine(raw string, inBlock *bool) (string, bool) {
	return blockDirectiveLine(raw, ignoreKeyword, inBlock)
}

// ParseIgnore returns the path of one ignore line, block keyword and delimiters already
// removed (see IgnoreLine), with a trailing comment dropped. Like golang.org/x/mod/modfile,
// it requires exactly one argument and unquotes a double-quoted one; it reports false for
// a line with no argument, more than one, or a malformed quoted token.
func ParseIgnore(line string) (string, bool) {
	code, _, _ := strings.Cut(line, "//")
	fields := strings.Fields(code)
	if len(fields) != 1 {
		return "", false
	}
	path, ok := unquoteToken(fields[0])
	if !ok || path == "" {
		return "", false
	}
	return path, true
}

// IgnoreSet is the directories a module's ignore directives remove from the go command's
// package patterns such as "./...". It applies the rule of the go command's
// search.IgnorePatterns (cmd/go/internal/search): a path starting with "./" ignores that
// directory below the module root and everything inside it; any other path ignores every
// directory with that slash-separated path at any depth, and everything inside it.
// Wildcards are not supported. The zero value ignores nothing.
type IgnoreSet struct {
	rooted   []string
	anywhere []string
}

// NewIgnoreSet builds the set of the ignore directive paths of one go.mod.
func NewIgnoreSet(paths []string) IgnoreSet {
	var set IgnoreSet
	for _, path := range paths {
		trimmed, rooted := strings.CutPrefix(path, "./")
		if rooted {
			set.rooted = append(set.rooted, wrapSlashes(trimmed))
		} else {
			set.anywhere = append(set.anywhere, wrapSlashes(trimmed))
		}
	}
	return set
}

// Ignores reports whether dir, a directory path relative to the module root in slash or
// OS form, is one the ignore directives remove. The module root itself ("" or ".") is
// never ignored: the go command never matches it against the directives.
func (s IgnoreSet) Ignores(dir string) bool {
	if dir == "" || dir == "." {
		return false
	}
	wrapped := wrapSlashes(dir)
	for _, pattern := range s.rooted {
		if strings.HasPrefix(wrapped, pattern) {
			return true
		}
	}
	for _, pattern := range s.anywhere {
		if strings.Contains(wrapped, pattern) {
			return true
		}
	}
	return false
}

// wrapSlashes converts path to slash form and adds a leading and a trailing slash where
// missing, so a pattern matches whole path elements only, as the go command's
// normalizePath does.
func wrapSlashes(path string) string {
	path = filepath.ToSlash(path)
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return path
}
