package adopt

import (
	"path/filepath"
	"strings"
	"testing"
)

// A Makefile line is a rule only when a colon reaches the parser before any assignment operator,
// within the text Make still reads: it cuts the line at the first unescaped "#" or ";" first, so
// an "=" a comment or an inline recipe carries decides nothing. Make itself draws that line, and
// every row below was measured against GNU Make 4.4.1 with a Makefile holding only that line.
// A negative answers "make verify-all" with "No rule to make target 'verify-all'" and exits 2 --
// except comment-before-colon and semicolon-before-colon, which Make rejects outright with
// "missing separator"; neither declares a target, so adoption must not declare "make verify-all"
// against any of them.
func TestMakefileTargetDetectionSeparatesRulesFromAssignments(t *testing.T) {
	for name, tc := range map[string]struct {
		line string
		want bool
	}{
		"rule":                      {"verify-all:\n\t@echo custom\n", true},
		"rule-with-deps":            {"verify-all: build test\n\t@echo custom\n", true},
		"double-colon-rule":         {"verify-all:: dep\n\t@echo custom\n", true},
		"target-list":               {"all verify-all: dep\n\t@echo custom\n", true},
		"windows-path-dep":          {"verify-all: C:\\deps\\stamp\n\t@echo custom\n", true},
		"substitution-prerequisite": {"verify-all: $(SRCS:.c=.o)\n\t@echo custom\n", true},
		"target-variable-then-rule": {"verify-all: CFLAGS := -g\nverify-all:\n\t@echo custom\n", true},
		"help-comment-assignment":   {"verify-all: lint ## run gates (FAST=1)\n\t@echo custom\n", true},
		"trailing-comment-equals":   {"verify-all: dep # set X=1\n\t@echo custom\n", true},
		"inline-recipe-assignment":  {"verify-all: ; FOO=1 echo c\n", true},
		"double-colon-help-comment": {"verify-all:: dep ## run gates (X=1)\n\t@echo custom\n", true},
		"escaped-hash-prerequisite": {"verify-all: dep\\#1\n\t@echo custom\n", true},
		"simple-assignment":         {"verify-all := x\n", false},
		"posix-assignment":          {"verify-all ::= x\n", false},
		"escaped-assignment":        {"verify-all :::= x\n", false},
		"unspaced-assignment":       {"verify-all:=x\n", false},
		"exported-assignment":       {"export verify-all := x\n", false},
		"override-assignment":       {"override verify-all := x\n", false},
		"recursive-value-colon":     {"verify-all = docker run --rm ci:latest check\n", false},
		"conditional-value-colon":   {"verify-all ?= a:b\n", false},
		"appending-value-colon":     {"verify-all += x:y\n", false},
		"shell-value-colon":         {"verify-all != date +%H:%M\n", false},
		"substitution-value":        {"verify-all = $(SRCS:.c=.o)\n", false},
		"value-names-target":        {"HELP = verify-all: run every gate\n", false},
		"target-specific-variable":  {"verify-all: CFLAGS := -g\n", false},
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
	} {
		t.Run(name, func(t *testing.T) {
			if got := hasVerificationTarget(tc.line, "verify-all"); got != tc.want {
				t.Fatalf("hasVerificationTarget(%q) = %v, want %v", tc.line, got, tc.want)
			}
		})
	}
}

// End to end: a Makefile whose only mention of verify-all is a variable assignment owns no
// verify-all rule, so adoption appends its own targets instead of preserving and certifying a
// rule that make cannot run.
func TestVerificationAssignmentIsNotAPreservedTarget(t *testing.T) {
	for name, tc := range map[string]struct {
		makefile  string
		preserved bool
	}{
		"assignment":              {"verify-all := $(MAKE) -C build check\nall:\n\t@echo original\n", false},
		"override-assignment":     {"override verify-all := $(MAKE) -C build check\nall:\n\t@echo original\n", false},
		"value-colon-assignment":  {"verify-all = docker run --rm ci:latest check\nall:\n\t@echo original\n", false},
		"target-specific-varible": {"verify-all: CFLAGS := -g\nall:\n\t@echo original\n", false},
		"rule":                    {"verify-all:\n\t@echo claimed\n", true},
		"rule-with-help-comment":  {"verify-all: ## run gates (FAST=1)\n\t@echo claimed\n", true},
	} {
		t.Run(name, func(t *testing.T) {
			root := newTestRepo(t, name)
			mustWrite(t, filepath.Join(root, "go.mod"), "module fixture\n")
			mustWrite(t, filepath.Join(root, "Makefile"), tc.makefile)
			report, err := Adopt(t.Context(), AdoptOptions{Path: root, Profile: "framework", LockSourceRoot: newAdoptLockSource(t)})
			if err != nil {
				t.Fatal(err)
			}
			got := mustRead(t, filepath.Join(root, "Makefile"))
			if tc.preserved {
				want, mergeErr := mergeDocumentationMakefile(tc.makefile, false)
				if mergeErr != nil {
					t.Fatal(mergeErr)
				}
				if report.Verification.Status != verificationPreserved || got != want {
					t.Fatalf("custom rule not preserved: %+v %q", report.Verification, got)
				}
				return
			}
			if report.Verification.Status != verificationDeclared {
				t.Fatalf("assignment certified as a custom gate: %+v", report.Verification)
			}
			if !strings.Contains(got, "\nverify-all:\n") || !strings.Contains(got, "@echo original") {
				t.Fatalf("declared verify-all not appended beside the existing recipes: %q", got)
			}
			for _, command := range report.Verification.Test {
				if strings.Join(command, " ") == "make verify-all" {
					t.Fatalf("plan tests a preserved rule that does not exist: %+v", report.Verification)
				}
			}
		})
	}
}

