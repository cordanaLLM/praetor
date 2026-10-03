package util

import (
	"slices"
	"strings"
)

// A define body is variable text: Make turns it into rules only when something parses its
// expansion as makefile syntax. Measured against GNU Make 4.4.1, three things do: a call that
// evaluates text (makefileCallsEval), a top-level line that is a bare expansion such as "$(name)"
// or "$(call name)", and an include that may hold either. A define used only through $(call ...) in
// a recipe declares no target, so it leaves ownership to this reader instead of to Make.
//
// A bare expansion needs no define to declare a rule. Make expands the line and parses the text
// as makefile syntax, so whatever the references hold decides: "$(call make-rule,docs-lint)" with
// "make-rule = $(1): ; @echo x", "$R" with "R = docs-lint: ; @echo x", "$($(N))", "$(value R)",
// "$(file <rules.txt)" and "$(shell cat rules.txt)" all declare docs-lint. Telling which bare
// expansion holds a rule needs Make's evaluation, so the reader evaluates nothing: every bare
// expansion leaves ownership to Make, $(if ...,$(error x)) included. One shape is exempt because
// it expands to nothing whatever its references hold: a line made only of info, warning and error
// calls (makefileSilentCalls), such as the "$(warning ...)" spf13/cobra's Makefile prints when
// golangci-lint is missing.

// makefileDirectiveWords are the first words, besides makefileConditionalWords, that make a line a
// directive rather than a rule, an assignment or a bare expansion. Include forms are listed for
// completeness; makefileLineIsAmbiguous reports them before this list is consulted.
var makefileDirectiveWords = map[string]bool{
	"define": true, "endef": true, "undefine": true, "vpath": true,
	"include": true, "-include": true, "sinclude": true,
}

// makefileConditionalWords are the first words of a conditional directive. Unlike every other
// line but a blank line or a comment, a conditional keeps a rule's recipe open: measured against
// GNU Make 4.4.1, "all:", "ifdef MAKE", a tab-indented "-include rules.mk" and "endif" make the
// include a recipe line of all.
var makefileConditionalWords = map[string]bool{
	"ifeq": true, "ifneq": true, "ifdef": true, "ifndef": true, "else": true, "endif": true,
}

// What a logical line is to Make, as makefileScanner.next reads it.
const (
	makefileSyntaxLine = iota // a line Make parses as makefile syntax
	makefileRecipeLine        // a recipe line of the rule above it
	makefileDefineLine        // the opening line, a body line or the closing endef of a define
)

// makefileScanner follows the two states GNU Make 4.4.1 reads a line in: define ... endef nesting,
// and whether a rule's recipe is open. A tab-prefixed line is a recipe line only while a recipe is
// open, that is after a rule line with nothing but blank lines, comments, conditionals and recipe
// lines since. Before the first rule, or after an assignment, a define, an include, an export or
// vpath directive or a bare expansion, Make parses a tab-prefixed line as makefile syntax: measured,
// a tab-indented "include rules.mk", "X := $(eval docs-lint: ; @echo x)" or "define X" there does
// what it does unindented, and a tab-indented rule or bare expansion stops Make with "recipe
// commences before first target".
//
// At top level a define may carry the override, export, unexport or private modifiers, alone or
// combined. Inside a body only a line that does not open with a tab and whose first word is
// exactly "define" or "endef" changes the depth: a tab-indented endef stays body text, and an
// "override define" nested in a body opens nothing, so the first endef closes the outer block.
type makefileScanner struct {
	depth  int
	recipe bool
}

// next reports what line is to Make and advances the define nesting and the recipe state.
func (s *makefileScanner) next(line string) int {
	if s.depth > 0 {
		s.body(line)
		return makefileDefineLine
	}
	if s.recipe && strings.HasPrefix(line, "\t") {
		return makefileRecipeLine
	}
	fields := strings.Fields(line)
	if makefileOpensDefine(fields) {
		s.depth, s.recipe = 1, false
		return makefileDefineLine
	}
	s.recipe = makefileKeepsRecipe(line, fields, s.recipe)
	return makefileSyntaxLine
}

// body advances the nesting over one line of a define body.
func (s *makefileScanner) body(line string) {
	fields := strings.Fields(line)
	if strings.HasPrefix(line, "\t") || len(fields) == 0 {
		return
	}
	switch fields[0] {
	case "define":
		s.depth++
	case "endef":
		s.depth--
	}
}

// makefileKeepsRecipe reports whether a recipe is open after line, a makefile syntax line, given
// whether one was open before it. A blank line, a comment and a conditional keep the state, a rule
// line opens a recipe, and any other line closes it, measured against GNU Make 4.4.1 for an
// assignment, a target-specific variable, define, undefine, include, export, unexport, vpath and
// a bare expansion. Make tests for an assignment first, so "ifdef = 1" closes the recipe.
func makefileKeepsRecipe(line string, fields []string, open bool) bool {
	switch {
	case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
		return open
	case makefileBindsVariable(line):
		return false
	case makefileConditionalWords[fields[0]]:
		return open
	}
	return makefileTargetNames(line) != nil
}

// makefileOpensDefine reports whether a top-level line opens a define block: its first word after
// the modifiers is "define" and the next word does not start with an assignment operator. Measured
// against GNU Make 4.4.1, "define := x" and "override define ?= x" bind a variable named define
// and a rule on the next line stays a rule, while "define name" and "define name =" open a block.
func makefileOpensDefine(fields []string) bool {
	index := makefileDirectiveIndex(fields)
	if index >= len(fields) || fields[index] != "define" {
		return false
	}
	if index+1 == len(fields) {
		return true
	}
	kind, _ := makefileOperatorAt(fields[index+1], 0)
	return kind != makefileAssignToken
}

