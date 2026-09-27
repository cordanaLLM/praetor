// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package docsref

import (
	"strings"
	"testing"
)

// modelSource is a CLI in the shapes the real dispatch table uses: a switch on args[0], an
// if chain, a leaf reached through a positional action parsed after the flags, a handler that
// takes its operand as a string, a handler that re-reads its own word, a git argument
// literal that must not become a flag, and a subcommand that reads the dispatch table, which
// must not inherit another command's flags.
const modelSource = `package main

import "flag"

func runDocs(args []string) error {
	if len(args) == 0 {
		return nil
	}
	switch args[0] {
	case "lookup":
		return runDocsLookup(args[1:])
	case "references":
		return runDocsReferences(args[1:])
	}
	return nil
}

func runDocsLookup(args []string) error {
	fs := flag.NewFlagSet("docs lookup", flag.ContinueOnError)
	fs.Bool("offline", false, "")
	return fs.Parse(args)
}

func runDocsReferences(args []string) error {
	fs := flag.NewFlagSet("docs references", flag.ContinueOnError)
	fs.String("path", ".", "")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(table()) == 0 {
		return nil
	}
	return git([]string{"ls-files", "--porcelain", "--exclude-standard"})
}

func git(argv []string) error { return nil }

func table() map[string]func([]string) error {
	return map[string]func([]string) error{"docs": runDocs, "other": runOther}
}

func runOther(args []string) error {
	fs := flag.NewFlagSet("other", flag.ContinueOnError)
	fs.Bool("strict", false, "")
	return fs.Parse(args)
}

func runAgent(args []string) error {
	if len(args) == 0 || args[0] == "list" {
		return nil
	}
	if args[0] == "run" {
		return dispatchAgentTask(args[1], args[2:])
	}
	return nil
}

func dispatchAgentTask(name string, args []string) error {
	switch name {
	case "praetor-gatekeeper":
		return nil
	}
	return nil
}

func runModels(args []string) error {
	fs := flag.NewFlagSet("models", flag.ContinueOnError)
	fs.String("config", "", "")
	task := fs.String("task", "", "")
	positional := parse(fs, args)
	action := first(positional)
	switch action {
	case "route":
		return route(*task)
	case "sync":
		return nil
	}
	return nil
}

func parse(fs *flag.FlagSet, args []string) []string { return fs.Args() }

func first(list []string) string { return "" }

func route(task string) error { return nil }

func runDogfood(args []string) error {
	if len(args) > 0 && args[0] == "repairs" {
		return runRepairs(args[1:])
	}
	return nil
}

func runRepairs(args []string) error {
	if len(args) > 0 && (args[0] == "run" || args[0] == "status") {
		return runRepairAction(args)
	}
	return nil
}

func runRepairAction(args []string) error {
	fs := flag.NewFlagSet("repairs", flag.ContinueOnError)
	fs.String("task", "", "")
	switch args[0] {
	case "run":
		return fs.Parse(args[1:])
	}
	return nil
}
`

// testModel builds the model of modelSource through the real source reader.
func testModel(t *testing.T) *CLIModel {
	t.Helper()
	root, inventory := writeModule(t, map[string]string{"cmd/standardsctl/main.go": modelSource})
	tree, err := newSourceTree(root, inventory)
	if err != nil {
		t.Fatalf("newSourceTree: %v", err)
	}
	model, err := buildModel(t.Context(), tree, Options{Package: "cmd/standardsctl", Commands: map[string]string{
		"docs": "runDocs", "agent": "runAgent", "models": "runModels", "dogfood": "runDogfood", "other": "runOther",
	}})
	if err != nil {
		t.Fatalf("buildModel: %v", err)
	}
	return model
}

// problems returns the model's problems for each invocation written as text.
func problems(t *testing.T, model *CLIModel, text string) []string {
	t.Helper()
	call := Invocations(Candidate{Text: text}, testPackage)[0]
	got, err := model.Check(t.Context(), call)
	if err != nil {
		t.Fatalf("Check(%q): %v", text, err)
	}
	return got
}

func TestCheck_Positive_SubcommandsFlagsAndOperandsBelowALeaf(t *testing.T) {
	model := testModel(t)
	for _, text := range []string{
		"praetorctl docs lookup cobra",
		"praetorctl docs lookup cobra --offline",
		"praetorctl docs references --path=.",
		"praetorctl docs <lookup|references>",
		"praetorctl agent run my-agent",
		"praetorctl agent list",
		"praetorctl models route summarization",
		"praetorctl models --task commit_message_synthesis route",
		"praetorctl dogfood repairs run nightly --task t",
		"praetorctl docs references -h",
		"praetorctl other --strict",
	} {
		if got := problems(t, model, text); len(got) != 0 {
			t.Errorf("Check(%q) = %q, want none", text, got)
		}
	}
}

func TestCheck_Negative_UnknownSubcommandsAndFlagsThatAreNotDefinitions(t *testing.T) {
	model := testModel(t)
	cases := map[string]string{
		"praetorctl provider doctor":                    `"provider" names no command`,
		"praetorctl docs frobnicate":                    `command docs has no subcommand "frobnicate"`,
		"praetorctl docs <lookup|zzqx>":                 `command docs has no subcommand "zzqx"`,
		"praetorctl agent walk":                         `command agent has no subcommand "walk"`,
		"praetorctl docs references --porcelain":        `command docs references has no flag "--porcelain"`,
		"praetorctl docs references --actor=me":         `command docs references has no flag "--actor"`,
		"praetorctl docs lookup cobra --path=.":         `command docs lookup has no flag "--path"`,
		"praetorctl dogfood repairs smash":              `command dogfood repairs has no subcommand "smash"`,
		"praetorctl models route --offline":             `command models route has no flag "--offline"`,
		"praetorctl --version":                          `"--version" names no command`,
		"praetorctl docs references --strict":           `command docs references has no flag "--strict"`,
		"praetorctl docs references --exclude-standard": `has no flag "--exclude-standard"`,
	}
	for text, want := range cases {
		got := problems(t, model, text)
		if len(got) != 1 || !strings.Contains(got[0], want) {
			t.Errorf("Check(%q) = %q, want one problem containing %q", text, got, want)
		}
	}
}

func TestCheck_Boundary_OperandsTerminatorAndValues(t *testing.T) {
	model := testModel(t)
	for _, text := range []string{
		"praetorctl",
		"praetorctl <command>",
		"praetorctl ./script",
		"praetorctl docs [path] PATH ./x \"quoted words\" ...",
		"praetorctl docs -- --anything goes",
		"praetorctl models --config route",
	} {
		if got := problems(t, model, text); len(got) != 0 {
			t.Errorf("Check(%q) = %q, want none", text, got)
		}
	}
	// One unknown word ends the walk: the words after it are not reported a second time.
	if got := problems(t, model, "praetorctl docs frob more words"); len(got) != 1 {
		t.Errorf("an unknown word must be reported once, got %q", got)
	}
}
