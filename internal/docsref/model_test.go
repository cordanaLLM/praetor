// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"strings"
	"testing"
)

// testModel is a hand-built model: the vocabulary derivation has its own fixture replay.
func testModel() *CLIModel {
	state := newVocabulary()
	for _, word := range []string{"sync", "task", "add", "list"} {
		state.words[word] = true
	}
	state.addFlag("verify", false)
	state.addFlag("log", true)
	return &CLIModel{commands: map[string]*Vocabulary{"state": state, "-h": nil, "unread": nil}}
}

// problems returns the model's problems for one invocation written as text.
func problems(text string) []string {
	call := Invocations(Candidate{Text: text}, testPackage)[0]
	return testModel().Check(call)
}

func TestCheck_Positive_KnownWordsFlagsAndValues(t *testing.T) {
	for _, text := range []string{
		"praetorctl state sync --verify .",
		"praetorctl state sync --log message --verify",
		"praetorctl state sync --log=message -verify",
		"praetorctl state task [add|list]",
		"praetorctl state task <add|list>",
		"praetorctl state sync [--verify] [--log MESSAGE]",
		"praetorctl state sync -h",
		"praetorctl -h",
	} {
		if got := problems(text); len(got) != 0 {
			t.Errorf("Check(%q) = %q, want none", text, got)
		}
	}
}

func TestCheck_Negative_UnknownCommandWordAndFlag(t *testing.T) {
	cases := map[string]string{
		"praetorctl provider doctor":     `"provider" names no command`,
		"praetorctl state purge":         `no subcommand or keyword "purge"`,
		"praetorctl state sync --force":  `no flag "--force"`,
		"praetorctl state task [add|rm]": `no subcommand or keyword "rm"`,
		"praetorctl --version":           `"--version" names no command`,
	}
	for text, want := range cases {
		got := problems(text)
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("Check(%q) = %q, want one problem containing %q", text, got, want)
		}
	}
}

func TestCheck_Boundary_OperandsTerminatorAndUnreadCommands(t *testing.T) {
	for _, text := range []string{
		"praetorctl <command>",
		"praetorctl ./script",
		"praetorctl state sync [dir] PATH ./x \"quoted words\" ...",
		"praetorctl state sync -- --anything goes",
		"praetorctl state sync --log --verify",
		"praetorctl unread anything --at-all",
	} {
		if got := problems(text); len(got) != 0 {
			t.Errorf("Check(%q) = %q, want none", text, got)
		}
	}
	// A value-taking flag consumes the next word, so an unknown word after it is its value;
	// one word later the check resumes.
	if got := problems("praetorctl state sync --log purge purge"); len(got) != 1 {
		t.Errorf("value consumption must stop after one word, got %q", got)
	}
}
