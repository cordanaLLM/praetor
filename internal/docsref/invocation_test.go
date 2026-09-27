// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"slices"
	"testing"
)

const testPackage = "cmd/standardsctl"

// calls returns the argument lists of every invocation in text.
func calls(text string, shell bool) [][]string {
	var found [][]string
	for _, call := range Invocations(Candidate{Text: text, Shell: shell}, testPackage) {
		found = append(found, call.Args)
	}
	return found
}

func TestInvocations_Positive_EveryBinarySpelling(t *testing.T) {
	cases := map[string][]string{
		"praetorctl state sync .":                                 {"state", "sync", "."},
		"standardsctl audit":                                      {"audit"},
		"./bin/praetorctl gate run":                               {"gate", "run"},
		`C:\tools\praetorctl.exe version`:                         {"version"},
		"go run ./cmd/standardsctl audit --path=.":                {"audit", "--path=."},
		"go run github.com/o/r/cmd/standardsctl@v1.2.3 docs sync": {"docs", "sync"},
		"FOO=1 BAR=2 sudo praetorctl hook claude stop":            {"hook", "claude", "stop"},
	}
	for text, want := range cases {
		got := calls(text, true)
		if len(got) != 1 || !slices.Equal(got[0], want) {
			t.Errorf("Invocations(%q) = %q, want one call %q", text, got, want)
		}
	}
}

func TestInvocations_Negative_MentionsAreNotCalls(t *testing.T) {
	for _, text := range []string{
		"this praetorctl serves no pair",
		"praetor hook: no engine serves claude",
		"go run ./cmd/standards-mcp serve",
		"echo praetorctl audit",
		"praetorctl-wrapper audit",
	} {
		if got := calls(text, false); len(got) != 0 {
			t.Errorf("Invocations(%q) = %q, want none", text, got)
		}
	}
}

func TestInvocations_Boundary_SeparatorsCommentsAndRedirections(t *testing.T) {
	got := calls("make build && praetorctl audit | tee out; (praetorctl gc --json) # praetorctl nosuch", true)
	want := [][]string{{"audit"}, {"gc", "--json"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Fatalf("calls = %q, want %q", got, want)
	}
	got = calls("praetorctl audit > out.txt 2>&1", true)
	if len(got) != 1 || len(got[0]) != 1 {
		t.Fatalf("a redirection must end the arguments, got %q", got)
	}
	got = calls(`praetorctl state task add "fix the | pipe"`, true)
	if len(got) != 1 || len(got[0]) != 4 {
		t.Fatalf("a quoted separator must stay inside its word, got %q", got)
	}
	if got = calls("praetorctl", false); len(got) != 1 || len(got[0]) != 0 {
		t.Fatalf("a bare binary is one call with no arguments, got %q", got)
	}
}
