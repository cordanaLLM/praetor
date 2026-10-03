package util

import (
	"slices"
	"strings"
)

// This file and makefile_define.go hold the one Makefile reader Praetor decides target ownership
// with. Adoption asks it whether a project owns verify-all or docs-lint before appending a rule
// (internal/adopt/governance.go, internal/adopt/verification_makefile.go), and editor generation
// asks it whether to offer a "make verify-all" task (internal/editor/capabilities.go). A second
// line test beside it once read eight assignment forms as rules (issue #304), so a caller never
// decides rule versus assignment on its own.
//
// The reader works on logical lines, as Make does: a line ending in an odd run of backslashes
// continues on the next one (makefileLogicalLines). It reads a Makefile up to the first point it
// cannot resolve -- line MaxMakefileLines + 1, a logical line longer than MaxMakefileLineBytes, or
// a chain of more than MaxMakefileContinuations continuations. A rule before that point counts
// for MakefileHasTarget, nothing at or after it does, and MakefileMayDefineTarget reports the file
// as one only Make can resolve.

// MaxMakefileLines bounds how many physical lines of a Makefile the reader scans (HISS-02). A
// rule past the bound is not read: MakefileHasTarget reports none, and MakefileMayDefineTarget
// reports the longer file as one only Make can resolve.
const MaxMakefileLines = 4096

// MaxMakefileContinuations bounds how many continuation lines the reader joins to one logical
// line (HISS-02): 256 continuations make a logical line of 257 physical lines. A longer chain is
// a point the reader cannot resolve, so the reading stops there.
const MaxMakefileContinuations = 256

// A Makefile line is read token by token; these name what a token turned out to be.
const (
	makefileAssignToken    = "assign"
	makefileRuleToken      = "rule"
	makefileEndToken       = "end"
	makefileReferenceToken = "reference"
)

// MaxMakefileLineBytes bounds the token scan of a single logical Makefile line (HISS-02), joined
// continuations included. Real declarations are far shorter; a longer line is a point the reader
// cannot resolve, so the reading stops there and a caller preserves the file instead of appending
// to it.
const MaxMakefileLineBytes = 8192

// makefileReferenceWidth returns the byte length of the variable reference text opens with, so the
// colon and the "=" inside "$(SRCS:.c=.o)" or "$(call rule,$(P)x,A=b)" are not read as operators.
// "$x" and "$$" span two bytes. Like GNU Make 4.4.1 it counts nested brackets of the kind the
// reference opens with: "$(a $(b),c=d)" ends at its last ")", a "}" inside "$(" is plain text, and
// a reference that is never closed spans the rest of text.
func makefileReferenceWidth(text string) int {
	if len(text) < 2 {
		return 1
	}
	closer := makefileCloser(text[1])
	if closer == 0 {
		return 2
	}
	depth := 0
	for end := 1; end < len(text) && end < MaxMakefileLineBytes; end++ {
		switch text[end] {
		case text[1]:
			depth++
		case closer:
			depth--
		}
		if depth == 0 {
			return end + 1
		}
	}
	return len(text)
}

// makefileCloser returns the bracket that closes a variable reference opened with open, "(" or
// "{", and 0 for any other byte.
func makefileCloser(open byte) byte {
	switch open {
	case '(':
		return ')'
	case '{':
		return '}'
	}
	return 0
}

// makefileReference classifies the "$" text opens with: "$$" is an escaped dollar sign that
// expands to a literal "$", anything else a variable reference Make expands.
func makefileReference(text string) (string, int) {
	if strings.HasPrefix(text, "$$") {
		return "", 2
	}
	return makefileReferenceToken, makefileReferenceWidth(text)
}

// makefileColonOperator classifies the run of colons starting at line[i]. A run followed by "=" is
// an assignment operator (":=", "::=", ":::="); any other run separates targets from prerequisites.
func makefileColonOperator(line string, i int) (string, int) {
	run := i
	for run < len(line) && run < MaxMakefileLineBytes && line[run] == ':' {
		run++
	}
	if run < len(line) && line[run] == '=' {
		return makefileAssignToken, run - i + 1
	}
	return makefileRuleToken, run - i
}

// makefileOperatorAt classifies the token starting at line[i] and reports the bytes it spans. Make
// ends a variable name at "=", "+=", "?=", "!=" or a run of colons followed by "="; it stops
// reading the line at an unescaped "#" (a comment) or ";" (the inline recipe), and a backslash
// escapes the byte behind it -- except "$": Make has no backslash escape for a reference, so
// measured against GNU Make 4.4.1 "\$(R)" with "R = x docs-lint: ; @echo x" declares docs-lint.
func makefileOperatorAt(line string, i int) (string, int) {
	switch line[i] {
	case '=':
		return makefileAssignToken, 1
	case '+', '?', '!':
		if i+1 < len(line) && line[i+1] == '=' {
			return makefileAssignToken, 2
		}
	case ':':
		return makefileColonOperator(line, i)
	case '$':
		return makefileReference(line[i:])
	case '#', ';':
		return makefileEndToken, 1
	case '\\':
		if i+1 < len(line) && line[i+1] != '$' {
			return "", 2
		}
	}
	return "", 1
}

