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

// makefileLeavesOwnershipToMake reads one logical line, advancing scanner, and reports whether it
// alone leaves ownership to Make: a define line that calls eval or binds a command's output, a
// line makefileLineIsAmbiguous reports, a bare expansion other than silent calls, an unsure line
// that makefileUnsureLineMayDefine reports, or a line the scanner cannot resolve. A recipe line is
// the shell's text and decides nothing; a tab-prefixed line outside a recipe is makefile syntax and
// is read as one.
func makefileLeavesOwnershipToMake(scanner *makefileScanner, line string) bool {
	trimmed := strings.TrimSpace(line)
	kind := scanner.next(line)
	switch {
	case scanner.lost:
		return true
	case kind == makefileDefineLine:
		return makefileCallsEval(line) || makefileDefinesCommandOutput(trimmed)
	case kind == makefileRecipeLine:
		return false
	case kind == makefileUnsureLine:
		return makefileUnsureLineMayDefine(trimmed)
	}
	return makefileLineIsAmbiguous(trimmed) || makefileBareExpansion(trimmed) && !makefileSilentCalls(trimmed)
}

// makefileUnsureLineMayDefine reports whether a trimmed tab-prefixed line that Make reads as a
// recipe line or as makefile syntax, depending on a branch it takes, leaves ownership to Make when
// read as syntax: an include directive, a call that evaluates text or a "!=" binding. Measured
// against GNU Make 4.4.1, after "ifdef UNSET", "foo:" and "endif" a tab-indented "X := $(eval
// docs-lint: ; @echo x)" declares docs-lint. A rule or a bare expansion read as syntax stops Make
// with "recipe commences before first target", so neither counts, and a recipe line such as
// "@printf '%s: done'" stays readable.
func makefileUnsureLineMayDefine(line string) bool {
	return makefileIncludes(strings.Fields(line)) || makefileCallsEval(line) || makefileBindsCommandOutput(line)
}

// makefileBindsCommandOutput reports whether a trimmed line binds a variable with "!=". Make runs
// the command and stores its output as the value of a recursively expanded variable, so it expands
// that output as makefile text wherever the variable is expanded: measured against GNU Make 4.4.1,
// "X != cat rules.txt" with rules.txt holding "$(eval docs-lint: ; @echo x)" declares docs-lint
// from "all: $(X)", "Y := $(X)", "ifeq ($(X),)" and "$(info $(X))". The reader runs no command.
func makefileBindsCommandOutput(line string) bool {
	assign, _, _ := makefileSplit(line)
	return makefileBindsVariable(line) && line[assign] == '!'
}

// makefileDefinesCommandOutput reports whether a trimmed line opens a define that binds a
// command's output, "define X !=" or "define X!=": the body is a command whose output Make expands
// like that of a "!=" binding (makefileBindsCommandOutput).
func makefileDefinesCommandOutput(line string) bool {
	return strings.HasSuffix(line, "!=") && makefileOpensDefine(strings.Fields(line))
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
// on its own line, or comes from a "!=" binding, which makefileBindsCommandOutput reports.
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
// ...) whose first argument may name one of makefileCallFunctions (makefileCallNamesEval): GNU
// Make's call invokes the built-in function its first argument names.
func makefileCallsEval(line string) bool {
	return makefileCallsFunction(line, func(name, arguments string) bool {
		return name == "eval" || name == "guile" || name == "call" && makefileCallNamesEval(arguments)
	})
}

// makefileCallFunctions are the built-in functions a $(call ...) can invoke by name to parse text
// as makefile syntax. Measured against GNU Make 4.4.1, "X := $(call eval,docs-lint: ; @echo x)",
// "$(call guile,(gmk-eval ...))" and "$(call call,eval,docs-lint: ; @echo x)" each declare
// docs-lint.
var makefileCallFunctions = []string{"eval", "guile", "call"}

// makefileCallNamesEval reports whether arguments, the text of a $(call ...) after its name, open
// with a first argument that may name one of makefileCallFunctions. Make trims the blanks around
// that name, and a name holding a reference may expand to one (makefileNameMayBe): measured
// against GNU Make 4.4.1, "$(call e$(S),docs-lint: ; @echo x)" with "S = val" declares docs-lint,
// while "build_$(ARCH)" can name none of them.
func makefileCallNamesEval(arguments string) bool {
	first, _, _ := strings.Cut(arguments, ",")
	first = strings.TrimSpace(first)
	return slices.ContainsFunc(makefileCallFunctions, func(function string) bool {
		return makefileNameMayBe(first, function)
	})
}

// makefileCallsFunction reports whether text opens a function call match accepts. Each "$(" or
// "${" opens a candidate that ends at the bracket closing it (makefileReferenceWidth): its name
// runs to the first blank inside, as GNU Make reads a function name, and its arguments start after
// the blanks behind the name. An escaped "$$(" is tested too, which only makes a caller stricter.
func makefileCallsFunction(text string, match func(name, arguments string) bool) bool {
	for i := 0; i+1 < len(text) && i < MaxMakefileLineBytes; i++ {
		if text[i] != '$' || text[i+1] != '(' && text[i+1] != '{' {
			continue
		}
		reference := text[i : i+makefileReferenceWidth(text[i:])]
		inner := strings.TrimSuffix(reference[2:], string(makefileCloser(text[i+1])))
		end := strings.IndexAny(inner, " \t")
		if end < 0 {
			end = len(inner)
		}
		if match(inner[:end], strings.TrimLeft(inner[end:], " \t")) {
			return true
		}
	}
	return false
}
