package forge

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestShellFields(t *testing.T) {
	for _, tc := range []struct {
		name, script string
		want         []string
	}{
		{"operators", "cd x;git status&&cd y||true|cat\nfalse", []string{"cd", "x", ";", "git", "status", "&&", "cd", "y", "||", "true", "|", "cat", ";", "false"}},
		{"quotes", `git -C "a  b; c" -c 'key=x|y' status`, []string{"git", "-C", `"a  b; c"`, "-c", "'key=x|y'", "status"}},
		{"comments", "git status # cd hidden\ncd real", []string{"git", "status", ";", "cd", "real"}},
		{"continuation", "git sta\\\ntus", []string{"git", "status"}},
		{"escaped", `echo a\;b \"x`, []string{"echo", `a\;b`, `\"x`}},
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

func TestShellFieldsBoundaries(t *testing.T) {
	if got, err := ShellFields(strings.Repeat("x ", maxRunScriptFields)); err != nil || len(got) != maxRunScriptFields {
		t.Fatalf("at bound: %d fields, %v", len(got), err)
	}
	for _, script := range []string{
		strings.Repeat("x ", maxRunScriptFields) + "x",
		strings.Repeat("x ", maxRunScriptFields+1), `git -C "unclosed`,
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