// makefileSplit returns the byte offsets of the first variable-assignment operator, of the first
// rule colon and of the first variable reference on line, each -1 when the line holds none. Make
// reads whichever operator comes first: an assignment first binds a variable whose value may
// itself contain colons ("V = a:b"), a colon first opens a rule ("t: dep"). The scan ends where
// Make stops reading the line -- at a comment or at the ";" that opens an inline recipe -- and at
// MaxMakefileLineBytes (HISS-02).
func makefileSplit(line string) (assign, colon, reference int) {
	assign, colon, reference = -1, -1, -1
	for i := 0; i < len(line) && i < MaxMakefileLineBytes; {
		kind, width := makefileOperatorAt(line, i)
		switch {
		case kind == makefileEndToken:
			return assign, colon, reference
		case kind == makefileAssignToken && assign < 0:
			assign = i
		case kind == makefileRuleToken && colon < 0:
			colon = i
		case kind == makefileReferenceToken && reference < 0:
			reference = i
		}
		i += width
	}
	return assign, colon, reference
}

// makefileBindsVariable reports whether text binds a variable rather than opening a rule, decided
// by whichever operator Make reaches first and by the name in front of it (makefileAssignment).
func makefileBindsVariable(text string) bool {
	binds, _ := makefileAssignment(text)
	return binds
}

// makefileAssignment reports whether Make reaches an assignment operator in text before any rule
// colon, and if so whether it binds a variable (binds) or not, because its name side holds two or
// more words after the modifiers (unnamed). GNU Make takes no blank inside a variable name, so
// measured against GNU Make 4.4.1 "foo bar = docs-lint: ; @echo x" and "override foo bar = ..." are
// rules declaring docs-lint, "$(R) x = y" is a bare expansion, and "export foo bar = ..." stays an
// export directive.
func makefileAssignment(text string) (binds, unnamed bool) {
	assign, colon, _ := makefileSplit(text)
	if assign < 0 || colon >= 0 && colon < assign {
		return false, false
	}
	named := makefileNameWords(text[:assign]) <= 1
	return named, !named
}

// makefileNameWords counts the blank-separated words of text, the name side of an assignment,
// after the override, export, unexport and private modifiers in front of it. A variable reference
// belongs to the word it sits in, so "$(a b)x" is one word.
func makefileNameWords(text string) int {
	words := make([]string, 0, 4)
	start := -1
	for i := 0; i < len(text) && i < MaxMakefileLineBytes; {
		if text[i] == ' ' || text[i] == '\t' {
			if start >= 0 {
				words = append(words, text[start:i])
			}
			start = -1
			i++
			continue
		}
		if start < 0 {
			start = i
		}
		_, width := makefileOperatorAt(text, i)
		i += width
	}
	if start >= 0 {
		words = append(words, text[start:])
	}
	return len(words) - makefileDirectiveIndex(words)
}

// makefileExportDirective reports whether line is an export or unexport directive: its first word,
// followed by whitespace, is "export" or "unexport". Make reads such a line as a list of variables
// to export and never as a rule, colon or not. Measured against GNU Make 4.4.1: "export
// verify-all: dep", "unexport verify-all: dep" and "export : dep" declare no target, while
// "export: dep" declares the target "export" and "override verify-all: dep", "private verify-all:
// dep" and "override export verify-all: dep" declare verify-all -- only the first word decides.
func makefileExportDirective(line string) bool {
	trimmed := strings.TrimLeft(line, " \t")
	end := strings.IndexAny(trimmed, " \t")
	if end < 0 {
		return false
	}
	word := trimmed[:end]
	return word == "export" || word == "unexport"
}

// makefileTargetNames returns the target names a Makefile line declares, and none when the line
// declares no rule. Make cuts the line at the first comment or inline-recipe ";" and decides on
// what is left, so the test runs twice over text Make still reads: once over the whole line and
// once over the prerequisites. Measured against GNU Make 4.4.1: "verify-all := x",
// "verify-all = a:b", "verify-all ?= a:b", "verify-all += x:y", "verify-all != date" and
// "override verify-all := x" all bind a variable, and "verify-all: CFLAGS := -g" -- with or
// without a trailing comment or ";" recipe -- binds a target-specific variable; each answers
// "make verify-all" with "No rule to make target". "verify-all:: dep", "verify-all: $(SRCS:.c=.o)",
// "verify-all: lint ## run gates (FAST=1)" and "verify-all: ; FOO=1 echo c" all declare the target:
// an "=" a comment or a recipe carries is not an assignment operator.
func makefileTargetNames(line string) []string {
	targets, _ := makefileRule(line)
	return targets
}

