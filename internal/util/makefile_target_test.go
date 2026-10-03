package util

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
			if got := MakefileHasTarget(tc.makefile, "verify-all"); got != tc.want {
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

// gnuMakeCandidates names the candidates tried in order on any platform (HISS-21).
var gnuMakeCandidates = []string{"make", "gmake", "mingw32-make"}

// resolveGNUMake finds the first GNU Make binary on PATH that reports GNU Make 4.x.
// If make is absent or not GNU Make 4.x, it skips the test with a stated reason (HISS-21).
func resolveGNUMake(t *testing.T) string {
	t.Helper()
	var tried []string
	for _, name := range gnuMakeCandidates {
		path, err := exec.LookPath(name)
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (not on PATH)", name))
			continue
		}
		version, err := RunCommand(t.Context(), t.TempDir(), path, "--version")
		if err != nil {
			tried = append(tried, fmt.Sprintf("%s (%v)", path, err))
			continue
		}
		firstLine := strings.SplitN(version, "\n", 2)[0]
		if strings.HasPrefix(version, "GNU Make 4.") {
			return path
		}
		tried = append(tried, fmt.Sprintf("%s (%s)", path, firstLine))
	}
	t.Skipf("GNU Make 4.x is required for replay; tried: %s", strings.Join(tried, "; "))
	return ""
}

// Replayed against the installed GNU Make, both directions: "make -n verify-all" fails for the
// eight assignments, which declare no rule, and prints the recipe for the four rules. A "dep" rule
// is appended so a rule row fails for no other reason than a missing verify-all.
func TestMakefileIssue304FormsGNUReplay(t *testing.T) {
	makePath := resolveGNUMake(t)
	// Make imports the environment as variables, so a caller's SRCS or MAKEFLAGS would change
	// what the rows expand to.
	for _, name := range []string{"SRCS", "CFLAGS", "HELP", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
	for name, tc := range makefileIssue304Rows {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(tc.makefile+"dep: ;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := RunCommand(t.Context(), root, makePath, "--no-print-directory", "-n", "verify-all")
			if declared := err == nil && strings.Contains(out, "echo custom"); declared != tc.want {
				t.Fatalf("GNU Make declares verify-all = %v, the reader's row says %v: %q %v", declared, tc.want, out, err)
			}
		})
	}
}

// makefileIssue554Row represents one shape from issue #554, whether the shared Makefile reader
// finds a target (hasTarget), whether it reports the file may define it (mayDefine), and whether
// GNU Make 4.x resolves it as a rule (makeTarget).
type makefileIssue554Row struct {
	makefile   string
	hasTarget  bool
	mayDefine  bool
	makeTarget bool
}

// makefileIssue554Rows covers the six shapes from issue #554 measured against GNU Make 4.4.1.
var makefileIssue554Rows = map[string]makefileIssue554Row{
	"single-line-template": {
		makefile:   "make-rule = $(1): ; @echo custom\n$(call make-rule,verify-all)\n",
		hasTarget:  false,
		mayDefine:  true,
		makeTarget: true,
	},
	"bare-expansion-backslash": {
		makefile:   "X = 1\n$(if $(X),verify-all \\\n  : ; @echo custom)\n",
		hasTarget:  false,
		mayDefine:  true,
		makeTarget: true,
	},
	"variable-value-rule": {
		makefile:   "A = verify-all\nB = : ; @echo custom\n$(A)$(B)\n",
		hasTarget:  false,
		mayDefine:  true,
		makeTarget: true,
	},
	"indirect-variable-rule": {
		makefile:   "A = verify-all\nB = : ; @echo custom\nR = $(A)$(B)\n$(R)\n",
		hasTarget:  false,
		mayDefine:  true,
		makeTarget: true,
	},
	"shell-expansion-rule": {
		makefile:   "$(shell echo 'verify-all: ; @echo custom')\n",
		hasTarget:  false,
		mayDefine:  true,
		makeTarget: true,
	},
	"redirected-shell-expansion": {
		makefile:   "$(shell mkdir -p build >/dev/null 2>&1)\nall:\n\t@echo all\n",
		hasTarget:  false,
		mayDefine:  false,
		makeTarget: false,
	},
	"simple-assign-colon": {
		makefile:   "make-rule := $(1): ; @echo custom\nall:\n\t@echo all\n",
		hasTarget:  false,
		mayDefine:  false,
		makeTarget: false,
	},
	"continued-assignment": {
		makefile:   "HELP = usage \\\n  verify-all: run every gate\nall:\n\t@echo all\n",
		hasTarget:  false,
		mayDefine:  false,
		makeTarget: false,
	},
	"continued-recipe": {
		makefile:   "other:\n\t@echo step 1 \\\n  verify-all: not a rule\n",
		hasTarget:  false,
		mayDefine:  false,
		makeTarget: false,
	},
	"continued-target-name": {
		makefile:   "verify-all \\\n  other: dep\n\t@echo custom\n",
		hasTarget:  true,
		mayDefine:  true,
		makeTarget: true,
	},
}

