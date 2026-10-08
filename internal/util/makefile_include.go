package util

import (
	"fmt"
	"path"
	"strings"
)

// MaxMakefileIncludeDepth bounds how many levels of nested literal includes MakefileExpandIncludes
// follows (HISS-01, HISS-02): an include in the Makefile is level 1, an include inside that
// fragment level 2. An include one level past the bound stays an include line, which the reader
// treats as ambiguous (makefileReadsFile).
const MaxMakefileIncludeDepth = 4

// MaxMakefileIncludeFiles bounds how many fragments one expansion reads in all (HISS-02).
const MaxMakefileIncludeFiles = 64

// MakefileIncludeReader returns the text of the fragment a literal include operand names, relative
// to the directory Make runs in, and false when the caller will not vouch for it: a path outside
// the repository, a file that is missing, untracked, generated, a symlink or not a regular file,
// or one above the byte bound. It decides nothing about Make syntax; MakefileExpandIncludes does.
type MakefileIncludeReader func(path string) (string, bool)

// MakefileExpandIncludes returns data with each "include" line whose operands are all literal
// paths replaced by the text of the fragments read returns, level by level up to
// MaxMakefileIncludeDepth, so MakefileHasTarget and MakefileMayDefineTarget decide from the
// combined text. Everything else stays as it is and stays ambiguous: "-include", "sinclude" and
// "load", an operand holding a variable reference, a wildcard, a function, a comment or any other
// character Make would expand or strip (makefileLiteralPath), an operand read refuses, a fragment
// whose own structure the splice would misread (makefileSpliceable), and an include past the depth
// or file bound. The result is for the ownership check only; a caller never writes it.
func MakefileExpandIncludes(data string, read MakefileIncludeReader) string {
	expanded, _ := MakefileExpandIncludesReport(data, read)
	return expanded
}

// MakefileExpandIncludesReport is MakefileExpandIncludes that also returns a note when it
// discards the expansion because Make may remake a followed include or a line has an unallowed shape,
// naming the includes, the first refused line and its shape, so the caller's refusal can say why
// an include it expected to follow stayed ambiguous.
func MakefileExpandIncludesReport(data string, read MakefileIncludeReader) (string, []string) {
	if read == nil {
		return data, nil
	}
	lines, whole := makefileTrackedLogicalLines(data, "Makefile")
	if !whole {
		return data, nil
	}
	files := 0
	followed := make([]string, 0, MaxMakefileIncludeFiles)
	includeLoc := make(map[string]string)
	original := data
	for level := 0; level < MaxMakefileIncludeDepth; level++ {
		expanded, changed, ok := makefileExpandLevel(lines, read, &files, &followed, includeLoc)
		if !ok || !changed {
			break
		}
		lines = expanded
	}
	if len(followed) == 0 {
		return data, nil
	}
	if note := makefileDefaultSuffixNote(followed, includeLoc); note != "" {
		return original, []string{note}
	}
	ok, file, line, shape := makefileTrackedLiteralShapes(lines, followed)
	if !ok {
		return original, []string{fmt.Sprintf("includes %s not followed: %s:%d: %s", strings.Join(followed, ", "), file, line, shape)}
	}
	texts := make([]string, len(lines))
	for i, l := range lines {
		texts[i] = l.text
	}
	return strings.Join(texts, "\n"), nil
}

func makefileDefaultSuffixNote(followed []string, includeLoc map[string]string) string {
	for _, operand := range followed {
		clean := path.Clean(operand)
		if makefileHasDefaultSuffix(clean) {
			loc := includeLoc[operand]
			if loc == "" {
				loc = operand
			}
			return fmt.Sprintf("includes %s not followed: %s: operand ends in default suffix %s",
				strings.Join(followed, ", "), loc, path.Ext(clean))
		}
	}
	return ""
}

// makefileDefaultSuffixes is GNU Make's default .SUFFIXES list. A file ending in one can be built
// by a built-in suffix rule from a neighbour with another suffix (gen.s from gen.S through .S.s,
// measured against GNU Make 4.4.1), which no name-prefix check on the neighbours sees.
var makefileDefaultSuffixes = []string{
	".out", ".a", ".ln", ".o", ".c", ".cc", ".C", ".cpp", ".p", ".f", ".F", ".m", ".r", ".y", ".l",
	".ym", ".yl", ".s", ".S", ".mod", ".sym", ".def", ".h", ".info", ".dvi", ".tex", ".texinfo",
	".texi", ".txinfo", ".w", ".ch", ".web", ".sh", ".elc", ".el",
}

// makefileHasDefaultSuffix reports whether name ends in a suffix of Make's default .SUFFIXES list.
func makefileHasDefaultSuffix(name string) bool {
	for _, suffix := range makefileDefaultSuffixes {
		if strings.HasSuffix(name, suffix) {
			return true
		}
	}
	return false
}

// makefileNormalizeName spells a file name as Make compares it: every leading "./" and the slashes
// after it are dropped, then the path is cleaned (GNU Make 4.4.1 treats "include gen.mk" and a rule
// target "././gen.mk" or ".//gen.mk" as one file). The loop shortens name each pass (HISS-02).
func makefileNormalizeName(name string) string {
	for strings.HasPrefix(name, "./") {
		name = strings.TrimLeft(name[2:], "/")
	}
	return path.Clean(name)
}