// makefileRule returns the target names and the prerequisite text of the rule line declares, and
// none when the line declares no rule (makefileTargetNames).
func makefileRule(line string) ([]string, string) {
	if strings.HasPrefix(line, "\t") || makefileExportDirective(line) {
		return nil, ""
	}
	_, colon, _ := makefileSplit(line)
	if colon < 0 || makefileBindsVariable(line) {
		return nil, ""
	}
	_, width := makefileOperatorAt(line, colon)
	prerequisites := line[colon+width:]
	if makefileBindsVariable(prerequisites) {
		return nil, ""
	}
	return strings.Fields(line[:colon]), prerequisites
}

// makefileLogicalLines returns the logical lines of data up to the first point the reader cannot
// resolve, and whether it read the whole file. Measured against GNU Make 4.4.1, a continuation
// joins every kind of line: an assignment ("HELP = usage \" then "  verify-all: x" binds HELP), a
// recipe line ("\techo a \" then "verify-all: x" is recipe text), a comment, a define body line
// (a continued line swallows the "endef" after it) and a rule line ("verify-all \" then
// "  other: dep" declares both targets).
func makefileLogicalLines(data string) ([]string, bool) {
	physical := strings.Split(data, "\n")
	lines := make([]string, 0, min(len(physical), MaxMakefileLines))
	for start := 0; start < len(physical) && start < MaxMakefileLines; {
		line, next, whole := makefileJoin(physical, start)
		if !whole {
			return lines, false
		}
		lines = append(lines, line)
		start = next
	}
	return lines, len(physical) <= MaxMakefileLines
}

// makefileJoin returns the logical line that starts at physical[start], the index of the physical
// line after it, and whether the reader resolves it: false when its continuation chain is longer
// than MaxMakefileContinuations, when it ends past line MaxMakefileLines, or when the joined line
// is longer than MaxMakefileLineBytes. A backslash on the file's last line continues nothing.
func makefileJoin(physical []string, start int) (string, int, bool) {
	end := start
	for end-start < MaxMakefileContinuations && end+1 < len(physical) && makefileContinues(physical[end]) {
		end++
	}
	if end >= MaxMakefileLines || (end+1 < len(physical) && makefileContinues(physical[end])) {
		return "", end + 1, false
	}
	line := makefileJoinText(physical[start : end+1])
	return line, end + 1, len(line) <= MaxMakefileLineBytes
}

// makefileJoinText joins the physical lines of one logical line. A recipe line keeps them as they
// are, joined by "\n", so MakefileTargetRecipe returns the lines the file holds. Any other line
// joins as Make reads it: each backslash-newline and the blanks around it become one space.
func makefileJoinText(parts []string) string {
	if len(parts) == 1 {
		return parts[0]
	}
	if strings.HasPrefix(parts[0], "\t") {
		return strings.Join(parts, "\n")
	}
	words := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSuffix(part, "\r")
		if makefileContinues(part) {
			part = part[:len(part)-1]
		}
		if part = strings.Trim(part, " \t"); part != "" {
			words = append(words, part)
		}
	}
	return strings.Join(words, " ")
}

// MakefileHasTarget reports whether data, the text of a Makefile, declares a rule for target on a
// line this reader resolves without Make: makefileTargetNames decides rule versus assignment, a
// define body is variable text, not rules, so a "target:" line inside one declares nothing, and a
// line makefileLineIsAmbiguous reports, such as "$(PREFIX) verify-all: dep", declares nothing the
// reader can claim. A Makefile that names .RECIPEPREFIX declares nothing the reader can claim
// either (makefileNamesRecipePrefix). MakefileMayDefineTarget reports the files where Make may
// still declare the target some other way. Lines may end in "\n" or "\r\n"; the reading stops
// where makefileLogicalLines does.
func MakefileHasTarget(data, target string) bool {
	lines, _ := makefileLogicalLines(data)
	return makefileTargetLine(lines, target) >= 0
}

// makefileNamesRecipePrefix reports whether line, unless it is a comment line, names .RECIPEPREFIX.
// Assigning it changes which lines are recipe lines: measured against GNU Make 4.4.1, after
// ".RECIPEPREFIX = >" a tab-indented "docs-lint: ; @echo x" declares docs-lint and "> docs-lint:
// dep" is a recipe line. The reader tells recipe lines by the tab alone, so a Makefile that names
// the variable anywhere, in an assignment, an eval or a define body, is one only Make can read.
func makefileNamesRecipePrefix(line string) bool {
	return strings.Contains(line, ".RECIPEPREFIX") && !strings.HasPrefix(strings.TrimSpace(line), "#")
}

