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
