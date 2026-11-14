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

// TestShellCode_SplitHeredocDelimiter compares logical body lines only for unquoted
// delimiters, preserving physical output lines in both shell views and newline conventions.
func TestShellCode_SplitHeredocDelimiter(t *testing.T) {
	for _, tc := range []struct {
		name, declaration, body string
		visible                 bool
	}{
		{"unquoted", "<<EOF", "E\\\nOF", true},
		{"single quoted", "<<'EOF'", "E\\\nOF", false},
		{"double quoted", `<<"EOF"`, "E\\\nOF", false},
		{"escaped", `<<\EOF`, "E\\\nOF", false},
		{"partly quoted", `<<E"O"F`, "E\\\nOF", false},
		{"empty quoted part", `<<E''OF`, "E\\\nOF", false},
		{"partly escaped", `<<EO\F`, "E\\\nOF", false},
		{"strip tabs", "<<-EOF", "\tE\\\nOF", true},
		{"quoted strip tabs", "<<-'EOF'", "\tE\\\nOF", false},
		{"continued tab remains", "<<-EOF", "\tE\\\n\tOF", false},
		{"three pieces", "<<EOF", "E\\\nO\\\nF", true},
		{"escaped backslash", "<<EOF", "E\\\\\nOF", false},
		{"joined non delimiter", "<<EOF", "text\\\nEOF", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			script := "cat " + tc.declaration + "\n" + tc.body + "\ngo test ./internal/hiss/\nEOF"
			for _, newline := range []string{"\n", "\r\n"} {
				input := strings.ReplaceAll(script, "\n", newline)
				for _, view := range []func(string, int) ([]string, bool){ShellCode, ShellSource} {
					lines, ok := view(input, 20)
					if !ok || strings.Contains(strings.Join(lines, "\n"), "go test") != tc.visible {
						t.Errorf("view(%q) = %q, %v; command visible = %v", input, lines, ok, tc.visible)
					}
				}
			}
		})
	}
}

// TestShellCode_HeredocQuoteRemoval checks decoded delimiters and source-word widths.
func TestShellCode_HeredocQuoteRemoval(t *testing.T) {
	for _, tc := range []struct{ word, delimiter string }{
		{`'EOF'x`, "EOFx"}, {`E'O'F`, "EOF"},
		{`"E\"OF"`, `E"OF`}, {`"E\OF"`, `E\OF`},
		{`"E\\OF"`, `E\OF`}, {`'E$OF'`, "E$OF"},
		{`E\$OF`, "E$OF"}, {`'EO F'`, "EO F"},
	} {
		t.Run(tc.word, func(t *testing.T) {
			script := "cat <<" + tc.word + "; echo width\nbody\n" + tc.delimiter + "\ngo test ./internal/hiss/"
			lines, ok := ShellCode(script, 10)
			if !ok || len(lines) != 4 || !strings.Contains(lines[0], "; echo width") || lines[3] != "go test ./internal/hiss/" {
				t.Errorf("ShellCode(%q) = %q, %v", script, lines, ok)
			}
		})
	}
}

// TestShellCode_HeredocUnreadable refuses indeterminate words and missing closing lines.
func TestShellCode_HeredocUnreadable(t *testing.T) {
	for _, script := range []string{
		"cat <<EOF\nE\\\nO\ngo test ./internal/hiss/",
		"cat <<'EOF'\nE\\\nOF\ngo test ./internal/hiss/",
		"cat <<EOF\nbody\\\nEOF",
		"cat <<E$X\nE\\\nOF\nEOF",
		"cat <<E\"OF\nE\\\nOF\nEOF",
	} {
		for _, view := range []func(string, int) ([]string, bool){ShellCode, ShellSource} {
			if lines, ok := view(script, 20); ok {
				t.Errorf("unreadable script %q returned %q", script, lines)
			}
		}
	}
}

// TestJoinShellContinuations_AfterSplitHeredoc proves body state ends before code joining.
func TestJoinShellContinuations_AfterSplitHeredoc(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"cat <<EOF\nE\\\nOF\ngo\\\n test ./internal/hiss/\nEOF", "cat <<EOF\nE\\\nOF\ngo test ./internal/hiss/\nEOF"},
		{"cat <<-EOF\n\tE\\\nOF\ngo\\\n test ./internal/hiss/\nEOF", "cat <<-EOF\n\tE\\\nOF\ngo test ./internal/hiss/\nEOF"},
		{"cat <<EOF <<'END'\nE\\\nOF\nE\\\nND\nEND\ngo\\\n test", "cat <<EOF <<'END'\nE\\\nOF\nE\\\nND\nEND\ngo test"},
	} {
		if got := JoinShellContinuations(tc.input); got != tc.want {
			t.Errorf("JoinShellContinuations(%q) = %q; want %q", tc.input, got, tc.want)
		}
	}
	for _, word := range []string{`'EOF'`, `"EOF"`, `\EOF`, `E"O"F`} {
		input := "cat <<" + word + "\nE\\\nOF\ngo\\\n test\nEOF"
		if got := JoinShellContinuations(input); got != input {
			t.Errorf("quoted body changed: %q -> %q", input, got)
		}
	}
}
