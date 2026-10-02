package editor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// verifyAllOffered reports whether editor generation offers the Verify All task for a workspace
// whose Makefile holds exactly makefile, and checks the answer against the reader adoption decides
// verify-all ownership with (util.MakefileHasTarget), so the two cannot disagree on a form.
func verifyAllOffered(t *testing.T, makefile string) bool {
	t.Helper()
	root := t.TempDir()
	writeTestFile(t, root, "Makefile", makefile)
	commands, err := detectWorkspaceCommands(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	offered := false
	for _, command := range commands {
		if command.Program == "make" && strings.Join(command.Args, " ") == "verify-all" {
			offered = true
		}
	}
	if shared := util.MakefileHasTarget(makefile, "verify-all"); shared != offered {
		t.Fatalf("editor offers the task = %v, the shared Makefile reader finds a rule = %v: %q", offered, shared, makefile)
	}
	return offered
}

// Negative: the eight forms issue #304 measured against GNU Make 4.4.1, each answered with "No
// rule to make target 'verify-all'". The editor's former line test, a cut at the first colon,
// read every one as a rule and bound a task to it. The last two rows are forms the same test got
// wrong and the issue's comment and the shared reader's define tracking add.
func TestWorkspaceCommands_Negative_AssignmentIsNoVerifyAllTask(t *testing.T) {
	for name, makefile := range map[string]string{
		"posix-assignment":         "verify-all ::= x\n",
		"escaped-assignment":       "verify-all :::= x\n",
		"recursive-value-colon":    "verify-all = docker run --rm ci:latest check\n",
		"conditional-value-colon":  "verify-all ?= a:b\n",
		"appending-value-colon":    "verify-all += x:y\n",
		"shell-value-colon":        "verify-all != date +%H:%M\n",
		"target-specific-variable": "verify-all: CFLAGS := -g\n",
		"value-names-target":       "HELP = verify-all: run every gate\n",
		"comment-before-colon":     "verify-all # : dep\n",
		"define-body-rule":         "define gates\nverify-all: lint\nendef\nall:\n\t@true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if verifyAllOffered(t, makefile) {
				t.Fatalf("a Makefile with no verify-all rule got a Verify All task: %q", makefile)
			}
		})
	}
}

// Positive: the rule forms the issue lists, plus a help comment that carries "=", which the
// former test read correctly and must keep reading as a rule.
func TestWorkspaceCommands_Positive_RuleIsAVerifyAllTask(t *testing.T) {
	for name, makefile := range map[string]string{
		"rule":                      "verify-all:\n\t@true\n",
		"double-colon-rule":         "verify-all:: dep\n\t@true\n",
		"target-list":               "all verify-all: dep\n\t@true\n",
		"substitution-prerequisite": "verify-all: $(SRCS:.c=.o)\n\t@true\n",
		"help-comment-assignment":   "verify-all: lint ## run gates (FAST=1)\n\t@true\n",
		"rule-after-define":         "define gates\nverify-all: lint\nendef\nverify-all:\n\t@true\n",
	} {
		t.Run(name, func(t *testing.T) {
			if !verifyAllOffered(t, makefile) {
				t.Fatalf("a Makefile with a verify-all rule got no Verify All task: %q", makefile)
			}
		})
	}
}

// Boundary: a CRLF checkout declares the same rule and the same assignment, a rule past the
// reader's line bound is not read, a Makefile only Make can resolve offers no task because no
// rule was observed, and a workspace without a Makefile offers none without an error.
func TestWorkspaceCommands_Boundary_UnreadRuleIsNoVerifyAllTask(t *testing.T) {
	if !verifyAllOffered(t, "all:\r\n\t@true\r\nverify-all: all\r\n\t@true\r\n") {
		t.Fatal("a CRLF Makefile with a verify-all rule got no Verify All task")
	}
	if verifyAllOffered(t, "verify-all := x\r\nall:\r\n\t@true\r\n") {
		t.Fatal("a CRLF assignment got a Verify All task")
	}
	inside := strings.Repeat("V := a:b\n", util.MaxMakefileLines-1) + "verify-all: dep"
	if !verifyAllOffered(t, inside) {
		t.Fatalf("a rule on line %d, the last one read, got no Verify All task", util.MaxMakefileLines)
	}
	if verifyAllOffered(t, "V := a:b\n"+inside) {
		t.Fatalf("a rule past line %d was read", util.MaxMakefileLines)
	}
	for _, unresolved := range []string{"include shared.mk\n", "verify-%:\n\t@true\n", "$(eval verify-all: dep)\n"} {
		if verifyAllOffered(t, unresolved) {
			t.Fatalf("a Makefile only Make can resolve got a Verify All task: %q", unresolved)
		}
	}
	commands, err := detectWorkspaceCommands(t.Context(), t.TempDir())
	if err != nil || len(commands) != 0 {
		t.Fatalf("a workspace without a Makefile: commands=%v err=%v", commands, err)
	}
}

// Negative: a Makefile that exists but cannot be read is an error, not a missing task. A
// directory in the Makefile's place fails the read on every platform.
func TestWorkspaceCommands_Negative_UnreadableMakefileIsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "Makefile"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := detectWorkspaceCommands(t.Context(), root); err == nil {
		t.Fatalf("a directory named Makefile was read as a Makefile without a rule on %s", runtime.GOOS)
	}
}