// makefileTargetLine returns the index of the first logical line that declares a rule for target,
// or -1 when none does or the Makefile names .RECIPEPREFIX. A tab-prefixed line outside a recipe
// declares nothing: Make stops at it with "recipe commences before first target".
func makefileTargetLine(lines []string, target string) int {
	if slices.ContainsFunc(lines, makefileNamesRecipePrefix) {
		return -1
	}
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		if scanner.next(lines[index]) != makefileSyntaxLine {
			continue
		}
		if slices.Contains(makefileTargetNames(lines[index]), target) &&
			!makefileLineIsAmbiguous(strings.TrimSpace(lines[index])) {
			return index
		}
	}
	return -1
}

// MakefileTargetRecipe returns the tab-prefixed recipe lines, each ending in "\n", that follow
// the first rule data declares for target, and whether data declares one, as MakefileHasTarget
// reads it. A rule without recipe lines, such as "test: build" alone, returns an empty recipe. A
// recipe line continued with a backslash is returned with its continuation lines.
// data holds "\n" line endings: a caller normalizes a CRLF file first.
func MakefileTargetRecipe(data, target string) (string, bool) {
	lines, _ := makefileLogicalLines(data)
	index := makefileTargetLine(lines, target)
	if index < 0 {
		return "", false
	}
	var recipe strings.Builder
	for next := index + 1; next < len(lines) && next < MaxMakefileLines && strings.HasPrefix(lines[next], "\t"); next++ {
		recipe.WriteString(lines[next])
		recipe.WriteByte('\n')
	}
	return recipe.String(), true
}

// makefileDirective returns the first word of a Makefile line that carries meaning, skipping the
// modifiers Make allows in front of a variable assignment or a define. "override", "export",
// "unexport" and "private" take an assignment or a "define" and nothing else, so none of them can
// introduce a rule; "override define recipe" is still a define.
func makefileDirective(fields []string) string {
	if index := makefileDirectiveIndex(fields); index < len(fields) {
		return fields[index]
	}
	return ""
}

// makefileDirectiveIndex returns the index of the first field that is not one of those modifiers,
// or len(fields) when every field is one.
func makefileDirectiveIndex(fields []string) int {
	for index, field := range fields {
		switch field {
		case "override", "export", "unexport", "private":
			continue
		}
		return index
	}
	return len(fields)
}

// makefileLineIsAmbiguous reports whether a line may define targets only Make can resolve: an
// include, a call that evaluates text (makefileCallsEval), a computed or pattern target name, or a
// rule whose prerequisites hold an assignment operator behind two or more words. Measured against
// GNU Make 4.4.1, Make reads "docs-lint: A B = x" as a rule with the prerequisites "A B = x" and
// stops at "docs-lint: A B := x" with "multiple target patterns", so the reader claims neither.
// The line is already trimmed and is neither a recipe line nor part of a define body. A define
// alone is not ambiguous: it only binds a variable, and makefileBareExpansion reports the lines
// that may expand it into rules. A bare modifier is not ambiguous either: measured against GNU
// Make 4.4.1, a Makefile holding "override verify-all := x" or "override CFLAGS += -Wall" beside an
// "all:" rule answers "make verify-all" with "No rule to make target".
func makefileLineIsAmbiguous(line string) bool {
	if strings.HasPrefix(line, "#") {
		return false
	}
	switch makefileDirective(strings.Fields(line)) {
	case "include", "-include", "sinclude":
		return true
	}
	if makefileCallsEval(line) {
		return true
	}
	targets, prerequisites := makefileRule(line)
	if _, unnamed := makefileAssignment(prerequisites); unnamed && targets != nil {
		return true
	}
	for _, name := range targets {
		if strings.ContainsAny(name, "$%") {
			return true
		}
	}
	return false
}

// MakefileMayDefineTarget reports whether data, the text of a Makefile with "\n" line endings,
// may already own target: a rule for it (MakefileHasTarget), a line only Make can resolve (an
// include, an eval call, a computed or pattern target name), a top-level bare expansion other
// than silent calls (makefileBareExpansion, makefileSilentCalls), a tab-prefixed line Make parses
// as one of these because no recipe is open (makefileScanner), a mention of .RECIPEPREFIX, a define
// that is never closed, or a point the reader cannot resolve (makefileLogicalLines), past which an
// unread line may hold any of these (HISS-02). A caller about to append a rule for target must not
// when this reports true: Make would override one of the two recipes.
func MakefileMayDefineTarget(data, target string) bool {
	lines, whole := makefileLogicalLines(data)
	if !whole || slices.ContainsFunc(lines, makefileNamesRecipePrefix) || makefileTargetLine(lines, target) >= 0 {
		return true
	}
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		if makefileLeavesOwnershipToMake(&scanner, lines[index]) {
			return true
		}
	}
	return scanner.depth > 0
}
