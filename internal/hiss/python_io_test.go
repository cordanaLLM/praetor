// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"fmt"
	"strings"
	"testing"
)

// TestPythonIO_Positive_MissingTimeoutAcrossTargetsAndImports reports every target reached through
// each import form when no deadline is passed.
func TestPythonIO_Positive_MissingTimeoutAcrossTargetsAndImports(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "pkg/calls.py", strings.Join([]string{
		"import os",                         // 1
		"import subprocess",                 // 2
		"import subprocess as sp",           // 3
		"from subprocess import run",        // 4
		"from subprocess import (",          // 5
		"    check_call as cc,",             // 6
		")",                                 // 7
		"import urllib.request",             // 8
		"from urllib import request",        // 9
		"import requests",                   // 10
		"",                                  // 11
		"subprocess.run(cmd)",               // 12
		"sp.check_output(cmd)",              // 13
		"run(cmd)",                          // 14
		"cc(cmd)",                           // 15
		"urllib.request.urlopen(url)",       // 16
		"request.urlopen(url)",              // 17
		"requests.get(url)",                 // 18
		"subprocess.run(",                   // 19
		"    cmd,",                          // 20
		"    check=True,",                   // 21
		")",                                 // 22
		"subprocess.run(cmd, timeout=None)", // 23
		`subprocess.run(cmd, env={**os.environ, "A": "1"})`, // 24
		"run(cmd, env=make(timeout=5))",                     // 25
		"urllib.request.urlopen(url, None)",                 // 26
		"urllib.request.urlopen(url, data, None)",           // 27
		"",
	}, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "pkg/calls.py", 26},
		{"HISS-02", "pkg/calls.py", 27},
		{"HISS-02", "pkg/calls.py", 12},
		{"HISS-02", "pkg/calls.py", 13},
		{"HISS-02", "pkg/calls.py", 14},
		{"HISS-02", "pkg/calls.py", 15},
		{"HISS-02", "pkg/calls.py", 16},
		{"HISS-02", "pkg/calls.py", 17},
		{"HISS-02", "pkg/calls.py", 18},
		{"HISS-02", "pkg/calls.py", 19},
		{"HISS-02", "pkg/calls.py", 23},
		{"HISS-02", "pkg/calls.py", 24},
		{"HISS-02", "pkg/calls.py", 25},
	})
}

// TestPythonIO_Negative_ExplicitDeadlinesUnpacksAndNonTargets passes calls with a deadline,
// unpacks, definitions, strings, comments and untracked names.
func TestPythonIO_Negative_ExplicitDeadlinesUnpacksAndNonTargets(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "pkg/clean.py", strings.Join([]string{
		"import subprocess",
		"from subprocess import run",
		"import urllib.request",
		"import requests",
		"",
		"subprocess.run(cmd, timeout=30)",
		"subprocess.run(",
		"    cmd,",
		"    check=True,",
		"    text=True,",
		"    timeout=15)",
		"subprocess.run(cmd, **kwargs)",
		"def run(args):",
		"    return args",
		"obj.run(x)",
		`MSG = "subprocess.run(cmd)"`,
		"# subprocess.run(cmd)",
		`subprocess.run(build(a, (b, c)), label=f"step ({i})", timeout=10)`,
		"urllib.request.urlopen(url, timeout=5)",
		"urllib.request.urlopen(url, None, 10)",
		"requests.post(url, timeout=5)",
		"",
	}, "\n"))
	writeFixture(t, root, "pkg/no_import.py", "def f():\n    run(cmd)\n    get(url)\n")

	if rep := scanFixture(t, root, ScanOptions{}); len(rep.Violations) != 0 {
		t.Fatalf("explicit timeouts, unpacks and non-targets must pass: %+v", rep.Violations)
	}
}

// TestPythonIO_Boundary_LineSpanSameLineAndPerFileAliases checks the 60-line bound, two calls on
// one line and per-file aliases.
func TestPythonIO_Boundary_LineSpanSameLineAndPerFileAliases(t *testing.T) {
	root := t.TempDir()
	lines60 := make([]string, 0, 62)
	lines60 = append(lines60, "import subprocess", "subprocess.run(")
	for i := 0; i < 58; i++ {
		lines60 = append(lines60, "    arg,")
	}
	lines60 = append(lines60, "    timeout=5)")
	writeFixture(t, root, "pkg/span60.py", strings.Join(lines60, "\n")+"\n")

	lines61 := make([]string, 0, 63)
	lines61 = append(lines61, "import subprocess", "subprocess.run(")
	for i := 0; i < 59; i++ {
		lines61 = append(lines61, "    arg,")
	}
	lines61 = append(lines61, "    timeout=5)")
	writeFixture(t, root, "pkg/span61.py", strings.Join(lines61, "\n")+"\n")

	writeFixture(t, root, "pkg/same_line.py", strings.Join([]string{
		"import subprocess",
		"subprocess.run(a, timeout=5); subprocess.run(b)",
		"",
	}, "\n"))
	writeFixture(t, root, "pkg/a_with_alias.py", "from subprocess import run as r\nr(cmd, timeout=5)\n")
	writeFixture(t, root, "pkg/b_without_alias.py", "r(cmd)\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "pkg/same_line.py", 2},
		{"HISS-02", "pkg/span61.py", 2},
	})
	if len(rep.Violations) >= 2 && !strings.Contains(rep.Violations[1].Message, "60-line") {
		t.Fatalf("expected 60-line bound message, got %q", rep.Violations[1].Message)
	}
}

