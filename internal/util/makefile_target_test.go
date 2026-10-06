package util_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// makefileTargetRow is one Makefile and whether it declares a verify-all rule.
type makefileTargetRow struct {
	makefile string
	want     bool
}

// makefileIssue304Rows are the forms issue #304 lists, each measured against GNU Make 4.4.1 with
// a Makefile holding only that line. The eight assignments answer "make verify-all" with "No rule
// to make target 'verify-all'"; a line test that cut at the first colon read every one as a rule.
// The four rules are the forms the issue names as the ones a shared reader must keep reading.
var makefileIssue304Rows = map[string]makefileTargetRow{
	"posix-assignment":          {"verify-all ::= x\n", false},
	"escaped-assignment":        {"verify-all :::= x\n", false},
	"recursive-value-colon":     {"verify-all = docker run --rm ci:latest check\n", false},
	"conditional-value-colon":   {"verify-all ?= a:b\n", false},
	"appending-value-colon":     {"verify-all += x:y\n", false},
	"shell-value-colon":         {"verify-all != date +%H:%M\n", false},
	"target-specific-variable":  {"verify-all: CFLAGS := -g\n", false},
	"value-names-target":        {"HELP = verify-all: run every gate\n", false},
	"rule":                      {"verify-all:\n\t@echo custom\n", true},
	"double-colon-rule":         {"verify-all:: dep\n\t@echo custom\n", true},
	"target-list":               {"all verify-all: dep\n\t@echo custom\n", true},
	"substitution-prerequisite": {"verify-all: $(SRCS:.c=.o)\n\t@echo custom\n", true},
}

// makefileTargetRows are the further forms the reader decides. A Makefile line is a rule only
// when a colon reaches the parser before any assignment operator, within the text Make still
// reads: it cuts the line at the first unescaped "#" or ";" first, so an "=" a comment or an
// inline recipe carries decides nothing. Make itself draws that line, and every row but the two
// CRLF ones was measured against GNU Make 4.4.1 with a Makefile holding only that line. A
// negative answers "make verify-all" with "No rule to make target 'verify-all'" and exits 2 --
// except comment-before-colon and semicolon-before-colon, which Make rejects outright with
// "missing separator"; neither declares a target, so no caller may declare "make verify-all"
// against any of them. The CRLF rows hold what a Windows checkout hands a caller that reads the
// file without normalizing it.
var makefileTargetRows = map[string]makefileTargetRow{
	"rule-with-deps":            {"verify-all: build test\n\t@echo custom\n", true},
	"windows-path-dep":          {"verify-all: C:\\deps\\stamp\n\t@echo custom\n", true},
	"nested-ref-prerequisite":   {"verify-all: $(filter-out $(X),a=b)\n\t@echo custom\n", true},
	"nested-if-prerequisite":    {"verify-all: $(if $(X),a,b=c)\n\t@echo custom\n", true},
	"target-variable-then-rule": {"verify-all: CFLAGS := -g\nverify-all:\n\t@echo custom\n", true},
	"help-comment-assignment":   {"verify-all: lint ## run gates (FAST=1)\n\t@echo custom\n", true},
	"trailing-comment-equals":   {"verify-all: dep # set X=1\n\t@echo custom\n", true},
	"inline-recipe-assignment":  {"verify-all: ; FOO=1 echo c\n", true},
	"double-colon-help-comment": {"verify-all:: dep ## run gates (X=1)\n\t@echo custom\n", true},
	"escaped-hash-prerequisite": {"verify-all: dep\\#1\n\t@echo custom\n", true},
	"override-prefixed-rule":    {"override verify-all: dep\n\t@echo custom\n", true},
	"private-prefixed-rule":     {"private verify-all: dep\n\t@echo custom\n", true},
	"export-second-word-rule":   {"override export verify-all: dep\n\t@echo custom\n", true},
	"export-prefix-word-rule":   {"exportx verify-all: dep\n\t@echo custom\n", true},
	"crlf-rule":                 {"all:\r\n\t@echo a\r\nverify-all:\r\n\t@echo custom\r\n", true},
	"simple-assignment":         {"verify-all := x\n", false},
	"unspaced-assignment":       {"verify-all:=x\n", false},
	"exported-assignment":       {"export verify-all := x\n", false},
	"override-assignment":       {"override verify-all := x\n", false},
	"crlf-assignment":           {"verify-all := x\r\nall:\r\n\t@echo a\r\n", false},
	"export-directive":          {"export verify-all: dep\n", false},
	"unexport-directive":        {"unexport verify-all: dep\n", false},
	"indented-export-directive": {"  export\tverify-all:: dep\n", false},
	"substitution-value":        {"verify-all = $(SRCS:.c=.o)\n", false},
	"target-variable-commented": {"verify-all: CFLAGS := -g # note\n", false},
	"target-variable-recipe":    {"verify-all: CFLAGS := -g ; echo hi\n", false},
	"comment-before-colon":      {"verify-all # : dep\n", false},
	"semicolon-before-colon":    {"verify-all ; x: dep\n", false},
	"assignment-then-comment":   {"verify-all = x # a:b\n", false},
	"recipe-line":               {"other:\n\tverify-all: not a rule\n", false},
	"comment":                   {"# verify-all: old proposal\n", false},
	"windows-path-target":       {"C:\\out\\verify-all: dep\n\t@echo custom\n", false},
	"empty":                     {"", false},
	"other-rule":                {"build:\n\t@echo custom\n", false},
}