// makefileIncludeBoundary is the line spliced before and after every fragment. A variable binding
// closes the recipe the scanner follows, which is what an include does in Make: Make closes the
// including file's open rule at the include, reads the fragment with none open, and continues the
// including file with none open. Without it a recipe, or a recipe state a conditional left unsure,
// would carry across the splice and a tab-prefixed line would read as a recipe line where Make
// parses it as makefile syntax (measured against GNU Make 4.4.1).
const makefileIncludeBoundary = ".PRAETOR_INCLUDE_BOUNDARY := 1"

// makefileExpandLevel replaces the literal include lines of lines once. Nested includes the
// fragments bring are left for the next level. When the scanner loses its place anywhere in lines
// the whole level is discarded and lines comes back unchanged, so nothing is expanded.
func makefileExpandLevel(lines []makefileTrackedLine, read MakefileIncludeReader, files *int, followed *[]string, includeLoc map[string]string) ([]makefileTrackedLine, bool, bool) {
	var scanner makefileScanner
	out := make([]makefileTrackedLine, 0, len(lines))
	changed := false
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		tl := lines[index]
		kind := scanner.next(tl.text)
		if scanner.lost {
			return lines, false, false
		}
		if kind == makefileSyntaxLine {
			if replacement, ok := makefileIncludeText(tl, read, files, followed, includeLoc); ok {
				out = append(out, replacement...)
				changed = true
				continue
			}
		}
		out = append(out, tl)
	}
	return out, changed, true
}

// makefileIncludeText returns the replacement lines an include line stands for, and false when
// line is no include of literal paths all of which read vouches for.
func makefileIncludeText(tl makefileTrackedLine, read MakefileIncludeReader, files *int, followed *[]string, includeLoc map[string]string) ([]makefileTrackedLine, bool) {
	fields := strings.Fields(tl.text)
	if len(fields) < 2 || fields[0] != "include" || strings.HasPrefix(tl.text, "\t") {
		return nil, false
	}
	parts := make([]makefileTrackedLine, 0, 2*len(fields))
	operands := make([]string, 0, len(fields)-1)
	parts = append(parts, makefileTrackedLine{text: makefileIncludeBoundary, file: tl.file, line: tl.line})
	for _, operand := range fields[1:] {
		fragLines, ok := makefileReadTrackedFragment(operand, read, files)
		if !ok {
			return nil, false
		}
		parts = append(parts, fragLines...)
		parts = append(parts, makefileTrackedLine{text: makefileIncludeBoundary, file: tl.file, line: tl.line})
		operands = append(operands, operand)
		includeLoc[operand] = fmt.Sprintf("%s:%d", tl.file, tl.line)
	}
	*followed = append(*followed, operands...)
	return parts, true
}

func makefileReadTrackedFragment(operand string, read MakefileIncludeReader, files *int) ([]makefileTrackedLine, bool) {
	if !makefileLiteralPath(operand) || *files >= MaxMakefileIncludeFiles {
		return nil, false
	}
	*files++
	text, ok := read(operand)
	if !ok || !makefileSpliceable(text) {
		return nil, false
	}
	normalized := strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	fragLines, whole := makefileTrackedLogicalLines(normalized, operand)
	if !whole {
		return nil, false
	}
	return fragLines, true
}

// makefileLiteralPath reports whether operand is a path Make uses as written: no variable
// reference, wildcard, bracket, comment, escape, quote, home shorthand, pattern or assignment
// character, and no ".." element or leading slash or dash (the caller's reader confines the rest).
func makefileLiteralPath(operand string) bool {
	if operand == "" || len(operand) > MaxMakefileLineBytes || strings.ContainsAny(operand, "$*?[]{}()%#\\'\"`~=:;!&|<>") {
		return false
	}
	if strings.HasPrefix(operand, "/") || strings.HasPrefix(operand, "-") {
		return false
	}
	for _, element := range strings.Split(operand, "/") {
		if element == ".." {
			return false
		}
	}
	return true
}

// makefileSpliceable reports whether fragment can be spliced into another Makefile without
// changing how either half reads. Make reads an included file on its own: a continuation, a
// define, a conditional or a recipe cannot cross its end, and a tab-prefixed line with no recipe
// open is an error in it. A fragment that breaks one of these is one the reader leaves to Make.
func makefileSpliceable(fragment string) bool {
	lines, whole := makefileLogicalLines(fragment)
	if !whole || strings.Contains(fragment, ".RECIPEPREFIX") {
		return false
	}
	physical := strings.Split(strings.TrimSuffix(fragment, "\n"), "\n")
	if makefileContinues(physical[len(physical)-1]) {
		return false
	}
	var scanner makefileScanner
	for _, line := range lines {
		kind := scanner.next(line)
		if scanner.lost || (kind == makefileSyntaxLine && strings.HasPrefix(line, "\t") && strings.TrimSpace(line) != "") {
			return false
		}
	}
	return scanner.depth == 0 && len(scanner.branches) == 0
}
