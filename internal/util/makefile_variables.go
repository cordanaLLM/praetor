package util

import (
	"slices"
	"strings"
)

// A computed target name such as "$(OUT_DIR):" is resolved only where the file alone fixes the
// value Make expands it to when it reads the rule. The reader admits a variable by an allow-list
// (makefileReadVariables): it is bound exactly once in the file, by a top-level "NAME := value",
// "NAME ::= value" or "NAME = value" outside every conditional, with no modifier, on a line before
// the reference; its value holds only plain path text (makefileValueByte) and, for ":=" and "::=",
// references to variables admitted the same way, at most MaxMakefileVariableDepth bindings deep.
// Anything else leaves the name to Make, as before: a variable bound twice, with "?=", "+=" or
// define, removed by undefine, bound with override, export, unexport or private, bound for a
// target, bound inside a conditional or by a computed name that may be it; a value holding a
// function call, $(shell ...) included, or a pattern, glob, comment-escape or rule character; and
// a variable bound nowhere in the file or only after the rule, whose value Make then takes from
// the environment. A file holding an include, a load, an eval call, a "!=" binding (a "define X
// !=" included) or a bare expansion other than silent calls, each of which may rebind any
// variable, resolves no name (makefileEvaluatesText); such a line leaves the file to Make anyway
// (makefileLeavesOwnershipToMake), and resolving nothing keeps MakefileHasTarget from claiming a
// computed name there. Neither does a file whose computed binding names hold more than
// MaxMakefileComputedRuns literal runs.
//
// The answer holds for the invocation Praetor's gates run, "make verify-all" or "make docs-lint"
// with no variable definitions and no options (verifyCommand in internal/adopt/harness.go). A
// command-line definition such as "make OUT_DIR=docs-lint", -e with the variable in the
// environment, and MAKEFLAGS or MAKEFILES in the environment change what a bound variable holds,
// and --eval declares any rule in any Makefile, literal names included, so no reading of the file
// excludes them: whoever passes them decides the rules. Inside the file, one variable reaches the
// same switches: measured against GNU Make 4.4.1, "MAKEFLAGS += X=docs-lint" after "X := foo"
// makes "$(X):" declare docs-lint, and "MAKEFLAGS += -e" before "X := foo" lets an X from the
// environment win. A file that binds a variable whose name may be MAKEFLAGS resolves no name,
// unless each such binding is a literal list of makefileHarmlessFlags.

// MaxMakefileVariableDepth bounds how many bindings deep the reader follows references to fix a
// computed target name (HISS-02): "OUT := $(ROOT)/out" after "ROOT := lib" is two deep. A deeper
// chain leaves the name to Make.
const MaxMakefileVariableDepth = 16

// MaxMakefileComputedRuns bounds the literal runs (makefileLiteralRuns) the computed binding names
// of one Makefile may hold while the reader still fixes variables (HISS-02). Each computed binding
// name is split once, and makefileBindings.fixed compares a variable it may fix with every run, so
// the bound holds that work to MaxMakefileLines times this many comparisons. A file whose computed
// binding names hold more resolves no computed name.
const MaxMakefileComputedRuns = 256

// makefileHarmlessFlags are the MAKEFLAGS words measured against GNU Make 4.4.1 to leave every
// variable's value alone, bound before or after the variable: none defines a variable, and none
// lets the environment override the file, as "-e", "--environment-overrides" or a first word "e"
// does.
var makefileHarmlessFlags = []string{
	"--no-print-directory", "--no-builtin-rules", "--no-builtin-variables",
	"--warn-undefined-variables", "--silent", "-r", "-R", "-s", "-rR",
}

// makefileFlagsVariable is the variable Make reads its options and command-line variable
// definitions from, again whenever the file binds it.
const makefileFlagsVariable = "MAKEFLAGS"

// makefileValue is a value the reader fixes: its text, the logical line that binds it and how many
// bindings deep its references go.
type makefileValue struct {
	text        string
	line, depth int
}

// makefileVariables are the values the reader fixes for one Makefile (makefileReadVariables) and
// the work it took to collect the bindings and fix them (makefileBindings.steps).
type makefileVariables struct {
	values map[string]makefileValue
	steps  int
}

