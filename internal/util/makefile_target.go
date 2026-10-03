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

// MaxMakefileLines bounds how many lines of a Makefile the reader scans (HISS-02). A rule past
// the bound is not read: MakefileHasTarget reports none, and MakefileMayDefineTarget reports the
// longer file as one only Make can resolve.
const MaxMakefileLines = 4096

// MaxMakefileContinuations bounds how many physical continuation lines can be joined into a single
// logical line (HISS-02).
const MaxMakefileContinuations = 256

// A Makefile line is read token by token; these name what a token turned out to be.
const (
	makefileAssignToken    = "assign"
	makefileRuleToken      = "rule"
	makefileEndToken       = "end"
	makefileReferenceToken = "reference"
)

// MaxMakefileLineBytes bounds the token scan of a single Makefile line (HISS-02). Real declarations
// are far shorter; a line past the bound is read only in part, so its ownership is unresolved, and
// makefileLineIsAmbiguous reports it as ambiguous so a caller preserves the file instead of
// appending to it.
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
	var closer byte
	switch text[1] {
	case '(':
		closer = ')'
	case '{':
		closer = '}'
	default:
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
// escapes the byte behind it.
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
		return "", 2
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
// by whichever operator Make reaches first.
func makefileBindsVariable(text string) bool {
	assign, colon, _ := makefileSplit(text)
	return assign >= 0 && (colon < 0 || assign < colon)
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
	if strings.HasPrefix(line, "\t") || makefileExportDirective(line) {
		return nil
	}
	_, colon, _ := makefileSplit(line)
	if colon < 0 || makefileBindsVariable(line) {
		return nil
	}
	_, width := makefileOperatorAt(line, colon)
	if makefileBindsVariable(line[colon+width:]) {
		return nil
	}
	return strings.Fields(line[:colon])
}

// makefileLineIsRule reports whether line opens a rule rather than an assignment or directive.
func makefileLineIsRule(line string) bool {
	if strings.HasPrefix(line, "\t") || makefileExportDirective(line) {
		return false
	}
	_, colon, _ := makefileSplit(line)
	if colon < 0 || makefileBindsVariable(line) {
		return false
	}
	_, width := makefileOperatorAt(line, colon)
	return !makefileBindsVariable(line[colon+width:])
}

// makefileJoinRecipeContinuation joins physical continuation lines for a recipe line that begins
// with a tab (HISS-02).
func makefileJoinRecipeContinuation(lines []string, start int) (string, int) {
	line := strings.TrimSuffix(lines[start], "\r")
	if !makefileContinues(line) {
		return line, 1
	}
	var joined strings.Builder
	joined.WriteString(line)
	consumed := 1
	for start+consumed < len(lines) && start+consumed < MaxMakefileLines && consumed < MaxMakefileContinuations {
		if !makefileContinues(line) || joined.Len() >= MaxMakefileLineBytes {
			break
		}
		next := strings.TrimSuffix(lines[start+consumed], "\r")
		joined.WriteByte('\n')
		joined.WriteString(next)
		line = next
		consumed++
	}
	return joined.String(), consumed
}

// makefileJoinStatementContinuation joins backslash-continued non-recipe lines (variable
// assignments, rule lines, directives), collapsing whitespace according to GNU Make rules (HISS-02).
func makefileJoinStatementContinuation(lines []string, start int) (string, int) {
	line := strings.TrimSuffix(lines[start], "\r")
	if !makefileContinues(line) {
		return line, 1
	}
	trimmed := strings.TrimRight(line[:len(line)-1], " \t")
	var joined strings.Builder
	joined.WriteString(trimmed)
	consumed := 1
	for start+consumed < len(lines) && start+consumed < MaxMakefileLines && consumed < MaxMakefileContinuations {
		if !makefileContinues(line) || joined.Len() >= MaxMakefileLineBytes {
			break
		}
		next := strings.TrimSuffix(lines[start+consumed], "\r")
		nextContent := next
		if makefileContinues(next) {
			nextContent = next[:len(next)-1]
		}
		joined.WriteByte(' ')
		joined.WriteString(strings.TrimLeft(strings.TrimRight(nextContent, " \t"), " \t"))
		line = next
		consumed++
	}
	return joined.String(), consumed
}

// makefileLogicalLines joins backslash continuations into logical lines before classifying (HISS-02):
// a backslash-newline in a variable assignment, a rule line or a recipe line.
func makefileLogicalLines(data string) []string {
	physical := strings.Split(data, "\n")
	result := make([]string, 0, len(physical))
	var define makefileDefineTracker
	inRule := false
	for index := 0; index < len(physical) && index < MaxMakefileLines; {
		raw := strings.TrimSuffix(physical[index], "\r")
		if define.body(raw) {
			result = append(result, raw)
			index++
			inRule = false
			continue
		}
		if inRule && strings.HasPrefix(raw, "\t") {
			line, consumed := makefileJoinRecipeContinuation(physical, index)
			result = append(result, line)
			index += consumed
			continue
		}
		line, consumed := makefileJoinStatementContinuation(physical, index)
		result = append(result, line)
		index += consumed
		if strings.TrimSpace(line) != "" && !strings.HasPrefix(strings.TrimSpace(line), "#") {
			inRule = makefileLineIsRule(line)
		}
	}
	return result
}

// MakefileHasTarget reports whether data, the text of a Makefile, declares a rule for target on a
// line this reader resolves without Make: makefileTargetNames decides rule versus assignment, and
// a define body is variable text, not rules, so a "target:" line inside one declares nothing.
// MakefileMayDefineTarget reports the files where Make may still declare the target some other
// way. Lines may end in "\n" or "\r\n"; only the first MaxMakefileLines lines are read.
func MakefileHasTarget(data, target string) bool {
	return makefileTargetLine(makefileLogicalLines(data), target) >= 0
}

// makefileTargetLine returns the index of the first line that declares a rule for target, or
// -1 when none within the scan bound does.
func makefileTargetLine(lines []string, target string) int {
	var define makefileDefineTracker
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		if define.body(lines[index]) {
			continue
		}
		if slices.Contains(makefileTargetNames(lines[index]), target) {
			return index
		}
	}
	return -1
}