// TestMakefileIssue554Forms tests that the shared Makefile reader handles all six shapes from
// issue #554 correctly: continuations are joined before classifying, and dynamic top-level
// expansions are left to Make (MakefileMayDefineTarget = true, MakefileHasTarget = false).
func TestMakefileIssue554Forms(t *testing.T) {
	for name, tc := range makefileIssue554Rows {
		t.Run(name, func(t *testing.T) {
			if got := MakefileHasTarget(tc.makefile, "verify-all"); got != tc.hasTarget {
				t.Fatalf("MakefileHasTarget(%q) = %v, want %v", tc.makefile, got, tc.hasTarget)
			}
			if got := MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.mayDefine {
				t.Fatalf("MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.mayDefine)
			}
		})
	}
}

// TestMakefileIssue554FormsGNUReplay replays the issue #554 forms against GNU Make 4.x.
func TestMakefileIssue554FormsGNUReplay(t *testing.T) {
	makePath := resolveGNUMake(t)
	for _, name := range []string{"A", "B", "R", "X", "HELP", "SRCS", "CFLAGS", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
	for name, tc := range makefileIssue554Rows {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(tc.makefile+"dep: ;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := RunCommand(t.Context(), root, makePath, "--no-print-directory", "-n", "verify-all")
			if declared := err == nil && (strings.Contains(out, "echo custom") || strings.Contains(out, "echo all")); declared != tc.makeTarget {
				t.Fatalf("GNU Make declares verify-all = %v, the test row says %v: %q %v", declared, tc.makeTarget, out, err)
			}
		})
	}
}

func buildContinuationMakefile(prefix, step, suffix string, count int) string {
	var b strings.Builder
	b.WriteString(prefix)
	for i := 0; i < count; i++ {
		b.WriteString(step)
	}
	b.WriteString(suffix)
	return b.String()
}

func TestMakefileContinuationBounds(t *testing.T) {
	atBoundAssignment := buildContinuationMakefile("HELP = usage \\\n", "  step \\\n", "  verify-all: not a rule\nall:\n\t@echo all\n", 255)
	pastBoundAssignment := buildContinuationMakefile("HELP = usage \\\n", "  step \\\n", "  verify-all: not a rule\nall:\n\t@echo all\n", 299)
	atBoundRule := buildContinuationMakefile("verify-all \\\n", "  step \\\n", "  other: dep\n\t@echo custom\n", 255)
	pastBoundRule := buildContinuationMakefile("verify-all \\\n", "  step \\\n", "  other: dep\n\t@echo custom\n", 299)

	t.Run("at-bound-assignment", func(t *testing.T) {
		if got := MakefileHasTarget(atBoundAssignment, "verify-all"); got != false {
			t.Fatalf("MakefileHasTarget = %v, want false", got)
		}
		if got := MakefileMayDefineTarget(atBoundAssignment, "verify-all"); got != false {
			t.Fatalf("MakefileMayDefineTarget = %v, want false", got)
		}
	})
	t.Run("past-bound-assignment", func(t *testing.T) {
		if got := MakefileHasTarget(pastBoundAssignment, "verify-all"); got != false {
			t.Fatalf("MakefileHasTarget = %v, want false", got)
		}
		if got := MakefileMayDefineTarget(pastBoundAssignment, "verify-all"); got != true {
			t.Fatalf("MakefileMayDefineTarget = %v, want true", got)
		}
	})
	t.Run("at-bound-rule", func(t *testing.T) {
		if got := MakefileHasTarget(atBoundRule, "verify-all"); got != true {
			t.Fatalf("MakefileHasTarget = %v, want true", got)
		}
		if got := MakefileMayDefineTarget(atBoundRule, "verify-all"); got != true {
			t.Fatalf("MakefileMayDefineTarget = %v, want true", got)
		}
	})
	t.Run("past-bound-rule", func(t *testing.T) {
		if got := MakefileHasTarget(pastBoundRule, "verify-all"); got != false {
			t.Fatalf("MakefileHasTarget = %v, want false", got)
		}
		if got := MakefileMayDefineTarget(pastBoundRule, "verify-all"); got != true {
			t.Fatalf("MakefileMayDefineTarget = %v, want true", got)
		}
	})
}

func TestMakefileContinuationBoundsGNUReplay(t *testing.T) {
	makePath := resolveGNUMake(t)
	for _, name := range []string{"HELP", "MAKEFLAGS", "MFLAGS", "GNUMAKEFLAGS", "MAKEFILES"} {
		t.Setenv(name, "")
	}
	atBoundAssignment := buildContinuationMakefile("HELP = usage \\\n", "  step \\\n", "  verify-all: not a rule\nall:\n\t@echo all\n", 255)
	pastBoundAssignment := buildContinuationMakefile("HELP = usage \\\n", "  step \\\n", "  verify-all: not a rule\nall:\n\t@echo all\n", 299)
	atBoundRule := buildContinuationMakefile("verify-all \\\n", "  step \\\n", "  other: dep\n\t@echo custom\n", 255)
	pastBoundRule := buildContinuationMakefile("verify-all \\\n", "  step \\\n", "  other: dep\n\t@echo custom\n", 299)

	cases := map[string]struct {
		makefile   string
		makeTarget bool
	}{
		"at-bound-assignment":   {atBoundAssignment, false},
		"past-bound-assignment": {pastBoundAssignment, false},
		"at-bound-rule":         {atBoundRule, true},
		"past-bound-rule":       {pastBoundRule, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "Makefile"), []byte(tc.makefile+"dep: ;\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			out, err := RunCommand(t.Context(), root, makePath, "--no-print-directory", "-n", "verify-all")
			if declared := err == nil && (strings.Contains(out, "echo custom") || strings.Contains(out, "echo all")); declared != tc.makeTarget {
				t.Fatalf("GNU Make declares verify-all = %v, want %v: %q %v", declared, tc.makeTarget, out, err)
			}
		})
	}
}

