// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package agenthook

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestPrivilegeLongOptionPrefixes replays ambiguous option readings without executing shell text.
func TestPrivilegeLongOptionPrefixes(t *testing.T) {
	data, err := os.ReadFile("testdata/privilege-wrappers.json")
	if err != nil {
		t.Fatal(err)
	}
	var replay struct {
		Options []struct {
			Command string `json:"command"`
			Blocked bool   `json:"blocked"`
		} `json:"option_cases"`
	}
	if err := json.Unmarshal(data, &replay); err != nil {
		t.Fatal(err)
	}
	if len(replay.Options) == 0 {
		t.Fatal("missing long option cases")
	}
	matcher := regexp.MustCompile(privilegeCommandSource)
	for _, row := range replay.Options {
		if got := matcher.MatchString(row.Command); got != row.Blocked {
			t.Errorf("%q: blocked=%v, want %v", row.Command, got, row.Blocked)
		}
	}
}

// TestEveryPrivilegeLongValuePrefix covers all wrapper and launcher option tables.
func TestEveryPrivilegeLongValuePrefix(t *testing.T) {
	matcher := regexp.MustCompile(privilegeCommandSource)
	for name, values := range privilegeLauncherValues {
		checkLongValuePrefixes(t, matcher, name, values, "'praetorctl' adopt /srv/dev")
	}
	for name, spec := range admissionWrappers {
		positionals := strings.Repeat("1 ", spec.positional)
		checkLongValuePrefixes(t, matcher, name, spec.values, positionals+"sudo 'praetorctl' adopt /srv/dev")
	}
}

// checkLongValuePrefixes tests every prefix including one letter and the full name.
func checkLongValuePrefixes(t *testing.T, matcher *regexp.Regexp, name string, values []string, command string) {
	t.Helper()
	for _, value := range values {
		if !strings.HasPrefix(value, "--") {
			continue
		}
		for end := 3; end <= len(value); end++ {
			text := name + " " + value[:end] + " option-value " + command
			if !matcher.MatchString(text) {
				t.Errorf("protected command escaped option prefix: %q", text)
			}
		}
	}
}

// TestWrapperOptionArityFindsProtectedPrivilegeCommand verifies that wrapper and launcher option arities match between Go admission and privilege rules.
func TestWrapperOptionArityFindsProtectedPrivilegeCommand(t *testing.T) {
	data, err := os.ReadFile("testdata/privilege-wrappers.json")
	if err != nil {
		t.Fatal(err)
	}
	var replay struct {
		Commands  []string `json:"commands"`
		Launchers []string `json:"launchers"`
		DataOnly  []string `json:"data_only"`
	}
	if err := json.Unmarshal(data, &replay); err != nil {
		t.Fatal(err)
	}
	matcher := regexp.MustCompile(privilegeCommandSource)
	for _, wrapper := range replay.Commands {
		t.Run(wrapper, func(t *testing.T) {
			verifyWrapperOptionArity(t, matcher, wrapper)
		})
	}
	for _, launcher := range replay.Launchers {
		command := launcher + " 'praetorctl' adopt /srv/dev"
		if !matcher.MatchString(command) || !privilegeInvocationPattern.MatchString(command) {
			t.Errorf("protected adoption escaped launcher option rule: %q", command)
		}
	}
	for _, command := range replay.DataOnly {
		if matcher.MatchString(command) {
			t.Errorf("option data treated as privilege invocation: %q", command)
		}
	}
}

// verifyWrapperOptionArity checks that commandAfter and matcher both locate the privilege escalator after wrapper options.
func verifyWrapperOptionArity(t *testing.T, matcher *regexp.Regexp, wrapper string) {
	t.Helper()
	words := strings.Fields(wrapper + " sudo praetorctl conform /srv/dev")
	if got := commandAfter(words, 0, admissionWrappers[words[0]]); got >= len(words) || words[got] != "sudo" {
		t.Errorf("wrapper option consumed incorrectly: command index %d in %q", got, words)
	}
	for _, escalator := range []string{"sudo", "doas"} {
		command := wrapper + " " + escalator + " praetorctl conform /srv/dev"
		if !matcher.MatchString(command) {
			t.Errorf("protected adoption escaped wrapper rule: %q", command)
		}
	}
}