func assertMakefileTargetRows(t *testing.T, rows map[string]makefileTargetRow) {
	t.Helper()
	for name, tc := range rows {
		t.Run(name, func(t *testing.T) {
			if got := util.MakefileHasTarget(tc.makefile, "verify-all"); got != tc.want {
				t.Fatalf("MakefileHasTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// The shared table: adoption and editor generation both read a Makefile through MakefileHasTarget,
// so one row decides what both of them answer for a form (issue #304).
func TestMakefileHasTargetIssue304Forms(t *testing.T) {
	if len(makefileIssue304Rows) != 12 {
		t.Fatalf("issue #304 lists eight assignments and four rules, the table holds %d rows", len(makefileIssue304Rows))
	}
	assertMakefileTargetRows(t, makefileIssue304Rows)
}

// clearMakeEnvironment empties the variables the rows reference, since Make imports the
// environment as variables and a caller's SRCS, PREFIX or MAKEFLAGS would change what they expand to.
func clearMakeEnvironment(t *testing.T) {
	t.Helper()
	for _, name := range []string{"A", "B", "N", "R", "T", "V", "X", "GATE", "NAME", "VAR", "PREFIX", "SRCS", "CFLAGS", "HELP", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
}

// makeDeclaresVerifyAll writes makefile with a "dep" rule appended, so a rule row fails for no
// other reason than a missing verify-all, rules.txt holding a verify-all rule and eval.txt an eval
// call declaring one, for the rows that read them, and reports whether "make -n verify-all" prints
// the row's recipe.
func makeDeclaresVerifyAll(t *testing.T, makePath, makefile string) (bool, string) {
	t.Helper()
	root := t.TempDir()
	for name, text := range map[string]string{
		"Makefile":  makefile + "dep: ;\n",
		"rules.txt": "verify-all: ; @echo custom\n",
		"eval.txt":  "$(eval verify-all: ; @echo custom)\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := util.RunCommand(t.Context(), root, makePath, "--no-print-directory", "-n", "verify-all")
	return err == nil && strings.Contains(out, "echo custom"), fmt.Sprintf("%q %v", out, err)
}

// Replayed against the installed GNU Make, both directions: "make -n verify-all" fails for the
// eight assignments, which declare no rule, and prints the recipe for the four rules.
func TestMakefileIssue304FormsGNUReplay(t *testing.T) {
	makePath := testsupport.GNUMake(t)
	clearMakeEnvironment(t)
	for name, tc := range makefileIssue304Rows {
		t.Run(name, func(t *testing.T) {
			if declared, out := makeDeclaresVerifyAll(t, makePath, tc.makefile); declared != tc.want {
				t.Fatalf("GNU Make declares verify-all = %v, the reader's row says %v: %s", declared, tc.want, out)
			}
		})
	}
}

// makefileOwnershipRow is a Makefile, what the reader answers for verify-all (hasTarget for
// MakefileHasTarget, mayDefine for MakefileMayDefineTarget), what GNU Make 4.4.1 answers
// (makeTarget: "make -n verify-all" prints the recipe), and whether the replay runs a shell
// command (shell: sh with cat, see testsupport.RequireGNUMakeShell). The reader may refuse more
// than Make declares, never less: makeTarget implies mayDefine, and hasTarget implies makeTarget.
type makefileOwnershipRow struct {
	makefile                         string
	hasTarget, mayDefine, makeTarget bool
	shell                            bool
}

// makefileIssue554Rows are the shapes issue #554 reports and the ones review of its fix found. Each
// row's reader answer differs from the reader before the fix, which read physical lines and
// counted a bare expansion only when its own text held a colon: the bare expansions there were
// appendable, a continued assignment, recipe line or comment declared verify-all, a continued
// target name declared nothing, and a define whose continued line swallows endef closed there.
// info-message is the one bare expansion the reader exempts: a line made only of info, warning
// and error calls expands to nothing (makefileSilentRows). computed-target-list declares
// verify-all, but the reader claims no target from a line whose target list holds a reference.
var makefileIssue554Rows = map[string]makefileOwnershipRow{
	"single-line-template": {
		makefile:  "make-rule = $(1): ; @echo custom\n$(call make-rule,verify-all)\n",
		mayDefine: true, makeTarget: true,
	},
	"bare-expansion-continued": {
		makefile:  "X = 1\n$(if $(X),verify-all \\\n  : ; @echo custom)\n",
		mayDefine: true, makeTarget: true,
	},
	"concatenated-values": {
		makefile:  "A = verify-all\nB = : ; @echo custom\n$(A)$(B)\n",
		mayDefine: true, makeTarget: true,
	},
	"shell-output": {
		makefile:  "$(shell cat rules.txt)\n",
		mayDefine: true, makeTarget: true, shell: true,
	},
	"info-message": {
		makefile: "$(info verify-all: ; @echo custom)\nall:\n\t@echo all\n",
	},
	"computed-target-list": {
		makefile:  "$(PREFIX) verify-all: dep\n\t@echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"continued-assignment": {
		makefile: "HELP = usage \\\n  verify-all: run every gate\nall:\n\t@echo all\n",
	},
	"continued-recipe": {
		makefile: "other:\n\t@echo step 1 \\\n  verify-all: not a rule\n",
	},
	"continued-comment": {
		makefile: "# old: \\\nverify-all: dep\n\t@echo custom\n",
	},
	"continued-target-name": {
		makefile:  "verify-all \\\n  other: dep\n\t@echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
	"define-swallows-endef": {
		makefile:  "define gates\nbody \\\nendef\nverify-all: dep\n\t@echo custom\n",
		mayDefine: true,
	},
}

// makefileSilentRows hold the one bare expansion the reader exempts: a line made only of info,
// warning and error calls, which expands to nothing whatever the calls print. A nested plain
// reference stays exempt (silent-call-prints-rule); a nested eval, call, guile or shell call, text
// outside the calls and an $(if ...) around them do not. silent-call-runs-shell declares nothing in
// Make and is refused all the same. The two variable rows show why a nested reference is safe: a
// value that evaluates text holds a call the reader reports on the assignment line itself.
var makefileSilentRows = map[string]makefileOwnershipRow{
	"silent-calls":            {makefile: "$(info a) $(warning b)\n${info c}\nall:\n\t@echo all\n"},
	"silent-call-prints-rule": {makefile: "R = verify-all: ; @echo custom\n$(info $(R))\nall:\n\t@echo all\n"},
	"silent-call-then-reference": {
		makefile:  "R = verify-all: ; @echo custom\n$(warning x) $(R)\n",
		mayDefine: true, makeTarget: true,
	},
	"silent-call-inside-if": {
		makefile:  "V = verify-all: ; @echo custom\n$(if $(V),$(V),$(error x))\n",
		mayDefine: true, makeTarget: true,
	},
	"silent-call-nested-eval": {
		makefile:  "$(info $(eval verify-all: ; @echo custom))\n",
		mayDefine: true, makeTarget: true,
	},
	"silent-call-nested-call-eval": {
		makefile:  "$(info $(call eval,verify-all: ; @echo custom))\n",
		mayDefine: true, makeTarget: true,
	},
	"silent-call-runs-shell": {
		makefile:  "$(info $(shell cat rules.txt))\nall:\n\t@echo all\n",
		mayDefine: true, shell: true,
	},
	"variable-calls-eval": {
		makefile:  "X = $(call eval,verify-all: ; @echo custom)\n$(info $(X))\n",
		mayDefine: true, makeTarget: true,
	},
	"variable-calls-computed-name": {
		makefile:  "T = eval\nX = $(call $(T),verify-all: ; @echo custom)\n$(info $(X))\n",
		mayDefine: true, makeTarget: true,
	},
}

// makefileCallRows hold the $(call ...) forms that invoke a built-in function which parses text as
// makefile syntax: a first argument partly computed ("e$(S)" with "S = val"), padded with blanks,
// or naming call itself, which passes the rest on to eval. call-of-user-function is the negative: a
// name whose literal part ("build_") no such function holds calls the user's variable.
var makefileCallRows = map[string]makefileOwnershipRow{
	"call-partial-name": {
		makefile:  "S = val\nX := $(call e$(S),verify-all: ; @echo custom)\n",
		mayDefine: true, makeTarget: true,
	},
	"call-spaced-name": {
		makefile:  "X := $(call  eval ,verify-all: ; @echo custom)\n",
		mayDefine: true, makeTarget: true,
	},
	"call-of-call": {
		makefile:  "X := $(call call,eval,verify-all: ; @echo custom)\n",
		mayDefine: true, makeTarget: true,
	},
	"call-of-user-function": {
		makefile: "build_x = $(1)\nARCH = x\nX := $(call build_$(ARCH),verify-all: ; @echo custom)\nall: ; @echo all\n",
	},
}

// makefileCommandOutputRows hold "!=" bindings. Make runs the command and stores its output as the
// value of a recursively expanded variable, so it expands the output as makefile text wherever the
// variable is expanded: an eval call that eval.txt holds declares verify-all from a prerequisite
// list, an info call that otherwise expands to nothing, or an immediate assignment. The reader runs
// no command, so every "!=" binding, a "define X !=" included, leaves ownership to Make.
var makefileCommandOutputRows = map[string]makefileOwnershipRow{
	"command-output-prerequisite": {
		makefile:  "X != cat eval.txt\nall: $(X)\n\t@echo all\n",
		mayDefine: true, makeTarget: true, shell: true,
	},
	"command-output-silent-call": {
		makefile:  "X != cat eval.txt\n$(info $(X))\n",
		mayDefine: true, makeTarget: true, shell: true,
	},
	"command-output-define": {
		makefile:  "define X !=\ncat eval.txt\nendef\nY := $(X)\n",
		mayDefine: true, makeTarget: true, shell: true,
	},
}

// makefileNameRows hold the two ways Make reads a line the reader used to read as an assignment
// or as an escape. Make has no backslash escape for "$", so "\$(R)" expands R; and Make takes no
// blank inside a variable name, so an assignment operator behind two words (after the modifiers)
// binds nothing: the line is a rule or a bare expansion, except after export, which stays the
// export directive. In a rule's prerequisites such an operator leaves the line to Make, which reads
// it as prerequisites or stops with "multiple target patterns".
var makefileNameRows = map[string]makefileOwnershipRow{
	"escaped-dollar-reference": {
		makefile:  "R = x verify-all: ; @echo custom\n\\$(R)\n",
		mayDefine: true, makeTarget: true,
	},
	"escaped-backslash-reference": {
		makefile:  "R = x verify-all: ; @echo custom\n\\\\$(R)\n",
		mayDefine: true, makeTarget: true,
	},
	"two-word-name-reference": {
		makefile:  "R = verify-all: ; @echo custom\n$(R) x = y\n",
		mayDefine: true, makeTarget: true,
	},
	"two-word-name": {
		makefile:  "foo bar = verify-all: ; @echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
	"override-two-word-name": {
		makefile:  "override foo bar = verify-all: ; @echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
	"escaped-blank-name": {
		makefile:  "foo\\ bar = verify-all: ; @echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
	"export-two-word-name":           {makefile: "export foo bar = verify-all: ; @echo custom\n"},
	"two-word-prerequisite-assign":   {makefile: "verify-all: A B = x ; @echo custom\n", mayDefine: true},
	"two-word-prerequisite-simple":   {makefile: "verify-all: A B := x\n", mayDefine: true},
	"one-word-prerequisite-variable": {makefile: "verify-all: export CFLAGS = -g\nall:\n\t@echo all\n"},
}

// makefileRecipeRows hold when a tab-prefixed line is a recipe line: only while a rule's recipe
// is open. Before the first rule, or after an assignment or any other line but a blank line, a
// comment or a conditional, Make parses it as makefile syntax, so a tab-indented include or eval
// declares the target and a tab-indented define opens a block. tab-assignment-runs-shell is such a
// line too: an assignment, whose shell output Make never parses as rules. A Makefile that names
// .RECIPEPREFIX is one only Make can read: the reader claims no rule from it.
var makefileRecipeRows = map[string]makefileOwnershipRow{
	"tab-include-in-conditional": {
		makefile:  "ifneq ($(wildcard rules.txt),)\n\tinclude rules.txt\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"tab-eval-in-conditional": {
		makefile:  "ifdef MAKE\n\tX := $(eval verify-all: ; @echo custom)\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"tab-include-after-assignment": {
		makefile:  "all:\n\t@echo all\nX = 1\n\tinclude rules.txt\n",
		mayDefine: true, makeTarget: true,
	},
	"tab-include-after-silent-call": {
		makefile:  "all:\n\t@echo all\n$(info x)\n\t-include rules.txt\n",
		mayDefine: true, makeTarget: true,
	},
	"tab-include-in-recipe":  {makefile: "all:\nifdef MAKE\n\t-include rules.txt\nendif\n\n# note\n\t-include rules.txt\n"},
	"tab-define-before-rule": {makefile: "\tdefine X\nverify-all: ; @echo custom\nendef\nall: ; @echo all\n"},
	"tab-assignment-runs-shell": {
		makefile: "ifdef MAKE\n\tX := $(shell cat rules.txt)\nendif\nall: ; @echo all\n",
		shell:    true,
	},
	"recipe-prefix-tab-rule": {
		makefile:  ".RECIPEPREFIX = >\nall:\n> @echo all\n\tverify-all: ; @echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"recipe-prefix-recipe-line": {makefile: ".RECIPEPREFIX = >\nall:\n> verify-all: dep\n", mayDefine: true},
	"recipe-prefix-computed-name": {
		makefile:  "P = .RECIPE\n$(P)PREFIX := >\nall:\n> @echo all\n\tverify-all: ; @echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"recipe-prefix-computed-whole-name": {
		makefile:  "N = $(addprefix .,RECIPEPREFIX)\n$(N) := >\nall:\n> @echo all\n\tverify-all: ; @echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"computed-name-not-recipe-prefix": {makefile: "PKG = app\n$(PKG)_SRCS := a.c\nall:\n\t@echo all\n\t-include rules.txt\n"},
}

// makefileBranchRows hold the recipe state across conditionals: a line between a conditional and
// its else or endif counts only when Make takes that branch. A rule in a skipped branch opens no
// recipe, so a tab-indented include or eval after the branch ends is makefile syntax again
// (skipped-rule-then-include, skipped-rule-then-eval); an assignment in a skipped branch closes
// none, so a tab-indented define after it is a recipe line and the rule below counts
// (skipped-assignment-then-define). Read either way, a tab-indented define or conditional decides
// how Make reads every later line, so where the recipe state depends on a branch the reader stops
// there: it claims nothing at or after the line and leaves the file to Make, whether Make then
// declares the target (taken-rule-then-define, else-define-after-rule, unsure-tab-endif) or
// swallows it (skipped-define-swallows-rule). A define in a skipped branch ends at its first
// endef, a tab-indented one included, while a taken one counts nested defines and keeps a
// tab-indented endef as body text, so such a body line inside a conditional stops the reader too
// (skipped-define-nests, skipped-define-tab-endef). The other rows stay readable: a recipe open in
// every branch keeps a tab-indented include a recipe line (rule-in-both-branches), an in-recipe
// eval after a conditional stays recipe text (recipe-eval-after-conditional,
// recipe-eval-in-conditional), a tab-indented assignment where the state is unsure binds a
// variable or is recipe text, neither of which declares a rule (unsure-tab-assignment), and
// tab-indented conditionals where no recipe can be open are directives (nested-tab-conditionals).
var makefileBranchRows = map[string]makefileOwnershipRow{
	"skipped-rule-then-include": {
		makefile:  "CC = gcc\nifeq ($(wildcard rules.txt),)\nconfig:\n\t./configure\nelse\n\tinclude rules.txt\nendif\nall: ; @echo all\n",
		mayDefine: true, makeTarget: true,
	},
	"skipped-rule-then-eval": {
		makefile:  "ifeq (a,b)\nfoo:\nendif\n\tX := $(eval verify-all: ; @echo custom)\n",
		mayDefine: true, makeTarget: true,
	},
	"skipped-assignment-then-define": {
		makefile:  "all:\n\t@echo all\nifeq (a,b)\nX = 1\nendif\n\tdefine X\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"taken-rule-then-define": {
		makefile:  "ifeq (x,x)\nall:\n\t@echo all\nelse\nX = 1\nendif\n\tdefine X\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"else-define-after-rule": {
		makefile:  "ifeq (x,x)\nfoo:\n\t@echo foo\nelse\n\tdefine X\nendif\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"skipped-define-swallows-rule": {
		makefile:  "ifeq (x,y)\nfoo:\n\tdefine X\nendif\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true,
	},
	"skipped-define-nests": {
		makefile:  "ifeq (a,b)\ndefine outer\ndefine inner\nendef\nendif\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"skipped-define-tab-endef": {
		makefile:  "ifeq (a,b)\ndefine outer\n\tendef\nendif\nverify-all: ; @echo custom\nifeq (a,b)\nendef\nendif\n",
		mayDefine: true, makeTarget: true,
	},
	"unsure-tab-endif": {
		makefile:  "ifeq (a,b)\nfoo:\nendif\nifeq (a,a)\n\tendif\nverify-all: ; @echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"unsure-tab-assignment": {
		makefile: "ifeq (a,b)\nfoo:\nendif\n\tCFLAGS = -O2\nall: ; @echo all\n",
	},
	"rule-in-both-branches": {
		makefile: "ifeq ($(X),y)\nall:\n\t@echo y\nelse\nall:\n\t@echo n\nendif\n\t-include rules.txt\n",
	},
	"recipe-eval-after-conditional": {
		makefile: "all:\nifeq ($(X),y)\n\t@echo y\nendif\n\t$(eval Y := 1)\n\t@echo all\n",
	},
	"recipe-eval-in-conditional": {
		makefile: "all:\nifeq ($(X),)\n\t$(eval verify-all: ; @echo custom)\nendif\n\t@echo all\n",
	},
	"nested-tab-conditionals": {
		makefile:  "X = 1\nifeq (a,a)\n\tifeq (b,b)\n\t\tY = 2\n\tendif\nendif\nverify-all: ; @echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
}

// continuedRule returns a rule for verify-all whose target list runs over continuations
// continuation lines, and continuedValue an assignment whose value does, with a rule-shaped last
// line: GNU Make declares verify-all for the first at any length and never for the second.
func continuedRule(continuations int) string {
	return "verify-all \\\n" + strings.Repeat("  step \\\n", continuations-1) + "  other: dep\n\t@echo custom\n"
}

func continuedValue(continuations int) string {
	return "HELP = usage \\\n" + strings.Repeat("  step \\\n", continuations-1) + "  verify-all: not a rule\nall:\n\t@echo all\n"
}

// joinedRule returns a rule for verify-all continued once whose joined logical line is exactly
// length bytes long.
func joinedRule(length int) string {
	const joined = "verify-all other: dep # "
	return "verify-all \\\n  other: dep # " + strings.Repeat("x", length-len(joined)) + "\n\t@echo custom\n"
}

// makefileBoundRows hold the continuation and joined-length bounds (HISS-02). At a bound the
// logical line is read whole; past it the reader stops: it claims nothing from that line on and
// leaves the file to Make, which still reads it.
var makefileBoundRows = map[string]makefileOwnershipRow{
	"rule-at-continuation-bound":    {makefile: continuedRule(util.MaxMakefileContinuations), hasTarget: true, mayDefine: true, makeTarget: true},
	"rule-past-continuation-bound":  {makefile: continuedRule(util.MaxMakefileContinuations + 1), mayDefine: true, makeTarget: true},
	"value-at-continuation-bound":   {makefile: continuedValue(util.MaxMakefileContinuations)},
	"value-past-continuation-bound": {makefile: continuedValue(util.MaxMakefileContinuations + 1), mayDefine: true},
	"rule-at-joined-byte-bound":     {makefile: joinedRule(util.MaxMakefileLineBytes), hasTarget: true, mayDefine: true, makeTarget: true},
	"rule-past-joined-byte-bound":   {makefile: joinedRule(util.MaxMakefileLineBytes + 1), mayDefine: true, makeTarget: true},
}

// chainTo returns a Makefile in which V1 is bound to value with ":=" and each of V2 to Vn to a
// reference to the one before, n bindings deep, and a rule names $(Vn).
func chainTo(n int, value string) string {
	var chain strings.Builder
	chain.WriteString("V1 := " + value + "\n")
	for i := 2; i <= n; i++ {
		fmt.Fprintf(&chain, "V%d := $(V%d)\n", i, i-1)
	}
	fmt.Fprintf(&chain, "$(V%d): dep\n\t@echo custom\nall: ; @echo all\n", n)
	return chain.String()
}

// makefileComputedRows hold computed target names (issue #537). The reader resolves a name when
// every variable in it is bound exactly once in the file with ":=", "::=" or a literal "=", at top
// level, before the rule, to plain path text and references resolved the same way, at most
// util.MaxMakefileVariableDepth bindings deep; the resolved name counts like a literal one. The
// literal-chains row is the five-variable shape the issue's comment reports, rebuilt
// (testsupport.MakefileLiteralChains). The trailing-blank rows hold the blank Make keeps at the end
// of a value, which splits a glued name into two targets. Every other shape leaves the name to
// Make as before, whether Make then declares verify-all or not: a second binding, one after the
// rule (Make takes the environment's value there), one inside a conditional, "?=", "+=",
// $(shell ...), a variable bound nowhere, a recursive reference, a modifier, a target-specific or
// computed-name binding, undefine, a MAKEFLAGS binding that defines a variable, a pattern or glob
// character, a single-letter or substitution reference, a chain past the depth bound, and an eval
// call or bare expansion anywhere in the file, which may rebind the variable (eval-rebinds and
// bare-expansion-rebinds declare build, so MakefileHasTarget must not claim verify-all there). A
// MAKEFLAGS binding of options that change no value keeps names resolvable (flags-harmless).
var makefileComputedRows = map[string]makefileOwnershipRow{
	"literal-chains":          {makefile: testsupport.MakefileLiteralChains},
	"chain-to-target":         {makefile: "GATE := verify\nNAME := $(GATE)-all\n$(NAME): dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"literal-recursive-value": {makefile: "NAME = verify-all\n$(NAME): dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"posix-brace-reference":   {makefile: "NAME ::= verify-all\n${NAME}: dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"value-in-target-list":    {makefile: "NAME := lint verify-all\n$(NAME) build: dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"crlf-chain":              {makefile: "GATE := verify\r\nNAME := $(GATE)-all\r\n$(NAME): dep\r\n\t@echo custom\r\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"trailing-blank":          {makefile: "NAME := verify-all \n$(NAME)x: dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"trailing-blank-comment":  {makefile: "NAME := verify-all # gate\n$(NAME)x: dep\n\t@echo custom\n", hasTarget: true, mayDefine: true, makeTarget: true},
	"trailing-blank-continued": {
		makefile:  "NAME := verify-all\\\n\n$(NAME)x: dep\n\t@echo custom\n",
		hasTarget: true, mayDefine: true, makeTarget: true,
	},
	"glued-value":          {makefile: "NAME := verify\n$(NAME)x-all: dep\n\t@echo custom\nall: ; @echo all\n"},
	"flags-harmless":       {makefile: "MAKEFLAGS += --no-print-directory -r\nNAME := build\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n"},
	"bound-twice":          {makefile: "NAME := build\nNAME := verify-all\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"bound-after-rule":     {makefile: "$(NAME): dep\n\t@echo custom\nNAME := verify-all\nall: ; @echo all\n", mayDefine: true},
	"bound-in-conditional": {makefile: "ifeq (a,a)\nNAME := verify-all\nendif\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	// The replay's environment defines NAME, empty (clearMakeEnvironment), so "?=" binds nothing:
	// the environment decides the name, which is why the reader leaves it to Make.
	"conditional-assignment": {makefile: "NAME ?= verify-all\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n", mayDefine: true},
	"appended-value":         {makefile: "NAME += verify-all\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"shell-value": {
		makefile:  "NAME := $(shell echo verify-all)\n$(NAME): dep\n\t@echo custom\n",
		mayDefine: true, makeTarget: true, shell: true,
	},
	"unbound-reference":       {makefile: "NAME := $(GATE)-all\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n", mayDefine: true},
	"recursive-reference":     {makefile: "GATE = verify\nNAME = $(GATE)-all\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"override-binding":        {makefile: "override NAME := verify-all\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"exported-binding":        {makefile: "export NAME := verify-all\n$(NAME): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"target-specific-binding": {makefile: "NAME := build\nall: NAME := verify-all\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n", mayDefine: true},
	"computed-binding-name": {
		makefile:  "VAR := NAME\nNAME := build\n$(VAR) := verify-all\n$(NAME): dep\n\t@echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"undefined-by-undefine": {makefile: "NAME := build\nundefine NAME\n$(NAME)verify-all: dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"flags-define-variable": {
		makefile:  "NAME := build\nMAKEFLAGS += NAME=verify-all\n$(NAME): dep\n\t@echo custom\n",
		mayDefine: true, makeTarget: true,
	},
	"pattern-in-name":         {makefile: "GATE := verify\n$(GATE)-%: dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"glob-in-value":           {makefile: "NAME := verify-a*\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n", mayDefine: true},
	"single-letter-reference": {makefile: "N := verify-all\n$N: dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"substitution-reference":  {makefile: "NAME := verify-x\n$(NAME:-x=-all): dep\n\t@echo custom\n", mayDefine: true, makeTarget: true},
	"chain-at-depth-bound":    {makefile: chainTo(util.MaxMakefileVariableDepth, "verify-all"), hasTarget: true, mayDefine: true, makeTarget: true},
	"chain-past-depth-bound":  {makefile: chainTo(util.MaxMakefileVariableDepth+1, "verify-all"), mayDefine: true, makeTarget: true},
	"other-at-depth-bound":    {makefile: chainTo(util.MaxMakefileVariableDepth, "build")},
	"other-past-depth-bound":  {makefile: chainTo(util.MaxMakefileVariableDepth+1, "build"), mayDefine: true},
	"eval-rebinds":            {makefile: "NAME := verify-all\n$(eval NAME := build)\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n", mayDefine: true},
	"bare-expansion-rebinds": {
		makefile:  "R = NAME := build\nNAME := verify-all\n$(R)\n$(NAME): dep\n\t@echo custom\nall: ; @echo all\n",
		mayDefine: true,
	},
}

func assertOwnershipRows(t *testing.T, rows map[string]makefileOwnershipRow) {
	t.Helper()
	for name, tc := range rows {
		t.Run(name, func(t *testing.T) {
			if tc.makeTarget && !tc.mayDefine || tc.hasTarget && !tc.makeTarget {
				t.Fatalf("the row fails open: hasTarget %v, mayDefine %v, makeTarget %v", tc.hasTarget, tc.mayDefine, tc.makeTarget)
			}
			if got := util.MakefileHasTarget(tc.makefile, "verify-all"); got != tc.hasTarget {
				t.Fatalf("MakefileHasTarget(%q) = %v, want %v", tc.makefile, got, tc.hasTarget)
			}
			if got := util.MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.mayDefine {
				t.Fatalf("MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.mayDefine)
			}
		})
	}
}

func TestMakefileIssue554Forms(t *testing.T) {
	assertOwnershipRows(t, makefileIssue554Rows)
}

func TestMakefileSilentCallsDeclareNothing(t *testing.T) {
	assertOwnershipRows(t, makefileSilentRows)
}

func TestMakefileNamesAndEscapesAsMakeReadsThem(t *testing.T) {
	assertOwnershipRows(t, makefileNameRows)
}

func TestMakefileTabLinesOutsideRecipes(t *testing.T) {
	assertOwnershipRows(t, makefileRecipeRows)
}

func TestMakefileContinuationAndJoinedByteBounds(t *testing.T) {
	assertOwnershipRows(t, makefileBoundRows)
}

func TestMakefileRecipeStateFollowsTakenBranch(t *testing.T) {
	assertOwnershipRows(t, makefileBranchRows)
}

func TestMakefileCallsOfEvalByName(t *testing.T) {
	assertOwnershipRows(t, makefileCallRows)
}

func TestMakefileCommandOutputLeavesOwnershipToMake(t *testing.T) {
	assertOwnershipRows(t, makefileCommandOutputRows)
}

func TestMakefileComputedTargetNamesTheFileFixes(t *testing.T) {
	assertOwnershipRows(t, makefileComputedRows)
}

// The issue #537 shape declares none of the targets the documentation gate and the verification
// block append, and the reader resolves each of its five computed names to the path Make declares:
// every name counts as a rule of its own, and a name the file does not declare counts as none.
func TestMakefileLiteralChainsResolveToTheirPaths(t *testing.T) {
	makefile := testsupport.MakefileLiteralChains
	for _, target := range []string{"docs-lint", "docs-figures", "verify-all"} {
		if util.MakefileHasTarget(makefile, target) || util.MakefileMayDefineTarget(makefile, target) {
			t.Fatalf("the literal chains were read as declaring %s", target)
		}
	}
	for _, path := range []string{".tools/bin/python", ".tools/bin/ruff", ".tools/bin/black", "engine/out", "engine/trace"} {
		if !util.MakefileHasTarget(makefile, path) {
			t.Fatalf("the computed name for %s was not resolved", path)
		}
	}
	if util.MakefileHasTarget(makefile, "$(OUT_DIR)") || util.MakefileHasTarget(makefile, "engine") {
		t.Fatal("an unexpanded name or a part of a value was read as a target")
	}
	if recipe, found := util.MakefileTargetRecipe(makefile, "engine/out"); !found || recipe != "\tmkdir -p $@\n" {
		t.Fatalf("MakefileTargetRecipe(engine/out) = %q, %v", recipe, found)
	}
}

// Negative: one more binding of a chain variable, anywhere in the file, leaves every name that
// references it to Make, so the gate targets are refused again, as before the resolver; a chain
// that does not reference it stays resolved. A MAKEFLAGS binding that may define a variable
// leaves every computed name to Make.
func TestMakefileLiteralChainsRebindingLeavesNamesToMake(t *testing.T) {
	for _, binding := range []string{"SRC_ROOT := docs-lint\n", "SRC_ROOT += x\n", "SRC_ROOT ?= x\n", "override SRC_ROOT := x\n", "all: SRC_ROOT := x\n", "undefine SRC_ROOT\n", "define SRC_ROOT\nx\nendef\n"} {
		makefile := testsupport.MakefileLiteralChains + binding
		if !util.MakefileMayDefineTarget(makefile, "docs-lint") || util.MakefileHasTarget(makefile, "engine/out") {
			t.Fatalf("%q after the chains left $(OUT_DIR) resolvable", binding)
		}
		if !util.MakefileHasTarget(makefile, ".tools/bin/ruff") {
			t.Fatalf("%q after the chains unresolved a chain that does not reference SRC_ROOT", binding)
		}
	}
	for _, prefix := range []string{
		"MAKEFLAGS += SRC_ROOT=x\n", "MAKEFLAGS += -e\n", "MAKEFLAGS = $(OPTIONS)\n", "$(M)FLAGS += -s\n",
		"-include extra.mk\n", "$(eval X := 1)\n", "define T\n$(eval X := 1)\nendef\n", "$(R)\n",
	} {
		makefile := prefix + testsupport.MakefileLiteralChains
		if !util.MakefileMayDefineTarget(makefile, "docs-lint") || util.MakefileHasTarget(makefile, ".tools/bin/ruff") {
			t.Fatalf("%q before the chains left a computed name resolvable", prefix)
		}
	}
	// Boundary: a line of silent calls parses no text, so the names stay resolved.
	if !util.MakefileHasTarget("$(info building)\n"+testsupport.MakefileLiteralChains, ".tools/bin/ruff") {
		t.Fatal("a silent $(info ...) line unresolved the computed names")
	}
}

// Replayed against the installed GNU Make: Make's answer must be the row's, and the reader's live
// answers must not fail open against it -- Make declaring verify-all implies MakefileMayDefineTarget,
// and MakefileHasTarget implies Make declaring it.
func TestMakefileOwnershipRowsGNUReplay(t *testing.T) {
	makePath := testsupport.GNUMake(t)
	clearMakeEnvironment(t)
	for _, rows := range []map[string]makefileOwnershipRow{
		makefileIssue554Rows, makefileBoundRows, makefileSilentRows, makefileNameRows, makefileRecipeRows,
		makefileBranchRows, makefileCallRows, makefileCommandOutputRows, makefileComputedRows,
	} {
		for name, tc := range rows {
			t.Run(name, func(t *testing.T) {
				if tc.shell {
					testsupport.RequireGNUMakeShell(t, "cat")
				}
				declared, out := makeDeclaresVerifyAll(t, makePath, tc.makefile)
				if declared != tc.makeTarget {
					t.Fatalf("GNU Make declares verify-all = %v, the row says %v: %s", declared, tc.makeTarget, out)
				}
				if declared && !util.MakefileMayDefineTarget(tc.makefile, "verify-all") {
					t.Fatalf("GNU Make declares verify-all, MakefileMayDefineTarget reports false: %s", out)
				}
				if util.MakefileHasTarget(tc.makefile, "verify-all") && !declared {
					t.Fatalf("MakefileHasTarget claims verify-all, GNU Make declares none: %s", out)
				}
			})
		}
	}
}

func TestMakefileHasTargetSeparatesRulesFromAssignments(t *testing.T) {
	assertMakefileTargetRows(t, makefileTargetRows)
}

// An assignment of the issue's eight forms binds no target, so a caller may append its own rule:
// util.MakefileMayDefineTarget agrees with util.MakefileHasTarget on the twelve rows but one. The
// "!=" binding (shell-value-colon) stores a command's output, which Make expands as makefile text
// wherever the variable is expanded, so it leaves ownership to Make although it declares no target
// (makefileCommandOutputRows).
func TestMakefileMayDefineTargetIssue304Forms(t *testing.T) {
	for name, tc := range makefileIssue304Rows {
		t.Run(name, func(t *testing.T) {
			want := tc.want || name == "shell-value-colon"
			if got := util.MakefileMayDefineTarget(tc.makefile, "verify-all"); got != want {
				t.Fatalf("util.MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, want)
			}
		})
	}
}

// util.MakefileMayDefineTarget decides whether a caller may append its own rule. An assignment binds
// no target and is appendable; the ambiguous forms documented in
// docs/guides/adoption-verification.md still require Make evaluation. A computed variable name
// counts among them when it may expand to .RECIPEPREFIX: "$(NAME) := x" and the $(if ...) name
// may, "$(PKG)_SRCS := a.c" cannot (makefileRecipeRows replays both kinds).
func TestMakefileMayDefineTargetSeparatesAssignmentsFromAmbiguousForms(t *testing.T) {
	for name, tc := range map[string]makefileTargetRow{
		"rule":                     {"verify-all:\n\t@echo custom\n", true},
		"double-colon-rule":        {"verify-all:: dep\n\t@echo custom\n", true},
		"help-comment-rule":        {"verify-all: lint ## run gates (FAST=1)\n\t@echo custom\n", true},
		"inline-recipe-rule":       {"verify-all: ; FOO=1 echo c\n", true},
		"include":                  {"include shared.mk\n", true},
		"define-eval":              {"define recipe\nverify-all: ; @echo custom\nendef\n$(eval $(recipe))\n", true},
		"define-bare-expansion":    {"define recipe\nverify-all: ; @echo custom\nendef\n$(recipe)\n", true},
		"unterminated-define":      {"define recipe\n@echo custom\n", true},
		"eval":                     {"$(eval verify-all: dep)\n", true},
		"brace-eval":               {"${eval verify-all: dep}\n", true},
		"generated-target":         {"$(TARGET):\n\t@echo custom\n", true},
		"pattern-target":           {"verify-%:\n\t@echo custom\n", true},
		"assignment":               {"verify-all := x\nall:\n\t@echo original\n", false},
		"posix-assignment":         {"verify-all ::= x\n", false},
		"export-assignment":        {"export verify-all := x\n", false},
		"override-assignment":      {"override verify-all = x\n", false},
		"unrelated-override":       {"override CFLAGS += -Wall\nall:\n\t@echo original\n", false},
		"recursive-value-colon":    {"verify-all = docker run --rm ci:latest check\nall:\n\t@echo original\n", false},
		"value-names-target":       {"HELP = verify-all: run every gate\nall:\n\t@echo original\n", false},
		"target-specific-variable": {"verify-all: CFLAGS := -g\nall:\n\t@echo original\n", false},
		"target-variable-comment":  {"verify-all: CFLAGS := -g # note\nall:\n\t@echo original\n", false},
		"generated-variable":       {"$(NAME) := x\n", true},
		"generated-variable-colon": {"A = 1\n$(if $(A),verify-all:c) = x\n", true},
		"generated-name-suffix":    {"PKG = app\n$(PKG)_SRCS := a.c\n", false},
		"define":                   {"define recipe\n@echo custom\nendef\n", false},
		"override-define":          {"override define recipe\n@echo custom\nendef\n", false},
		"define-body-rule":         {"define recipe\nverify-all: dep\nendef\nall:\n\t@echo original\n", false},
		"plain-rule":               {"all:\n\t@echo original\n", false},
		"empty":                    {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := util.MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.want {
				t.Fatalf("util.MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// A define body is variable text: a rule line inside one declares nothing, while the same line
// after endef does, so a define holding a verify-all template is no rule Make can run.
func TestMakefileDefineBodyDeclaresNoTarget(t *testing.T) {
	body := "define gates\nverify-all: lint\n\t@echo template\nendef\n"
	if util.MakefileHasTarget(body, "verify-all") || util.MakefileMayDefineTarget(body, "verify-all") {
		t.Fatal("a rule inside a define body was read as a declared target")
	}
	if !util.MakefileHasTarget(body+"verify-all: lint\n", "verify-all") {
		t.Fatal("a rule after endef was not read")
	}
	nested := "define outer\ndefine inner\nendef\nverify-all: lint\nendef\n"
	if util.MakefileHasTarget(nested, "verify-all") {
		t.Fatal("the inner endef closed the outer define")
	}
	for _, assignment := range []string{"define := x\n", "define = x\n", "override define ?= x\n"} {
		if !util.MakefileHasTarget(assignment+"verify-all: lint\n", "verify-all") {
			t.Fatalf("%q binds a variable named define but was read as opening a block", assignment)
		}
	}
}

// Boundary: a logical line longer than MaxMakefileLineBytes (HISS-02) is a point the reader cannot
// resolve, so the reading stops there: no target is claimed from that line or any later one, and
// MakefileMayDefineTarget reports the file as ambiguous so a caller preserves the Makefile instead
// of appending a rule that would override one Make does see. A line exactly at the bound is still
// read whole. Before the reader stopped there, it claimed a rule whose colon came before the bound
// on a longer line, and every rule after it.
func TestMakefileScanBoundLeavesOwnershipAmbiguous(t *testing.T) {
	const declaration = " verify-all: dep"
	past := strings.Repeat("x", util.MaxMakefileLineBytes) + declaration + "\n"
	if util.MakefileHasTarget(past, "verify-all") {
		t.Fatalf("target read past the %d byte scan bound", util.MaxMakefileLineBytes)
	}
	if !util.MakefileMayDefineTarget(past, "verify-all") {
		t.Fatal("a line past the scan bound must stay ambiguous so a caller preserves it")
	}
	at := strings.Repeat("x", util.MaxMakefileLineBytes-len(declaration)) + declaration + "\n"
	if !util.MakefileHasTarget(at, "verify-all") {
		t.Fatalf("a line of exactly %d bytes must still be read whole", util.MaxMakefileLineBytes)
	}
	longPrerequisites := "verify-all: " + strings.Repeat("dep ", util.MaxMakefileLineBytes/4) + "\n"
	afterLongLine := "HELP = " + strings.Repeat("x", util.MaxMakefileLineBytes) + "\nverify-all: dep\n"
	for name, makefile := range map[string]string{"long-prerequisites": longPrerequisites, "rule-after-long-line": afterLongLine} {
		if util.MakefileHasTarget(makefile, "verify-all") || !util.MakefileMayDefineTarget(makefile, "verify-all") {
			t.Fatalf("%s: a rule at or after a line past the %d byte bound was claimed", name, util.MaxMakefileLineBytes)
		}
	}
}

// Boundary: the file scan stops at util.MaxMakefileLines (HISS-02). The last line inside the bound is
// still read by the rule-versus-assignment parser, a rule one line further is not, a Makefile of
// exactly the bound built from assignments owns nothing, and one line more leaves the unread tail
// unresolved, so the ownership check reports it as ambiguous for every target.
func TestMakefileLineCountBoundLeavesOwnershipAmbiguous(t *testing.T) {
	assignments := strings.Repeat("V := a:b\n", util.MaxMakefileLines-1)
	if !util.MakefileHasTarget(assignments+"verify-all: dep", "verify-all") {
		t.Fatalf("a rule on line %d was not read", util.MaxMakefileLines)
	}
	if util.MakefileHasTarget(assignments+"verify-all := dep", "verify-all") {
		t.Fatalf("an assignment on line %d was read as a rule", util.MaxMakefileLines)
	}
	if util.MakefileHasTarget("V := a:b\n"+assignments+"verify-all: dep", "verify-all") {
		t.Fatalf("a rule on line %d, past the bound, was read", util.MaxMakefileLines+1)
	}
	if util.MakefileMayDefineTarget(assignments, "verify-all") || util.MakefileMayDefineTarget(assignments, "docs-lint") {
		t.Fatalf("a Makefile of exactly %d lines of assignments was reported as owning a target", util.MaxMakefileLines)
	}
	past := assignments + "V := c\n"
	if !util.MakefileMayDefineTarget(past, "verify-all") || !util.MakefileMayDefineTarget(past, "docs-lint") {
		t.Fatalf("a Makefile past %d lines must stay ambiguous so a caller preserves it", util.MaxMakefileLines)
	}
	// A rule continued from line MaxMakefileLines-1 onto the last line read is read whole; one
	// continued from the last line read onto the next is not, and leaves the file to Make.
	inside := strings.Repeat("V := a:b\n", util.MaxMakefileLines-2) + "verify-all \\\n  other: dep"
	if !util.MakefileHasTarget(inside, "verify-all") {
		t.Fatalf("a rule continued onto line %d was not read", util.MaxMakefileLines)
	}
	crossing := assignments + "verify-all \\\n  other: dep"
	if util.MakefileHasTarget(crossing, "verify-all") || !util.MakefileMayDefineTarget(crossing, "verify-all") {
		t.Fatalf("a rule continued past line %d was claimed or left appendable", util.MaxMakefileLines)
	}
}

// MakefileTargetRecipe reads the recipe lines of the first rule for a target, a continued recipe
// line with its continuation lines, stops at the first line that is not a recipe line, finds no
// rule inside a define body or behind an assignment, and stops reading at MaxMakefileLines.
func TestMakefileTargetRecipe(t *testing.T) {
	bound := strings.Repeat("V := a:b\n", util.MaxMakefileLines-2)
	for _, tc := range []struct {
		name, data string
		want       string
		found      bool
	}{
		{"recipe", "test:\n\tpython3 -m pytest\n\tpython3 -m mypy\n\nbuild:\n\techo b\n", "\tpython3 -m pytest\n\tpython3 -m mypy\n", true},
		{"last-line-without-newline", "test: build\n\tpython3 -m pytest", "\tpython3 -m pytest\n", true},
		{"first-rule-wins", "test: build\ntest:\n\tpython3 -m pytest\n", "", true},
		{"absent", "build:\n\techo b\n", "", false},
		{"assignment", "test := pytest\n\techo t\n", "", false},
		{"define-body", "define rules\ntest:\n\techo t\nendef\n", "", false},
		{"empty", "", "", false},
		{"recipe-at-line-bound", bound + "test:\n\techo last\n\techo unread\n", "\techo last\n", true},
		{"rule-past-line-bound", bound + "V := c\nV := d\ntest:\n\techo t\n", "", false},
		{"continued-recipe-line", "test:\n\tgo test \\\n  ./...\n\techo b\nbuild:\n", "\tgo test \\\n  ./...\n\techo b\n", true},
		{"continued-rule-line", "test: \\\n  build\n\techo t\n", "\techo t\n", true},
		{"computed-name", "T := test\n$(T):\n\techo t\n", "\techo t\n", true},
		{"computed-name-unbound", "$(T):\n\techo t\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := util.MakefileTargetRecipe(tc.data, "test")
			if got != tc.want || found != tc.found {
				t.Fatalf("util.MakefileTargetRecipe = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}
