package forge

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestShellFields checks shell words and code or data here-documents.
func TestShellFields(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         []string
	}{
		{"data cd hidden", "cat <<'EOF'\ncd /tmp\nEOF\ngh pr comment 1", []string{"cat", ";", "gh", "pr", "comment", "1"}},
		{"data sbom hidden", "cat <<'EOF'\nsyft packages\nEOF", []string{"cat", ";"}},
		{"shell heredoc code", "bash <<'EOF'\ngh pr view 1\nEOF", []string{"bash", ";", "gh", "pr", "view", "1", ";"}},
		{"cat heredoc data", "cat <<'EOF'\nreviewer's\nEOF", []string{"cat", ";"}},
		{"assignment cat data", "FOO=1 cat > out.md <<'EOF'\nreviewer's\nEOF", []string{"FOO=1", "cat", ">", "out.md", ";"}},
		{"absolute cat data", "/usr/bin/cat <<'EOF'\nreviewer's\nEOF", []string{"/usr/bin/cat", ";"}},
		{"sudo tee code", "sudo tee out.md <<'EOF'\ngh pr view 1\nEOF", []string{"sudo", "tee", "out.md", ";", "gh", "pr", "view", "1", ";"}},
		{"heredoc", "cat <<'EOF'\nreviewer's note\nEOF\ngh pr view 1", []string{"cat", ";", "gh", "pr", "view", "1"}},
		{"two heredocs", "cat <<A <<-B\none\nA\n\ttwo\n\tB\n", []string{"cat", ";"}},
		{"delimiter trailing space", "cat <<EOF\nEOF \nreviewer's\nEOF\n", []string{"cat", ";"}},
		{"operators", "cd x;git status&&cd y||true|cat\nfalse", []string{"cd", "x", ";", "git", "status", "&&", "cd", "y", "||", "true", "|", "cat", ";", "false"}},
		{"quotes", `git -C "a  b; c" -c 'key=x|y' status`, []string{"git", "-C", `"a  b; c"`, "-c", "'key=x|y'", "status"}},
		{"comments", "git status # cd hidden\ncd real", []string{"git", "status", ";", "cd", "real"}},
		{"continuation", "git sta\\\ntus", []string{"git", "status"}},
		{"escaped", `echo a\;b \"x`, []string{"echo", `a\;b`, `\"x`}},
		{"20w token line", `token="$(printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"`, []string{`token="$(printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p')"`}},
		{"nested command substitution", `"$(echo "x y")"`, []string{`"$(echo "x y")"`}},
		{"printf command substitution", `"$(printf '%s' "x")"`, []string{`"$(printf '%s' "x")"`}},
		{"backquote form", `token="` + "`" + `printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'` + "`" + `"`, []string{`token="` + "`" + `printf '%s' "$token_response" | sed -n 's/.*"token":"\([^"]*\)".*/\1/p'` + "`" + `"`}},
		{"backquote echo", `"` + "`" + `echo "x y"` + "`" + `"`, []string{`"` + "`" + `echo "x y"` + "`" + `"`}},
		{"empty", "", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellFields(tc.script)
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ShellFields = %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}

// TestShellFieldsBoundaries rejects oversized and malformed scripts.
func TestShellFieldsBoundaries(t *testing.T) {
	if got, err := ShellFields(strings.Repeat("x ", maxRunScriptFields)); err != nil || len(got) != maxRunScriptFields {
		t.Fatalf("at bound: %d fields, %v", len(got), err)
	}
	for _, script := range []string{
		strings.Repeat("x ", maxRunScriptFields) + "x",
		strings.Repeat("x ", maxRunScriptFields+1), `git -C "unclosed`, "cat <<EOF\nmissing\n", `cat <<<'it's'`,
		"bash <<EOF\nreviewer's\nEOF\n",
	} {
		if got, err := ShellFields(script); err == nil || got != nil {
			t.Errorf("unreadable script accepted: %d fields, %v", len(got), err)
		}
	}
	if _, err := ShellFields(strings.Repeat("x ", maxRunScriptFields+1)); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("field overflow must be identifiable: %v", err)
	}
}

func TestShellAssignment(t *testing.T) {
	for _, tc := range []struct {
		word, name, value string
		ok                bool
	}{
		{`GIT_DIR="a b/.git"`, "GIT_DIR", `"a b/.git"`, true},
		{"X=", "X", "", true}, {"git", "", "", false}, {"--git-dir=x", "", "", false},
	} {
		t.Run(tc.word, func(t *testing.T) {
			name, value, ok := ShellAssignment(tc.word)
			if name != tc.name || value != tc.value || ok != tc.ok {
				t.Fatalf("assignment = %q %q %v", name, value, ok)
			}
		})
	}
}

func TestShellFieldsLineLimit(t *testing.T) {
	atBound := "\"" + strings.Repeat("\n", 99999) + "\""
	if fields, err := ShellFields(atBound); err != nil || len(fields) != 1 {
		t.Fatalf("100000 lines: %d fields, %v", len(fields), err)
	}
	if _, err := ShellFields(atBound + "\n"); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("line overflow must be identifiable: %v", err)
	}
}

func TestShellFields_NestedQuoteErrors(t *testing.T) {
	unclosed := []struct {
		name, script string
	}{
		{"real unclosed quote", `echo "abc`},
		{"unclosed substitution quote", `"$(echo "x)"`},
		{"unclosed inner substitution", `"$(echo "x"`},
	}
	for _, tc := range unclosed {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ShellFields(tc.script)
			if err == nil || got != nil {
				t.Fatalf("ShellFields(%s) accepted: %v", tc.name, got)
			}
			if !strings.Contains(err.Error(), "run script has an unclosed quote") {
				t.Fatalf("error = %v; want unclosed quote", err)
			}
		})
	}

	var pastBound strings.Builder
	for i := 0; i < 17; i++ {
		pastBound.WriteString(`"$(`)
	}
	if _, err := ShellFields(pastBound.String()); !errors.Is(err, ErrShellReadLimit) {
		t.Fatalf("nesting overflow must return ErrShellReadLimit, got: %v", err)
	}
}