// makefileBinding is a top-level binding the reader may fix: one plain name, its operator, the
// text after the operator and the logical line it sits on.
type makefileBinding struct {
	name, operator, value string
	line                  int
}

// makefileBindings records how a Makefile binds variables: how often each literal name is bound,
// in any form; the computed names that may bind any variable they can expand to, each split once,
// and how many literal runs they hold (runs); the bindings the reader may fix; whether a binding
// of MAKEFLAGS may change values (flags); and whether a line makes Make parse text it computes or
// reads, which may bind any variable (evaluates). steps counts the work: the bytes of every bound
// name split into literal runs and the runs fixed compares a variable's name with.
type makefileBindings struct {
	count     map[string]int
	computed  []makefileName
	runs      int
	plain     []makefileBinding
	flags     bool
	evaluates bool
	steps     int
}

// makefileReadVariables returns the values the reader fixes for lines, the logical lines of a
// Makefile, by the allow-list this file opens with.
func makefileReadVariables(lines []string) makefileVariables {
	bindings := makefileCollectBindings(lines)
	variables := makefileVariables{values: make(map[string]makefileValue)}
	if bindings.flags || bindings.evaluates || bindings.runs > MaxMakefileComputedRuns {
		variables.steps = bindings.steps
		return variables
	}
	for _, binding := range bindings.plain {
		if !bindings.fixed(binding.name) {
			continue
		}
		if value, ok := variables.bind(binding); ok {
			variables.values[binding.name] = value
		}
	}
	variables.steps = bindings.steps
	return variables
}

// makefileCollectBindings records every binding on a line Make parses as makefile syntax, an
// unsure line included: the opening line of a define and every line makefileBindings.line reads.
// Define bodies and recipe lines bind nothing, unless a body calls eval. The reading stops where
// the scanner stops; a caller leaves such a file to Make before it resolves any name.
func makefileCollectBindings(lines []string) makefileBindings {
	bindings := makefileBindings{count: make(map[string]int)}
	var scanner makefileScanner
	for index := 0; index < len(lines) && index < MaxMakefileLines; index++ {
		opening := scanner.depth == 0
		kind := scanner.next(lines[index])
		switch {
		case scanner.lost:
			return bindings
		case kind == makefileRecipeLine:
		case kind == makefileDefineLine:
			bindings.define(opening, strings.TrimSpace(lines[index]))
		default:
			top := kind == makefileSyntaxLine && len(scanner.branches) == 0
			bindings.evaluates = bindings.evaluates || makefileEvaluatesText(strings.TrimSpace(lines[index]))
			bindings.line(index, strings.TrimLeft(strings.TrimSuffix(lines[index], "\r"), " \t"), top)
		}
	}
	return bindings
}

// makefileEvaluatesText reports whether a trimmed syntax line makes Make parse text it computes or
// reads, which may bind any variable before a rule names it: a line makefileParsesComputedText
// reports, an include, a call that evaluates text or a "!=" binding, or a bare expansion other
// than silent calls. Measured against GNU Make 4.4.1, "$(eval NAME := build)", a bare "$(R)" with
// "R = NAME := build", or "X != cat rebind.txt" with rebind.txt holding "$(eval NAME := build)"
// and a later "all: $(X)", between "NAME := verify-all" and "$(NAME):" declares build.
// makefileLeavesOwnershipToMake reports each of these lines as well.
func makefileEvaluatesText(line string) bool {
	return makefileParsesComputedText(line) || makefileBareExpansion(line) && !makefileSilentCalls(line)
}

// define records a line of a define, trimmed: the opening line binds the variable it names, and a
// line makefileDefineParsesText reports, a body line calling eval or a "define X !=" opening, may
// bind any variable.
func (b *makefileBindings) define(opening bool, line string) {
	b.evaluates = b.evaluates || makefileDefineParsesText(line)
	if opening {
		name, _ := makefileBoundName(line)
		b.add(name, false)
	}
}

