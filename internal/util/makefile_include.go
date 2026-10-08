package util

import (
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
// discards the expansion because Make may remake a followed include, naming the includes, so the
// caller's refusal can say why an include it expected to follow stayed ambiguous.
func MakefileExpandIncludesReport(data string, read MakefileIncludeReader) (string, []string) {
	if read == nil {
		return data, nil
	}
	files := 0
	followed := make([]string, 0, MaxMakefileIncludeFiles)
	original := data
	for level := 0; level < MaxMakefileIncludeDepth; level++ {
		expanded, changed := makefileExpandLevel(data, read, &files, &followed)
		if !changed {
			break
		}
		data = expanded
	}
	if makefileMayRemakeIncluded(data, followed) {
		return original, []string{"includes " + strings.Join(followed, ", ") + " not followed: Make may remake them before reading"}
	}
	return data, nil
}

// makefileDefaultSuffixes is GNU Make's default .SUFFIXES list. A file ending in one can be built
// by a built-in suffix rule from a neighbour with another suffix (gen.s from gen.S through .S.s,
// measured against GNU Make 4.4.1), which no name-prefix check on the neighbours sees.
var makefileDefaultSuffixes = []string{
	".out", ".a", ".ln", ".o", ".c", ".cc", ".C", ".cpp", ".p", ".f", ".F", ".m", ".r", ".y", ".l",
	".ym", ".yl", ".s", ".S", ".mod", ".sym", ".def", ".h", ".info", ".dvi", ".tex", ".texinfo",
	".texi", ".txinfo", ".w", ".ch", ".web", ".sh", ".elc", ".el",
}

// makefileMayRemakeIncluded reports whether Make may remake any followed include before reading it.
// It is an allow-list: an include is trusted only when nothing in the combined text can make Make
// look for a source. The text must mention no VPATH, vpath or .SUFFIXES (they widen where and how
// a source is found), hold no pattern rule or double-colon rule (either can build any file), and
// no explicit rule whose target equals a followed operand after Make's normalisation
// (makefileNormalizeName); no operand may end in one of Make's default suffixes.
func makefileMayRemakeIncluded(data string, followed []string) bool {
	if len(followed) == 0 {
		return false
	}
	for _, word := range []string{".SUFFIXES", "VPATH", "vpath"} {
		if strings.Contains(data, word) {
			return true
		}
	}
	targets, unsafe := makefileRuleTargets(data)
	if unsafe {
		return true
	}
	for _, operand := range followed {
		clean := path.Clean(operand)
		if _, named := targets[makefileNormalizeName(operand)]; named || makefileHasDefaultSuffix(clean) {
			return true
		}
		for _, spelling := range []string{operand, clean, "./" + clean} {
			if MakefileMayDefineTarget(data, spelling) {
				return true
			}
		}
	}
	return false
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

// makefileRuleTargets returns the normalised explicit rule targets of data's syntax lines, and
// whether data holds a rule the set cannot describe: a pattern rule (a "%" in the target part) or
// a double-colon rule. Recipe lines are skipped; a line that is not a rule contributes nothing.
func makefileRuleTargets(data string) (map[string]struct{}, bool) {
	lines, whole := makefileLogicalLines(data)
	if !whole {
		return nil, true
	}
	targets := make(map[string]struct{})
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		kind := scanner.next(lines[index])
		if scanner.lost {
			return nil, true
		}
		if kind != makefileSyntaxLine {
			continue
		}
		head, double, isRule := makefileRuleHead(lines[index])
		if isRule && (double || strings.Contains(head, "%")) {
			return nil, true
		}
		for _, field := range strings.Fields(head) {
			targets[makefileNormalizeName(field)] = struct{}{}
		}
	}
	return targets, false
}

// makefileRuleHead splits a syntax line at its first colon: head is the target part, double says
// the colon is followed by another that does not start an assignment, and isRule is false for a
// line with no colon or an assignment (":=", "::=", ":::=").
func makefileRuleHead(line string) (head string, double, isRule bool) {
	trimmed := strings.TrimSpace(line)
	colon := strings.IndexByte(trimmed, ':')
	if colon < 0 || strings.HasPrefix(trimmed, "#") {
		return "", false, false
	}
	if equals := strings.IndexByte(trimmed, '='); equals >= 0 && equals < colon {
		return "", false, false
	}
	rest := trimmed[colon+1:]
	if strings.HasPrefix(rest, "=") || strings.HasPrefix(rest, ":=") || strings.HasPrefix(rest, "::=") {
		return "", false, false
	}
	return trimmed[:colon], strings.HasPrefix(rest, ":"), true
}

// makefileIncludeBoundary is the line spliced before and after every fragment. A variable binding
// closes the recipe the scanner follows, which is what an include does in Make: Make closes the
// including file's open rule at the include, reads the fragment with none open, and continues the
// including file with none open. Without it a recipe, or a recipe state a conditional left unsure,
// would carry across the splice and a tab-prefixed line would read as a recipe line where Make
// parses it as makefile syntax (measured against GNU Make 4.4.1).
const makefileIncludeBoundary = ".PRAETOR_INCLUDE_BOUNDARY := 1"

// makefileExpandLevel replaces the literal include lines of data once. Nested includes the
// fragments bring are left for the next level. When the scanner loses its place anywhere in data
// the whole level is discarded and data comes back unchanged, so nothing is expanded.
func makefileExpandLevel(data string, read MakefileIncludeReader, files *int, followed *[]string) (string, bool) {
	lines, whole := makefileLogicalLines(data)
	if !whole {
		return data, false
	}
	var scanner makefileScanner
	out := make([]string, 0, len(lines))
	changed := false
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		line := lines[index]
		kind := scanner.next(line)
		if scanner.lost {
			return data, false
		}
		if kind == makefileSyntaxLine {
			if text, ok := makefileIncludeText(line, read, files, followed); ok {
				line, changed = text, true
			}
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n"), changed
}

// makefileIncludeText returns the fragment text an include line stands for, and false when line is
// no include of literal paths all of which read vouches for.
func makefileIncludeText(line string, read MakefileIncludeReader, files *int, followed *[]string) (string, bool) {
	fields := strings.Fields(line)
	if len(fields) < 2 || fields[0] != "include" || strings.HasPrefix(line, "\t") {
		return "", false
	}
	parts := make([]string, 0, 2*len(fields))
	operands := make([]string, 0, len(fields)-1)
	parts = append(parts, makefileIncludeBoundary)
	for _, operand := range fields[1:] {
		if !makefileLiteralPath(operand) || *files >= MaxMakefileIncludeFiles {
			return "", false
		}
		*files++
		text, ok := read(operand)
		if !ok || !makefileSpliceable(text) {
			return "", false
		}
		parts = append(parts, strings.TrimSuffix(strings.ReplaceAll(text, "\r\n", "\n"), "\n"))
		parts = append(parts, makefileIncludeBoundary)
		operands = append(operands, operand)
	}
	*followed = append(*followed, operands...)
	return strings.Join(parts, "\n"), true
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
