// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package hiss

import (
	"reflect"
	"strings"
	"testing"
)

// Positive: code stays, quoted text and comments are blanked, a here-document body comes back
// as empty lines, and a backquoted substitution is written as $(...).
func TestShellCode_Positive_BlanksQuotesCommentsAndBodies(t *testing.T) {
	lines, ok := ShellCode("echo \"make test\" && go vet ./... # make\ncat <<'EOF'\nmake all\nEOF\nx=`go env GOPATH`", 10)
	want := []string{`echo "" && go vet ./... `, "cat <<", "", "", "x=$(go env GOPATH)"}
	if !ok || !reflect.DeepEqual(lines, want) {
		t.Fatalf("ShellCode = %q, %v; want %q", lines, ok, want)
	}
}

// Negative: an open quote, a here-document without its closing line and a script over the
// line bound are not readable.
func TestShellCode_Negative_UnreadableScripts(t *testing.T) {
	for name, script := range map[string]string{
		"open quote":        `echo "unclosed`,
		"open here-doc":     "cat <<EOF\nmake all",
		"over bound":        strings.Repeat("true\n", 10),
		"unreadable delim":  "cat <<E$X\nbody\nE$X",
		"open substitution": "x=$(go env",
	} {
		if lines, ok := ShellCode(script, 5); ok {
			t.Errorf("%s read as %q", name, lines)
		}
	}
}

// Boundary: the empty script is one empty line, a script at exactly the bound is read, and a
// CRLF script reads like its LF form.
func TestShellCode_Boundary_EmptyBoundAndCRLF(t *testing.T) {
	if lines, ok := ShellCode("", 1); !ok || !reflect.DeepEqual(lines, []string{""}) {
		t.Fatalf("empty script = %q, %v", lines, ok)
	}
	if _, ok := ShellCode("a\nb\nc", 3); !ok {
		t.Fatal("a script at the line bound was refused")
	}
	crlf, ok1 := ShellCode("make a\r\nmake b", 5)
	lf, ok2 := ShellCode("make a\nmake b", 5)
	if !ok1 || !ok2 || !reflect.DeepEqual(crlf, lf) {
		t.Fatalf("CRLF %q vs LF %q", crlf, lf)
	}
}

// TestShellInspection_Positive_ExecutableContexts checks code and literal payload source.
func TestShellInspection_Positive_ExecutableContexts(t *testing.T) {
	script := "echo ${v:-$(sh -c 'job &')}\ncat <<EOF\n$(job &)\nEOF"
	code, sources, ok := ShellInspection(script, 10)
	if !ok || !strings.Contains(code, "job &") || !strings.Contains(strings.Join(sources, "\n"), "sh -c 'job &'") {
		t.Fatalf("inspection: code=%q sources=%q ok=%t", code, sources, ok)
	}
}

// TestShellInspection_Negative_Unreadable checks fail-closed syntax and bounded nesting.
func TestShellInspection_Negative_Unreadable(t *testing.T) {
	for _, script := range []string{`echo "open`, "cat <<EOF\n$(job\nEOF", "cat <<EOF\n$(cat <<INNER)\nEOF", strings.Repeat("$(", 33)} {
		if _, _, ok := ShellInspection(script, 10); ok {
			t.Fatalf("accepted %q", script)
		}
	}
}

// TestShellInspection_Boundary_DataAndLines checks exact bounds and inert heredoc text.
func TestShellInspection_Boundary_DataAndLines(t *testing.T) {
	for _, script := range []string{`(( a & b ))`, `echo $(( a & b ))`, "cat <<'EOF'\n$(job &)\nEOF", "cat <<EOF\njob &\nEOF"} {
		code, _, ok := ShellInspection(script, 3)
		if !ok || strings.Contains(code, "&") {
			t.Fatalf("data inspection %q: %q %t", script, code, ok)
		}
	}
	if _, _, ok := ShellInspection("a\nb\nc", 2); ok {
		t.Fatal("accepted excessive lines")
	}
}

// TestJoinShellContinuations_CommentBoundary plants PR 1958's physical-comment regression.
func TestJoinShellContinuations_CommentBoundary(t *testing.T) {
	for _, tc := range []struct{ script, want string }{
		{"# comment \\\njob &", "# comment \\\njob &"},
		{"echo foo\\\nbar", "echo foobar"},
		{"echo foo\\\r\nbar", "echo foobar"},
		{"echo 'a\\\nb'", "echo 'a\\\nb'"},
		{"echo x\\ # comment\\\njob &", "echo x\\ # commentjob &"},
	} {
		if got := JoinShellContinuations(tc.script); got != tc.want {
			t.Fatalf("join %q: %q, want %q", tc.script, got, tc.want)
		}
	}
}
