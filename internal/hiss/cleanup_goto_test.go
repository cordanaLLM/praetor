// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

// cleanupGoto is the exception as a repository that declares and documents it scans with.
var cleanupGoto = CleanupGoto{Enabled: true}

// gotoFindings scans src as one C file under exception and returns the HISS-01 lines, sorted.
func gotoFindings(t *testing.T, src string, exception CleanupGoto) []int {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "unit.c", src)
	rep := scanFixture(t, root, ScanOptions{CleanupGoto: exception})
	var lines []int
	for _, v := range rep.Violations {
		if v.RuleID == "HISS-01" {
			lines = append(lines, v.LineNumber)
		}
	}
	slices.Sort(lines)
	return lines
}

func cSource(lines ...string) string {
	return strings.Join(lines, "\n") + "\n"
}

// forwardCleanup is the shape the exception exists for: a single-level forward jump, from a
// nested block, to the one cleanup label directly in the function body.
var forwardCleanup = cSource(
	"int open_all(int n) {", // 1
	"    int rc = 0;",       // 2
	"    if (n < 0) {",      // 3
	"        rc = -1;",      // 4
	"        goto out;",     // 5
	"    }",                 // 6
	"    if (n == 0)",       // 7
	"        goto out;",     // 8
	"    rc = n;",           // 9
	"out:",                  // 10
	"    return rc;",        // 11
	"}",                     // 12
)

// TestCleanupGoto_Positive_DeclaredExceptionAcceptsForwardCleanup: with the exception, every
// forward jump to the sole body-level cleanup label passes, whatever block it leaves.
func TestCleanupGoto_Positive_DeclaredExceptionAcceptsForwardCleanup(t *testing.T) {
	if got := gotoFindings(t, forwardCleanup, cleanupGoto); len(got) != 0 {
		t.Fatalf("forward cleanup gotos reported at %v under the exception", got)
	}
	for _, label := range cleanupGotoLabels {
		src := strings.ReplaceAll(forwardCleanup, "out", label)
		if got := gotoFindings(t, src, cleanupGoto); len(got) != 0 {
			t.Errorf("label %q: reported at %v under the exception", label, got)
		}
	}
	declared := strings.ReplaceAll(forwardCleanup, "out", "unwind")
	if got := gotoFindings(t, declared, CleanupGoto{Enabled: true, Labels: []string{"unwind"}}); len(got) != 0 {
		t.Errorf("declared label unwind reported at %v", got)
	}
}

// TestCleanupGoto_Negative_UndeclaredReportsEveryGoto: without the exception the same file
// reports each goto, exactly as the scan did before the exception existed.
func TestCleanupGoto_Negative_UndeclaredReportsEveryGoto(t *testing.T) {
	if got := gotoFindings(t, forwardCleanup, CleanupGoto{}); !slices.Equal(got, []int{5, 8}) {
		t.Fatalf("undeclared exception: HISS-01 at %v, want [5 8]", got)
	}
	// Labels alone enable nothing: the declaration is Enabled.
	if got := gotoFindings(t, forwardCleanup, CleanupGoto{Labels: []string{"out"}}); !slices.Equal(got, []int{5, 8}) {
		t.Fatalf("labels without the exception: HISS-01 at %v, want [5 8]", got)
	}
}