// mayDefineVerificationTarget decides whether adoption may append its own rule. An assignment
// binds no target and is appendable; the ambiguous forms documented in
// docs/guides/adoption-verification.md still require Make evaluation and stay preserved.
func TestMakefileOwnershipSeparatesAssignmentsFromAmbiguousForms(t *testing.T) {
	for name, tc := range map[string]struct {
		makefile string
		want     bool
	}{
		"rule":                     {"verify-all:\n\t@echo custom\n", true},
		"double-colon-rule":        {"verify-all:: dep\n\t@echo custom\n", true},
		"help-comment-rule":        {"verify-all: lint ## run gates (FAST=1)\n\t@echo custom\n", true},
		"inline-recipe-rule":       {"verify-all: ; FOO=1 echo c\n", true},
		"include":                  {"include shared.mk\n", true},
		"define":                   {"define recipe\n@echo custom\nendef\n", true},
		"override-define":          {"override define recipe\n@echo custom\nendef\n", true},
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
		"plain-rule":               {"all:\n\t@echo original\n", false},
		"empty":                    {"", false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := mayDefineVerificationTarget(tc.makefile); got != tc.want {
				t.Fatalf("mayDefineVerificationTarget(%q) = %v, want %v", tc.makefile, got, tc.want)
			}
		})
	}
}

// The appended block skips a helper target the project already declares. A variable of the same
// name declares nothing, so compile-context and audit must still be appended; without them the
// appended verify-all recipe references prerequisites make cannot build. The rows that discriminate
// are value-colon and target-variable, which the "first colon wins" reader took for rules, and
// help-comment, which the target-specific-variable guard took for an assignment; the plain ":="
// rows are regression cover, correct in every generation of the reader.
func TestAppendedHelperTargetsIgnoreVariableAssignments(t *testing.T) {
	plan := &VerificationPlan{Status: verificationDeclared}
	for name, tc := range map[string]struct {
		existing string
		want     bool
	}{
		"assignment":        {"compile-context := x\naudit := y\nall:\n\t@echo original\n", true},
		"override-variable": {"override compile-context := x\noverride audit := y\n", true},
		"value-colon":       {"compile-context = go run ./x:latest\naudit = ci:audit\n", true},
		"target-variable":   {"compile-context: CFLAGS := -g\naudit: CFLAGS := -g\n", true},
		"rule":              {"compile-context:\n\t@echo c\naudit:\n\t@echo a\n", false},
		"help-comment": {"compile-context: dep ## compile (X=1)\n\t@echo c\n" +
			"audit: dep ## audit (Y=2)\n\t@echo a\n", false},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := appendVerificationTargets(tc.existing, plan)
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range []string{"compile-context", "audit"} {
				if strings.Contains(got, "\n"+target+":\n\t@$(PRAETORCTL)") != tc.want {
					t.Fatalf("appended %s target = %v, want %v: %q", target, !tc.want, tc.want, got)
				}
			}
		})
	}
}

// Boundary: the line scan stops at maxMakefileLineBytes (HISS-02), so a longer line is read in
// part and its ownership is unresolved rather than decided. The reader reports no target for it,
// and mayDefineVerificationTarget reports it as ambiguous so adoption preserves the Makefile
// instead of appending a rule that would override one Make does see. A line exactly at the bound
// is still read whole.
func TestMakefileScanBoundLeavesOwnershipAmbiguous(t *testing.T) {
	const declaration = " verify-all: dep"
	past := strings.Repeat("x", maxMakefileLineBytes) + declaration + "\n"
	if hasVerificationTarget(past, "verify-all") {
		t.Fatalf("target read past the %d byte scan bound", maxMakefileLineBytes)
	}
	if !mayDefineVerificationTarget(past) {
		t.Fatal("a line past the scan bound must stay ambiguous so adoption preserves it")
	}
	at := strings.Repeat("x", maxMakefileLineBytes-len(declaration)) + declaration + "\n"
	if !hasVerificationTarget(at, "verify-all") {
		t.Fatalf("a line of exactly %d bytes must still be read whole", maxMakefileLineBytes)
	}
}

// Boundary: the file scan stops at maxMakefileLines (HISS-02). The last line inside the bound is
// still read by the rule-versus-assignment parser, a Makefile of exactly the bound built from
// assignments owns nothing, and one line more leaves the unread tail unresolved, so both the
// verify-all and the docs-lint ownership checks report it as ambiguous.
func TestMakefileLineCountBoundLeavesOwnershipAmbiguous(t *testing.T) {
	assignments := strings.Repeat("V := a:b\n", maxMakefileLines-1)
	if !hasVerificationTarget(assignments+"verify-all: dep", "verify-all") {
		t.Fatalf("a rule on line %d was not read", maxMakefileLines)
	}
	if hasVerificationTarget(assignments+"verify-all := dep", "verify-all") {
		t.Fatalf("an assignment on line %d was read as a rule", maxMakefileLines)
	}
	if mayDefineVerificationTarget(assignments) || mayDefineTarget(assignments, "docs-lint") {
		t.Fatalf("a Makefile of exactly %d lines of assignments was reported as owning a target", maxMakefileLines)
	}
	past := assignments + "V := c\n"
	if !mayDefineVerificationTarget(past) || !mayDefineTarget(past, "docs-lint") {
		t.Fatalf("a Makefile past %d lines must stay ambiguous so adoption preserves it", maxMakefileLines)
	}
}