// MakefileTargetRecipe returns the tab-prefixed recipe lines, each ending in "\n", that follow
// the first rule data declares for target, and whether data declares one, as MakefileHasTarget
// reads it. A rule without recipe lines, such as "test: build" alone, returns an empty recipe.
// data holds "\n" line endings: a caller normalizes a CRLF file first.
func MakefileTargetRecipe(data, target string) (string, bool) {
	lines := makefileLogicalLines(data)
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
// include, an $(eval ...) or ${eval ...} call, a computed or pattern target name, or a line longer
// than the scan bound, which is read in part and therefore unresolved. The line is already trimmed
// and is neither a recipe line nor part of a define body. A define alone is not ambiguous: it only
// binds a variable, and makefileOwnershipScan reports the files that may expand it into rules. A
// bare modifier is not ambiguous either: measured against GNU Make 4.4.1, a Makefile holding
// "override verify-all := x" or "override CFLAGS += -Wall" beside an "all:" rule answers
// "make verify-all" with "No rule to make target".
func makefileLineIsAmbiguous(line string) bool {
	if strings.HasPrefix(line, "#") {
		return false
	}
	if len(line) > MaxMakefileLineBytes {
		return true
	}
	switch makefileDirective(strings.Fields(line)) {
	case "include", "-include", "sinclude":
		return true
	}
	if makefileCallsEval(line) {
		return true
	}
	for _, name := range makefileTargetNames(line) {
		if strings.ContainsAny(name, "$%") {
			return true
		}
	}
	return false
}

// MakefileMayDefineTarget reports whether data, the text of a Makefile with "\n" line endings,
// may already own target: a rule for it (MakefileHasTarget), a line only Make can resolve (an
// include, an eval call, a computed or pattern target name, a line past the scan bound), a define
// Make may expand into rules or that is never closed, a top-level expansion that could produce a
// rule (a call of a variable holding a colon, $(shell ...), $(A)$(B)), or more than
// MaxMakefileLines lines, whose unread tail may hold any of these (HISS-02). A caller about to
// append a rule for target must not when this reports true: Make would override one of the two
// recipes.
func MakefileMayDefineTarget(data, target string) bool {
	if MakefileHasTarget(data, target) {
		return true
	}
	lines := strings.Split(data, "\n")
	if len(lines) > MaxMakefileLines {
		return true
	}
	logicalLines := makefileLogicalLines(data)
	colonVars := makefileColonVariables(logicalLines)
	var scan makefileOwnershipScan
	scan.colonVars = colonVars
	for index := 0; index < len(logicalLines) && index < MaxMakefileLines; index++ {
		if scan.read(logicalLines[index]) {
			return true
		}
	}
	return scan.unresolved()
}
