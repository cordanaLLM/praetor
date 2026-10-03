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
	for _, name := range []string{"A", "B", "R", "T", "V", "X", "PREFIX", "SRCS", "CFLAGS", "HELP", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
}

// makeDeclaresVerifyAll writes makefile with a "dep" rule appended, so a rule row fails for no
// other reason than a missing verify-all, and rules.txt holding a verify-all rule for the rows that
// read it, and reports whether "make -n verify-all" prints the row's recipe.
func makeDeclaresVerifyAll(t *testing.T, makePath, makefile string) (bool, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(makefile+"dep: ;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "rules.txt"), []byte("verify-all: ; @echo custom\n"), 0o600); err != nil {
		t.Fatal(err)
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

// Replayed against the installed GNU Make: Make's answer must be the row's, and the reader's live
// answers must not fail open against it -- Make declaring verify-all implies MakefileMayDefineTarget,
// and MakefileHasTarget implies Make declaring it.
func TestMakefileOwnershipRowsGNUReplay(t *testing.T) {
	makePath := testsupport.GNUMake(t)
	clearMakeEnvironment(t)
	for _, rows := range []map[string]makefileOwnershipRow{makefileIssue554Rows, makefileBoundRows, makefileSilentRows, makefileNameRows, makefileRecipeRows} {
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
// util.MakefileMayDefineTarget agrees with util.MakefileHasTarget on every one of the twelve rows.
func TestMakefileMayDefineTargetIssue304Forms(t *testing.T) {
	for name, tc := range makefileIssue304Rows {
		t.Run(name, func(t *testing.T) {
			if got := util.MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.want {
				t.Fatalf("util.MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// util.MakefileMayDefineTarget decides whether a caller may append its own rule. An assignment binds
// no target and is appendable; the ambiguous forms documented in
// docs/guides/adoption-verification.md still require Make evaluation.
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
		"generated-variable":       {"$(NAME) := x\n", false},
		"generated-variable-colon": {"A = 1\n$(if $(A),verify-all:c) = x\n", false},
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := util.MakefileTargetRecipe(tc.data, "test")
			if got != tc.want || found != tc.found {
				t.Fatalf("util.MakefileTargetRecipe = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}