func TestMakefileHasTargetSeparatesRulesFromAssignments(t *testing.T) {
	assertMakefileTargetRows(t, makefileTargetRows)
}

// An assignment of the issue's eight forms binds no target, so a caller may append its own rule:
// MakefileMayDefineTarget agrees with MakefileHasTarget on every one of the twelve rows.
func TestMakefileMayDefineTargetIssue304Forms(t *testing.T) {
	for name, tc := range makefileIssue304Rows {
		t.Run(name, func(t *testing.T) {
			if got := MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.want {
				t.Fatalf("MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// MakefileMayDefineTarget decides whether a caller may append its own rule. An assignment binds
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
			if got := MakefileMayDefineTarget(tc.makefile, "verify-all"); got != tc.want {
				t.Fatalf("MakefileMayDefineTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// A define body is variable text: a rule line inside one declares nothing, while the same line
// after endef does, so a define holding a verify-all template is no rule Make can run.
func TestMakefileDefineBodyDeclaresNoTarget(t *testing.T) {
	body := "define gates\nverify-all: lint\n\t@echo template\nendef\n"
	if MakefileHasTarget(body, "verify-all") || MakefileMayDefineTarget(body, "verify-all") {
		t.Fatal("a rule inside a define body was read as a declared target")
	}
	if !MakefileHasTarget(body+"verify-all: lint\n", "verify-all") {
		t.Fatal("a rule after endef was not read")
	}
	nested := "define outer\ndefine inner\nendef\nverify-all: lint\nendef\n"
	if MakefileHasTarget(nested, "verify-all") {
		t.Fatal("the inner endef closed the outer define")
	}
	for _, assignment := range []string{"define := x\n", "define = x\n", "override define ?= x\n"} {
		if !MakefileHasTarget(assignment+"verify-all: lint\n", "verify-all") {
			t.Fatalf("%q binds a variable named define but was read as opening a block", assignment)
		}
	}
}

// Boundary: the line scan stops at MaxMakefileLineBytes (HISS-02), so a longer line is read in
// part and its ownership is unresolved rather than decided. The reader reports no target for it,
// and MakefileMayDefineTarget reports it as ambiguous so a caller preserves the Makefile instead
// of appending a rule that would override one Make does see. A line exactly at the bound is still
// read whole.
func TestMakefileScanBoundLeavesOwnershipAmbiguous(t *testing.T) {
	const declaration = " verify-all: dep"
	past := strings.Repeat("x", MaxMakefileLineBytes) + declaration + "\n"
	if MakefileHasTarget(past, "verify-all") {
		t.Fatalf("target read past the %d byte scan bound", MaxMakefileLineBytes)
	}
	if !MakefileMayDefineTarget(past, "verify-all") {
		t.Fatal("a line past the scan bound must stay ambiguous so a caller preserves it")
	}
	at := strings.Repeat("x", MaxMakefileLineBytes-len(declaration)) + declaration + "\n"
	if !MakefileHasTarget(at, "verify-all") {
		t.Fatalf("a line of exactly %d bytes must still be read whole", MaxMakefileLineBytes)
	}
}

// Boundary: the file scan stops at MaxMakefileLines (HISS-02). The last line inside the bound is
// still read by the rule-versus-assignment parser, a rule one line further is not, a Makefile of
// exactly the bound built from assignments owns nothing, and one line more leaves the unread tail
// unresolved, so the ownership check reports it as ambiguous for every target.
func TestMakefileLineCountBoundLeavesOwnershipAmbiguous(t *testing.T) {
	assignments := strings.Repeat("V := a:b\n", MaxMakefileLines-1)
	if !MakefileHasTarget(assignments+"verify-all: dep", "verify-all") {
		t.Fatalf("a rule on line %d was not read", MaxMakefileLines)
	}
	if MakefileHasTarget(assignments+"verify-all := dep", "verify-all") {
		t.Fatalf("an assignment on line %d was read as a rule", MaxMakefileLines)
	}
	if MakefileHasTarget("V := a:b\n"+assignments+"verify-all: dep", "verify-all") {
		t.Fatalf("a rule on line %d, past the bound, was read", MaxMakefileLines+1)
	}
	if MakefileMayDefineTarget(assignments, "verify-all") || MakefileMayDefineTarget(assignments, "docs-lint") {
		t.Fatalf("a Makefile of exactly %d lines of assignments was reported as owning a target", MaxMakefileLines)
	}
	past := assignments + "V := c\n"
	if !MakefileMayDefineTarget(past, "verify-all") || !MakefileMayDefineTarget(past, "docs-lint") {
		t.Fatalf("a Makefile past %d lines must stay ambiguous so a caller preserves it", MaxMakefileLines)
	}
}

// MakefileTargetRecipe reads the recipe lines of the first rule for a target, stops at the first
// line that is not a recipe line, finds no rule inside a define body or behind an assignment, and
// stops reading at MaxMakefileLines.
func TestMakefileTargetRecipe(t *testing.T) {
	bound := strings.Repeat("V := a:b\n", MaxMakefileLines-2)
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
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, found := MakefileTargetRecipe(tc.data, "test")
			if got != tc.want || found != tc.found {
				t.Fatalf("MakefileTargetRecipe = %q, %v; want %q, %v", got, found, tc.want, tc.found)
			}
		})
	}
}
