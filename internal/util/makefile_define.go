package util

import "strings"

// A define body is variable text: Make turns it into rules only when something parses its
// expansion as makefile syntax. Measured against GNU Make 4.4.1, three things do: an $(eval ...)
// or ${eval ...} call, a top-level line that is a bare expansion such as "$(name)" or
// "$(call name)", and an include that may hold either. A define used only through $(call ...) in a
// recipe declares no target, so it leaves ownership to this reader instead of to Make.
//
// A bare expansion needs no define to declare a rule. Make expands the line and parses the text
// as makefile syntax, so whatever the references hold decides: "$(call make-rule,docs-lint)" with
// "make-rule = $(1): ; @echo x", "$R" with "R = docs-lint: ; @echo x", "$($(N))", "$(value R)",
// "$(file <rules.txt)" and "$(shell cat rules.txt)" all declare docs-lint. Telling which bare
// expansion holds a rule needs Make's evaluation, so the reader evaluates nothing: every bare
// expansion leaves ownership to Make, "$(info ...)", "$(warning ...)" and "$(error ...)" included,
// although those three expand to nothing. That is the price of failing closed.

// makefileDirectiveWords are the first words that make a line a directive rather than a rule, an
// assignment or a bare expansion. Include forms are listed for completeness; makefileLineIsAmbiguous
// reports them before this list is consulted.
var makefileDirectiveWords = map[string]bool{
	"ifeq": true, "ifneq": true, "ifdef": true, "ifndef": true, "else": true, "endif": true,
	"define": true, "endef": true, "undefine": true, "vpath": true,
	"include": true, "-include": true, "sinclude": true,
}

// makefileDefineTracker follows define ... endef nesting line by line the way GNU Make 4.4.1 reads
// it. At top level a define may carry the override, export, unexport or private modifiers, alone or
// combined. Inside a body only a line that does not open with a tab and whose first word is
// exactly "define" or "endef" changes the depth: a tab-indented endef stays body text, and an
// "override define" nested in a body opens nothing, so the first endef closes the outer block.
type makefileDefineTracker struct {
	depth int
}

// body reports whether line belongs to a define block -- its opening line, a body line or its
// closing endef -- and advances the nesting. A tab-indented line at top level is a recipe line,
// never an opening; treating one as ordinary text only makes the reader stricter.
func (d *makefileDefineTracker) body(line string) bool {
	if strings.HasPrefix(line, "\t") {
		return d.depth > 0
	}
	fields := strings.Fields(line)
	if d.depth == 0 {
		if !makefileOpensDefine(fields) {
			return false
		}
		d.depth = 1
		return true
	}
	if len(fields) > 0 && fields[0] == "define" {
		d.depth++
	}
	if len(fields) > 0 && fields[0] == "endef" {
		d.depth--
	}
	return true
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

// makefileLeavesOwnershipToMake reads one logical line, advancing define, and reports whether it
// alone leaves ownership to Make: a define body line that calls eval, a line makefileLineIsAmbiguous
// reports, or a bare expansion. A recipe line is the shell's text and decides nothing.
func makefileLeavesOwnershipToMake(define *makefileDefineTracker, line string) bool {
	if define.body(line) {
		return makefileCallsEval(line)
	}
	if strings.HasPrefix(line, "\t") {
		return false
	}
	trimmed := strings.TrimSpace(line)
	return makefileLineIsAmbiguous(trimmed) || makefileBareExpansion(trimmed)
}

// makefileBareExpansion reports whether a trimmed top-level line is a bare expansion: it holds a
// variable reference Make expands but no assignment operator, no rule colon and no directive, so
// Make parses the expanded text as makefile syntax. The reference need not open the line:
// "docs$(R)" with "R = -lint: ; @echo x" declares docs-lint. A comment, a directive, an assignment,
// a rule, and a line whose references all sit behind its comment or inline recipe are not.
func makefileBareExpansion(line string) bool {
	if strings.HasPrefix(line, "#") || makefileExportDirective(line) ||
		makefileDirectiveWords[makefileDirective(strings.Fields(line))] {
		return false
	}
	assign, colon, reference := makefileSplit(line)
	return assign < 0 && colon < 0 && reference >= 0
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

// makefileCallsEval reports whether line holds an $(eval ...) or ${eval ...} call.
func makefileCallsEval(line string) bool {
	return strings.Contains(line, "$(eval") || strings.Contains(line, "${eval")
}