// TestCleanupGoto_Negative_OtherGotosStillFail: every goto outside the rule is reported under
// the exception: backward, into or within a nested block, a second label, another name, a label
// in another function, a computed goto and a goto outside any function.
func TestCleanupGoto_Negative_OtherGotosStillFail(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []int
	}{
		{"backward", cSource("int f(void) {", "out:", "    work();", "    goto out;", "}"), []int{4}},
		{"label in nested block", cSource("int f(int n) {", "    goto out;", "    if (n) {", "out:", "        n = 0;", "    }", "    return n;", "}"), []int{2}},
		{"jump within nested block", cSource("int f(int n) {", "    if (n) {", "        goto out;", "out:", "        n = 0;", "    }", "    return n;", "}"), []int{3}},
		{"two labels", cSource("int f(int n) {", "    if (n) goto a;", "    goto err;", "    goto out;", "err:", "    n = 1;", "out:", "    return n;", "}"), []int{3, 4}},
		{"undeclared name", cSource("int f(void) {", "    goto done;", "done:", "    return 0;", "}"), []int{2}},
		{"label in other function", cSource("int f(void) {", "    goto out;", "    return 1;", "}", "int g(void) {", "out:", "    return 0;", "}"), []int{2}},
		{"computed goto", cSource("int f(void *p) {", "    goto *p;", "out:", "    return 0;", "}"), []int{2}},
		{"outside function", cSource("goto out;", "out:"), []int{1}},
		{"unclosed body", cSource("int f(void) {", "    goto out;", "    return 0;"), []int{2}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gotoFindings(t, tc.src, cleanupGoto); !slices.Equal(got, tc.want) {
				t.Fatalf("HISS-01 at %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCleanupGoto_Boundary_LabelRecognition pins what counts as the function's label: a switch
// default, a C++ access specifier, a qualified name and a case never do, a label after a closing
// brace does at the depth that brace leaves, and the label on the very next line still follows.
func TestCleanupGoto_Boundary_LabelRecognition(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want []int
	}{
		{"next line", cSource("int f(void) {", "    goto out;", "out:", "    return 0;", "}"), nil},
		{"default, case and qualified names", cSource(
			"int f(int n) {", "    switch (n) {", "    case 1:", "        goto out;", "    default:",
			"        std::puts(\"x\");", "    }", "public:", "out:", "    return n;", "}"), nil},
		{"label after closing brace", cSource("int f(int n) {", "    if (n) {", "        goto out;", "    } out:", "    return n;", "}"), nil},
		{"label after inner closing brace", cSource("int f(int n) {", "    if (n) {", "        if (n > 1) {", "            goto out;", "        } out:", "    }", "    return n;", "}"), []int{4}},
		{"label sharing line with statement", cSource("int f(void) {", "    goto fail;", "fail: return -1;", "}"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := gotoFindings(t, tc.src, cleanupGoto); !slices.Equal(got, tc.want) {
				t.Fatalf("HISS-01 at %v, want %v", got, tc.want)
			}
		})
	}
}

// TestCleanupGoto_Boundary_PendingBound: a function holds at most maxPendingGotos gotos; the next
// is reported at once, so the bound fails closed rather than dropping a finding.
func TestCleanupGoto_Boundary_PendingBound(t *testing.T) {
	lines := []string{"int f(int n) {"}
	for i := 0; i <= maxPendingGotos; i++ {
		lines = append(lines, "    if (n == "+fmt.Sprint(i)+")", "        goto out;")
	}
	lines = append(lines, "out:", "    return n;", "}")
	got := gotoFindings(t, cSource(lines...), cleanupGoto)
	if want := []int{1 + 2*(maxPendingGotos+1)}; !slices.Equal(got, want) {
		t.Fatalf("HISS-01 at %v, want only the goto past the bound %v", got, want)
	}
}

// TestValidCleanupGotoLabel covers the declarable label shape: a C identifier of at most 64 bytes.
func TestValidCleanupGotoLabel(t *testing.T) {
	for _, name := range []string{"unwind", "_x", "L1", strings.Repeat("a", maxCleanupGotoLabelLen)} {
		if !ValidCleanupGotoLabel(name) {
			t.Errorf("ValidCleanupGotoLabel(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "1x", "a-b", "a b", "out:", strings.Repeat("a", maxCleanupGotoLabelLen+1)} {
		if ValidCleanupGotoLabel(name) {
			t.Errorf("ValidCleanupGotoLabel(%q) = true, want false", name)
		}
	}
}

// TestCleanupGotoRule_StatesEveryCondition holds the rule text to what cleanupGotoAllowed checks:
// each default label name, the declared-label key, forward, sole label, same function and the
// body level.
func TestCleanupGotoRule_StatesEveryCondition(t *testing.T) {
	rule := CleanupGotoRule()
	want := []string{"forward jump", "sole label", "same function", "directly in function body",
		"outside nested blocks", "`hiss.exceptions.c_goto_cleanup_labels`"}
	for _, label := range cleanupGotoLabels {
		want = append(want, "`"+label+"`")
	}
	for _, phrase := range want {
		if !strings.Contains(rule, phrase) {
			t.Errorf("CleanupGotoRule() = %q, missing %q", rule, phrase)
		}
	}
}