// makefileLeavesOwnershipToMake reads one logical line, advancing scanner, and reports whether it
// alone leaves ownership to Make: a define line that calls eval, a line makefileLineIsAmbiguous
// reports, or a bare expansion other than silent calls. A recipe line is the shell's text and
// decides nothing; a tab-prefixed line outside a recipe is makefile syntax and is read as one.
func makefileLeavesOwnershipToMake(scanner *makefileScanner, line string) bool {
	switch scanner.next(line) {
	case makefileDefineLine:
		return makefileCallsEval(line)
	case makefileRecipeLine:
		return false
	}
	trimmed := strings.TrimSpace(line)
	return makefileLineIsAmbiguous(trimmed) || makefileBareExpansion(trimmed) && !makefileSilentCalls(trimmed)
}

// makefileBareExpansion reports whether a trimmed top-level line is a bare expansion: it holds a
// variable reference Make expands but binds no variable and has no rule colon and no directive,
// so Make parses the expanded text as makefile syntax. The reference need not open the line:
// "docs$(R)" with "R = -lint: ; @echo x" declares docs-lint, and so does "$(R) x = y" with "R =
// docs-lint: ; @echo x", since a name side of two words binds nothing (makefileAssignment). A
// comment, a directive, an assignment, a rule, and a line whose references all sit behind its
// comment or inline recipe are not.
func makefileBareExpansion(line string) bool {
	first := makefileDirective(strings.Fields(line))
	if strings.HasPrefix(line, "#") || makefileExportDirective(line) ||
		makefileDirectiveWords[first] || makefileConditionalWords[first] {
		return false
	}
	_, colon, reference := makefileSplit(line)
	return colon < 0 && reference >= 0 && !makefileBindsVariable(line)
}

// makefileSilentFunctions print a message or stop Make and expand to nothing.
var makefileSilentFunctions = []string{"info", "warning", "error"}

// makefileSilentCalls reports whether a trimmed line consists only of info, warning and error calls
// separated by blanks, each one makefileSilentCall accepts. Such a line expands to nothing, so Make
// parses no makefile syntax from it: measured against GNU Make 4.4.1, "$(info a) $(warning b)" and
// spf13/cobra's "$(warning "could not find golangci-lint in $(PATH), run: ...")" declare no target.
// Anything outside the calls makes the line a bare expansion again: "$(warning x) $(R)" with "R =
// docs-lint: ; @echo x" declares docs-lint, and so does "$(if $(V),,$(error x))" whenever its
// condition expands a rule.
func makefileSilentCalls(line string) bool {
	calls := 0
	for i := 0; i < len(line) && i < MaxMakefileLineBytes; {
		if line[i] == ' ' || line[i] == '\t' {
			i++
			continue
		}
		width := makefileReferenceWidth(line[i:])
		if line[i] != '$' || !makefileSilentCall(line[i:i+width]) {
			return false
		}
		calls++
		i += width
	}
	return calls > 0
}

// makefileSilentCall reports whether reference, one variable reference closed by the bracket it
// opens with, calls info, warning or error -- the name and a blank right after the bracket, since
// "$(info)" references a variable named info -- and calls nothing inside that evaluates text, runs
// a command or calls another function: eval, guile, call or shell. A plain variable reference
// inside expands its value, and a value that evaluates text holds a call makefileCallsEval reports
// on its own line.
func makefileSilentCall(reference string) bool {
	if len(reference) < 3 || reference[len(reference)-1] != makefileCloser(reference[1]) {
		return false
	}
	inner := reference[2 : len(reference)-1]
	end := strings.IndexAny(inner, " \t")
	if end < 0 || !slices.Contains(makefileSilentFunctions, inner[:end]) {
		return false
	}
	return !makefileCallsFunction(inner, func(name, _ string) bool {
		return name == "eval" || name == "guile" || name == "call" || name == "shell"
	})
}

// makefileContinues reports whether line ends in an odd run of backslashes, which joins the next
// physical line to it. A carriage return left by a CRLF file does not break the run.
func makefileContinues(line string) bool {
	line = strings.TrimSuffix(line, "\r")
	run := 0
	for run < len(line) && run < MaxMakefileLineBytes && line[len(line)-1-run] == '\\' {
		run++
	}
	return run%2 == 1
}

// makefileCallsEval reports whether line holds a call that parses text as makefile syntax: an
// $(eval ...) or ${eval ...} call, a $(guile ...) call, whose gmk-eval does the same, or a $(call
// ...) of eval or of a computed name, which may name eval: GNU Make's call invokes the built-in
// function its first argument names.
func makefileCallsEval(line string) bool {
	return makefileCallsFunction(line, func(name, arguments string) bool {
		return name == "eval" || name == "guile" ||
			name == "call" && (strings.HasPrefix(arguments, "eval") || strings.HasPrefix(arguments, "$"))
	})
}

// makefileCallsFunction reports whether text opens a function call match accepts. Each "$(" or
// "${" opens a candidate: its name runs to the first blank or the end of text, as GNU Make reads
// a function name, and its arguments start after the blanks behind the name. An escaped "$$(" is
// tested too, which only makes a caller stricter.
func makefileCallsFunction(text string, match func(name, arguments string) bool) bool {
	for i := 0; i+1 < len(text) && i < MaxMakefileLineBytes; i++ {
		if text[i] != '$' || text[i+1] != '(' && text[i+1] != '{' {
			continue
		}
		rest := text[i+2:]
		end := strings.IndexAny(rest, " \t")
		if end < 0 {
			end = len(rest)
		}
		if match(rest[:end], strings.TrimLeft(rest[end:], " \t")) {
			return true
		}
	}
	return false
}
