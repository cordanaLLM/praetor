package util

import "strings"

// makefileConditionalWords are the first words of a conditional directive. A conditional neither
// opens nor closes a rule's recipe, but a line between it and its else or endif counts only when
// Make takes that branch. Measured against GNU Make 4.4.1: after "all:" and "ifdef MAKE", a
// tab-indented "-include rules.mk" before "endif" is a recipe line of all; after "ifdef UNSET",
// "foo:" and "endif", the same tab-indented line is an include directive again, because the rule
// sat in a branch Make skipped (makefileScanner).
var makefileConditionalWords = map[string]bool{
	"ifeq": true, "ifneq": true, "ifdef": true, "ifndef": true, "else": true, "endif": true,
}

// What a logical line is to Make, as makefileScanner.next reads it.
const (
	makefileSyntaxLine = iota // a line Make parses as makefile syntax
	makefileRecipeLine        // a recipe line of the rule above it
	makefileDefineLine        // the opening line, a body line or the closing endef of a define
	makefileUnsureLine        // a tab-prefixed line that is either, depending on a branch Make takes
)

// What the reader knows about a rule's recipe at a line, assuming Make takes every branch the line
// sits in. makefileRecipeNone marks a conditional none of whose branches has ended yet.
const (
	makefileRecipeNone   = iota - 1
	makefileRecipeClosed // no recipe is open: a tab-prefixed line is makefile syntax
	makefileRecipeOpen   // a rule's recipe is open: a tab-prefixed line is a recipe line
	makefileRecipeUnsure // the answer depends on a branch Make takes
)

// makefileScanner follows the states GNU Make 4.4.1 reads a line in: define ... endef nesting,
// whether a rule's recipe is open, and the conditionals around the line. A tab-prefixed line is a
// recipe line only while a recipe is open, that is after a rule line with nothing but blank lines,
// comments, conditionals and recipe lines since. Before the first rule, or after an assignment, a
// define, an include, an export or vpath directive or a bare expansion, Make parses a tab-prefixed
// line as makefile syntax: measured, a tab-indented "include rules.mk", "X := $(eval docs-lint: ;
// @echo x)" or "define X" there does what it does unindented, and a tab-indented rule or bare
// expansion stops Make with "recipe commences before first target".
//
// The reader evaluates no condition, so it reads every branch as taken and merges the branches at
// endif: a rule line or an assignment inside a branch opens or closes the recipe only in the branch
// Make takes. When the branches disagree, or one of them may be skipped, the recipe state after
// endif is unsure, and a tab-prefixed line there is an unsure line (makefileLeavesOwnershipToMake).
//
// At top level a define may carry the override, export, unexport or private modifiers, alone or
// combined. Inside a body only a line that does not open with a tab and whose first word is
// exactly "define" or "endef" changes the depth: a tab-indented endef stays body text, and an
// "override define" nested in a body opens nothing, so the first endef closes the outer block.
type makefileScanner struct {
	depth    int
	recipe   int
	branches []makefileBranch
	// scope is the recipe state when the outermost open conditional opened, and mixed whether the
	// recipe state differed from it anywhere inside that conditional since.
	scope int
	mixed bool
	// lost reports that the reader met a line it cannot resolve: nothing at or after it counts.
	lost bool
}

// makefileBranch is one open conditional: the recipe state before it (entry), the merged state at
// the end of the branches an else already ended (ended, makefileRecipeNone when none has), and
// whether one of those else lines was a plain else, after which no case is left untaken.
type makefileBranch struct {
	entry, ended int
	plainElse    bool
}

// next reports what line is to Make and advances the define nesting, the recipe state and the
// conditionals. A line Make may read as a binding of .RECIPEPREFIX is a point the reader cannot
// resolve (makefileMayBindRecipePrefix): which lines after it are recipe lines depends on the
// value.
func (s *makefileScanner) next(line string) int {
	if s.depth > 0 {
		s.body(line)
		return makefileDefineLine
	}
	fields := strings.Fields(line)
	kind := makefileSyntaxLine
	if strings.HasPrefix(line, "\t") {
		kind = s.tabLine(line, fields)
	}
	if kind != makefileRecipeLine && makefileMayBindRecipePrefix(line) {
		s.lost = true
	}
	if kind != makefileSyntaxLine {
		return kind
	}
	if makefileOpensDefine(fields) {
		s.depth = 1
		s.set(makefileRecipeClosed)
		return makefileDefineLine
	}
	s.syntax(line, fields)
	return makefileSyntaxLine
}

// tabLine classifies a tab-prefixed line: a recipe line while a recipe is open, makefile syntax
// while none is, and an unsure line while that depends on a branch Make takes. A line that opens a
// define or is a conditional changes how Make reads every line after it, so read either way it is
// a point the reader cannot resolve (lost) when the recipe state is unsure, or inside a conditional
// whose branches leave different states: Make reads such a line in a skipped branch too, in the
// state the branches it took left. Measured against GNU Make 4.4.1, a tab-indented "define X" after
// "ifeq (a,b)", "X = 1" and "endif" is a recipe line of the rule above, so a rule after it counts.
func (s *makefileScanner) tabLine(line string, fields []string) int {
	structural := makefileOpensDefine(fields) || makefileConditionalLine(line, fields)
	switch {
	case structural && (s.recipe == makefileRecipeUnsure || s.mixed):
		s.lost = true
		return makefileUnsureLine
	case s.recipe == makefileRecipeOpen:
		return makefileRecipeLine
	case s.recipe == makefileRecipeUnsure:
		return makefileUnsureLine
	}
	return makefileSyntaxLine
}