// TestPythonIO_Positive_EveryRequestsVerb reports each requests verb called without a deadline.
func TestPythonIO_Positive_EveryRequestsVerb(t *testing.T) {
	root := t.TempDir()
	verbs := []string{"get", "post", "put", "patch", "delete", "head", "options", "request"}
	lines := []string{"import requests"}
	want := make([]expectedViolation, 0, len(verbs))
	for i, verb := range verbs {
		lines = append(lines, "requests."+verb+"(url)")
		want = append(want, expectedViolation{"HISS-02", "pkg/verbs.py", i + 2})
	}
	writeFixture(t, root, "pkg/verbs.py", strings.Join(lines, "\n")+"\n")
	assertViolations(t, scanFixture(t, root, ScanOptions{}), want)
}

// TestPythonIO_Boundary_UnclosedCallsAndNonCodeLines reports a call still open at the end of the
// file, with or without trailing comment and blank lines, and counts comment lines toward the
// 60-line bound.
func TestPythonIO_Boundary_UnclosedCallsAndNonCodeLines(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "pkg/eof.py", "import subprocess\nsubprocess.run(\n    cmd,\n")
	writeFixture(t, root, "pkg/eof_comments.py", "import subprocess\nsubprocess.run(\n    cmd,\n# trailing comment\n\n")
	span := []string{"import subprocess", "subprocess.run("}
	for i := 0; i < 61; i++ {
		span = append(span, "    # comment")
	}
	span = append(span, "    timeout=5)")
	writeFixture(t, root, "pkg/comment_span.py", strings.Join(span, "\n")+"\n")

	assertViolations(t, scanFixture(t, root, ScanOptions{}), []expectedViolation{
		{"HISS-02", "pkg/comment_span.py", 2},
		{"HISS-02", "pkg/eof.py", 2},
		{"HISS-02", "pkg/eof_comments.py", 2},
	})
}

// TestPythonIO_Boundary_PendingCallLimit passes maxPythonIOPending open calls and reports the
// next call opened while maxPythonIOPending calls are already pending.
func TestPythonIO_Boundary_PendingCallLimit(t *testing.T) {
	root := t.TempDir()
	atLimit := strings.Join([]string{
		"import subprocess",
		strings.Repeat("subprocess.run(", maxPythonIOPending) + "cmd",
		strings.Repeat(", timeout=5)", maxPythonIOPending),
		"",
	}, "\n")
	writeFixture(t, root, "pkg/pending32.py", atLimit)

	overLimit := strings.Join([]string{
		"import subprocess",
		strings.Repeat("subprocess.run(", maxPythonIOPending),
		"subprocess.run(cmd",
		strings.Repeat(", timeout=5)", maxPythonIOPending+1),
		"",
	}, "\n")
	writeFixture(t, root, "pkg/pending33.py", overLimit)

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "pkg/pending33.py", 3},
	})
	if len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0].Message, "more than 32 I/O calls open at once") {
		t.Fatalf("expected pending-limit message, got %+v", rep.Violations)
	}
}

// TestPythonIO_Boundary_LineByteLimit passes a 65536-byte line and reports a 65537-byte line in a
// file with tracked I/O bindings.
func TestPythonIO_Boundary_LineByteLimit(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "pkg/line65536.py", "import subprocess\n"+strings.Repeat("a", 65536)+"\n")
	writeFixture(t, root, "pkg/line65537.py", "import subprocess\n"+strings.Repeat("a", 65537)+"\n")
	writeFixture(t, root, "pkg/untracked_long.py", strings.Repeat("a", 65537)+"\n")

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "pkg/line65537.py", 2},
	})
	if len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0].Message, "65536-byte") {
		t.Fatalf("expected 65536-byte limit message, got %+v", rep.Violations)
	}
}

// TestPythonIO_Boundary_TrackedBindingLimit passes maxPythonIOTracked distinct bindings (including
// rebinding an existing alias) and reports the 65th binding at its import line.
func TestPythonIO_Boundary_TrackedBindingLimit(t *testing.T) {
	root := t.TempDir()
	lines64 := make([]string, 0, maxPythonIOTracked+2)
	for i := 0; i < maxPythonIOTracked; i++ {
		lines64 = append(lines64, fmt.Sprintf("import subprocess as sp%d", i))
	}
	lines64 = append(lines64, "import requests as sp0", "")
	writeFixture(t, root, "pkg/tracked64.py", strings.Join(lines64, "\n"))

	lines65 := make([]string, 0, maxPythonIOTracked+2)
	for i := 0; i < maxPythonIOTracked; i++ {
		lines65 = append(lines65, fmt.Sprintf("import subprocess as sp%d", i))
	}
	lines65 = append(lines65, "from subprocess import run as r64", "")
	writeFixture(t, root, "pkg/tracked65.py", strings.Join(lines65, "\n"))

	rep := scanFixture(t, root, ScanOptions{})
	assertViolations(t, rep, []expectedViolation{
		{"HISS-02", "pkg/tracked65.py", 65},
	})
	if len(rep.Violations) != 1 || !strings.Contains(rep.Violations[0].Message, "more than 64 tracked I/O bindings") {
		t.Fatalf("expected tracked-binding limit message, got %+v", rep.Violations)
	}
}