// line records the bindings of text, a syntax line at index without its leading blanks: the
// variables an undefine names, the variable of a target-specific binding, and every word in front
// of a top-level assignment operator. The binding is one the reader may fix when it sits outside
// every conditional on a syntax line (top), binds one word and carries no modifier.
func (b *makefileBindings) line(index int, text string, top bool) {
	fields := strings.Fields(text)
	assign, colon, _ := makefileSplit(text)
	switch {
	case assign < 0:
		b.undefine(fields)
		return
	case makefileConditionalLine(text, fields):
		return
	}
	_, width := makefileOperatorAt(text, assign)
	binding := makefileBinding{operator: text[assign : assign+width], value: text[assign+width:], line: index}
	harmless := makefileHarmlessFlagsValue(binding.operator, binding.value)
	if colon >= 0 && colon < assign {
		_, after := makefileOperatorAt(text, colon)
		b.addAll(makefileNameWords(text[colon+after:assign]), harmless)
		return
	}
	names := makefileNameWords(text[:assign])
	b.addAll(names, harmless)
	if top && len(names) == 1 && makefileDirectiveIndex(strings.Fields(text[:assign])) == 0 {
		binding.name = names[0]
		b.plain = append(b.plain, binding)
	}
}

// undefine records the variables an undefine directive, whose words are fields, removes. Make then
// expands each of them to nothing, or to a value bound after the directive.
func (b *makefileBindings) undefine(fields []string) {
	if makefileDirective(fields) != "undefine" {
		return
	}
	b.addAll(fields[makefileDirectiveIndex(fields)+1:], false)
}

// addAll records one binding of each of names.
func (b *makefileBindings) addAll(names []string, harmless bool) {
	for _, name := range names {
		b.add(name, harmless)
	}
}

// add records one binding of name, split once into its literal runs (makefileSplitName). A name
// holding a reference may bind any variable it can expand to (makefileName.mayBe). A binding whose
// name may be MAKEFLAGS may change values unless the name is MAKEFLAGS itself and the value is
// harmless (makefileHarmlessFlagsValue).
func (b *makefileBindings) add(name string, harmless bool) {
	if name == "" {
		return
	}
	split := makefileSplitName(name)
	b.steps += len(name)
	if split.computed {
		b.computed = append(b.computed, split)
		b.runs += len(split.runs)
	} else {
		b.count[name]++
	}
	if split.mayBe(makefileFlagsVariable) && (name != makefileFlagsVariable || !harmless) {
		b.flags = true
	}
}

// fixed reports whether the file binds name exactly once, under a plain name, and no computed
// binding name may be it. Each computed name was split when it was recorded, so the test compares
// name with literal runs only, and counts each run it may compare in steps.
func (b *makefileBindings) fixed(name string) bool {
	if b.count[name] != 1 || !makefilePlainName(name) {
		return false
	}
	return !slices.ContainsFunc(b.computed, func(computed makefileName) bool {
		b.steps += len(computed.runs)
		return computed.mayBe(name)
	})
}

// makefileHarmlessFlagsValue reports whether value, bound with operator, holds only
// makefileHarmlessFlags words up to a comment: no reference, which may expand to a variable
// definition (measured: "A := X=docs-lint" and "MAKEFLAGS += $(A)" define X), no escape and no
// command, whose output the reader does not know.
func makefileHarmlessFlagsValue(operator, value string) bool {
	text, _, _ := strings.Cut(value, "#")
	if operator == "!=" || strings.ContainsAny(text, "$\\") {
		return false
	}
	for _, word := range strings.Fields(text) {
		if !slices.Contains(makefileHarmlessFlags, word) {
			return false
		}
	}
	return true
}

// makefilePlainName reports whether name is a variable name the reader may fix: letters, digits,
// "_" and "-", and not MAKEFLAGS, whose value Make rewrites from the options it parses (measured
// against GNU Make 4.4.1, "MAKEFLAGS := foo" leaves no "foo" in $(MAKEFLAGS)).
func makefilePlainName(name string) bool {
	if name == "" || name == makefileFlagsVariable || len(name) > MaxMakefileLineBytes {
		return false
	}
	for i := 0; i < len(name) && i < MaxMakefileLineBytes; i++ {
		if !makefileNameByte(name[i]) {
			return false
		}
	}
	return true
}