// body advances the nesting over one line of a define body. A define inside a conditional is a
// point the reader cannot resolve once its body holds a line that ends or nests it differently in
// a branch Make skips (makefileSkippedDefineDiverges).
func (s *makefileScanner) body(line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	tab := strings.HasPrefix(line, "\t")
	if len(s.branches) > 0 && makefileSkippedDefineDiverges(tab, fields[0]) {
		s.lost = true
	}
	if tab {
		return
	}
	switch fields[0] {
	case "define":
		s.depth++
	case "endef":
		s.depth--
	}
}

// syntax advances the recipe state over line, a makefile syntax line whose words are fields. A
// blank line and a comment keep the state, a conditional opens, switches or closes a branch, a rule
// line opens a recipe, and any other line closes it, measured against GNU Make 4.4.1 for an
// assignment, a target-specific variable, define, undefine, include, export, unexport, vpath and
// a bare expansion. Make tests for an assignment first, so "ifdef = 1" closes the recipe.
func (s *makefileScanner) syntax(line string, fields []string) {
	switch {
	case len(fields) == 0 || strings.HasPrefix(fields[0], "#"):
	case makefileBindsVariable(line):
		s.set(makefileRecipeClosed)
	case makefileConditionalWords[fields[0]]:
		s.conditional(fields)
	case makefileTargetNames(line) != nil:
		s.set(makefileRecipeOpen)
	default:
		s.set(makefileRecipeClosed)
	}
}

// conditional advances the open conditionals over a conditional directive whose words are fields.
// "else ifeq (...)" switches to a branch with a condition of its own; a plain else, or one followed
// only by a comment, leaves no case untaken.
func (s *makefileScanner) conditional(fields []string) {
	switch fields[0] {
	case "else":
		s.switchBranch(len(fields) == 1 || strings.HasPrefix(fields[1], "#"))
	case "endif":
		s.closeBranch()
	default:
		s.openBranch()
	}
}

// openBranch opens a conditional in the current recipe state.
func (s *makefileScanner) openBranch() {
	if len(s.branches) == 0 {
		s.scope, s.mixed = s.recipe, s.recipe == makefileRecipeUnsure
	}
	s.branches = append(s.branches, makefileBranch{entry: s.recipe, ended: makefileRecipeNone})
}

// switchBranch ends the current branch at an else: the next branch starts in the state before the
// conditional, since Make takes it only when it skipped the branches before. An else outside any
// conditional, which Make rejects, changes nothing.
func (s *makefileScanner) switchBranch(plain bool) {
	if len(s.branches) == 0 {
		return
	}
	top := &s.branches[len(s.branches)-1]
	top.ended = makefileMergeRecipe(top.ended, s.recipe)
	top.plainElse = top.plainElse || plain
	s.recipe = top.entry
}

// closeBranch ends a conditional at endif: the state after it is the state at the end of every
// branch merged, the state before it included unless a plain else left no case untaken. An endif
// outside any conditional, which Make rejects, changes nothing.
func (s *makefileScanner) closeBranch() {
	if len(s.branches) == 0 {
		return
	}
	top := s.branches[len(s.branches)-1]
	s.branches = s.branches[:len(s.branches)-1]
	state := makefileMergeRecipe(top.ended, s.recipe)
	if !top.plainElse {
		state = makefileMergeRecipe(state, top.entry)
	}
	s.set(state)
	if len(s.branches) == 0 {
		s.mixed = false
	}
}

// set records the recipe state after a line, and inside a conditional whether it left the state
// the outermost conditional opened in.
func (s *makefileScanner) set(state int) {
	if len(s.branches) > 0 && state != s.scope {
		s.mixed = true
	}
	s.recipe = state
}

// makefileSkippedDefineDiverges reports whether a define body line, tab-prefixed or not, whose
// first word is word, ends or nests the define differently when the define sits in a branch Make
// skips. Make then skips the body up to the first line whose first word is endef, so a nested
// "define" counts for nothing, and a tab-indented endef closes the define when no recipe is open.
// Measured against GNU Make 4.4.1: in a skipped "ifeq (a,b)" branch, "define outer" followed by
// "define inner" or by a tab-indented "endef" ends at that endef, so a docs-lint rule after the
// branch's endif declares docs-lint; in a taken branch the same rule is body text.
func makefileSkippedDefineDiverges(tab bool, word string) bool {
	return !tab && word == "define" || tab && word == "endef"
}

// makefileMergeRecipe returns the recipe state Make is in after one of two branches: the shared
// state when both agree, and makefileRecipeUnsure when they do not. makefileRecipeNone merges as
// no branch.
func makefileMergeRecipe(ended, state int) int {
	switch ended {
	case makefileRecipeNone, state:
		return state
	}
	return makefileRecipeUnsure
}

// makefileConditionalLine reports whether line, whose words are fields, is a conditional directive:
// its first word is one of makefileConditionalWords and it binds no variable, since Make tests for
// an assignment first.
func makefileConditionalLine(line string, fields []string) bool {
	return len(fields) > 0 && makefileConditionalWords[fields[0]] && !makefileBindsVariable(line)
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