// makefileNameByte reports whether c may appear in a plain variable name: an ASCII letter or
// digit, "_" or "-".
func makefileNameByte(c byte) bool {
	return 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '_' || c == '-'
}

// makefileWordByte reports whether c may appear in a fixed value outside the blanks: a name byte
// (makefileNameByte) or one of "./+@,". None of them is a pattern, glob, tilde, archive, comment,
// escape, reference or rule character to Make, so a target name made of them is the file name Make
// declares.
func makefileWordByte(c byte) bool {
	return makefileNameByte(c) || strings.IndexByte("./+@,", c) >= 0
}

// makefileValueByte reports whether c may appear in a fixed value: a word byte (makefileWordByte)
// or a blank, which separates target names once Make expands the value.
func makefileValueByte(c byte) bool {
	return c == ' ' || c == '\t' || makefileWordByte(c)
}

// bind returns the value binding gives its variable: the text after the operator up to a comment,
// leading blanks dropped and trailing blanks kept, as Make keeps them. ":=" and "::=" expand the
// references in it at once; "=" defers them, so only a value without references is fixed. Any
// other operator fixes nothing.
func (v makefileVariables) bind(binding makefileBinding) (makefileValue, bool) {
	text, _, _ := strings.Cut(binding.value, "#")
	text = strings.TrimLeft(text, " \t")
	switch binding.operator {
	case ":=", "::=":
	case "=":
		if strings.Contains(text, "$") {
			return makefileValue{}, false
		}
	default:
		return makefileValue{}, false
	}
	expanded, depth, ok := v.expand(text, binding.line)
	if !ok || depth >= MaxMakefileVariableDepth {
		return makefileValue{}, false
	}
	return makefileValue{text: expanded, line: binding.line, depth: depth + 1}, true
}

// expand returns text with every reference replaced by the value it names as of the logical line
// at index line, and how many bindings deep the deepest reference goes. It fails on a byte
// makefileValueByte rejects, on a reference the reader fixes no value for before that line
// (lookup), and on a text or result longer than MaxMakefileLineBytes.
func (v makefileVariables) expand(text string, line int) (string, int, bool) {
	if len(text) > MaxMakefileLineBytes {
		return "", 0, false
	}
	var expanded strings.Builder
	depth := 0
	for i := 0; i < len(text) && expanded.Len() <= MaxMakefileLineBytes; {
		if text[i] != '$' {
			if !makefileValueByte(text[i]) {
				return "", 0, false
			}
			expanded.WriteByte(text[i])
			i++
			continue
		}
		width := makefileReferenceWidth(text[i:])
		value, ok := v.lookup(text[i:i+width], line)
		if !ok {
			return "", 0, false
		}
		expanded.WriteString(value.text)
		depth = max(depth, value.depth)
		i += width
	}
	if expanded.Len() > MaxMakefileLineBytes {
		return "", 0, false
	}
	return expanded.String(), depth, true
}

// lookup returns the value reference names, "$(NAME)" or "${NAME}" closed by its own bracket, when
// the reader fixed one on a line before line. A single-letter "$X", an escaped "$$", a substitution
// reference and a function call name no fixed variable.
func (v makefileVariables) lookup(reference string, line int) (makefileValue, bool) {
	if len(reference) < 4 || reference[len(reference)-1] != makefileCloser(reference[1]) {
		return makefileValue{}, false
	}
	value, ok := v.values[reference[2:len(reference)-1]]
	return value, ok && value.line < line
}

// targets returns the target names a rule on the logical line at index line declares, names as
// makefileRule returns them: a literal name as it is, and a computed one expanded (expand) and
// split at its blanks, as Make splits it. It reports false when a computed name does not resolve:
// only Make can say what the line declares.
func (v makefileVariables) targets(names []string, line int) ([]string, bool) {
	resolved := make([]string, 0, len(names))
	for _, name := range names {
		if !strings.Contains(name, "$") {
			resolved = append(resolved, name)
			continue
		}
		expanded, _, ok := v.expand(name, line)
		if !ok {
			return nil, false
		}
		resolved = append(resolved, strings.Fields(expanded)...)
	}
	return resolved, true
}
