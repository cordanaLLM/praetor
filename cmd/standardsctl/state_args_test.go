// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/state"
)

// initStateFixture creates a ledger in a fresh temporary directory and returns it.
func initStateFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatalf("state init: %v", err)
	}
	return dir
}

// BUG-895: a stray positional used to stop flag parsing, so `--severity` after it was
// dropped and the bug was written with the default severity while the command exited 0.
func TestStateBugAdd_FlagsAfterStrayPositional(t *testing.T) {
	dir := initStateFixture(t)
	err := dispatchCommand("state", []string{"bug", "add", "--title=first", "stray", "--severity=p0", "--dir=" + dir})
	if err == nil || !strings.Contains(err.Error(), `"stray"`) {
		t.Fatalf("negative: a stray positional must be refused by name, got %v", err)
	}
	if bugs, listErr := state.ListBugs(dir, "all"); listErr != nil || len(bugs) != 0 {
		t.Fatalf("negative: a refused command must write nothing, got %v (%v)", bugs, listErr)
	}

	// Positive: every flag binds wherever it is written.
	if err := dispatchCommand("state", []string{"bug", "add", "--dir=" + dir, "--title=first", "--severity=p0"}); err != nil {
		t.Fatalf("positive: %v", err)
	}
	// Boundary: a trailing terminator adds no positional.
	if err := dispatchCommand("state", []string{"bug", "add", "--title=second", "--severity=p1", "--dir=" + dir, "--"}); err != nil {
		t.Fatalf("boundary: trailing terminator: %v", err)
	}
	bugs, err := state.ListBugs(dir, "all")
	if err != nil || len(bugs) != 2 || bugs[0].Severity != "p0" || bugs[1].Severity != "p1" {
		t.Fatalf("want severities p0 and p1 recorded, got %+v (%v)", bugs, err)
	}
}

func TestStateQuestionAdd_FlagsAfterStrayPositional(t *testing.T) {
	dir := initStateFixture(t)
	err := dispatchCommand("state", []string{"question", "add", "--prompt=which", "stray", "--options=A,B", "--dir=" + dir})
	if err == nil || !strings.Contains(err.Error(), `"stray"`) {
		t.Fatalf("negative: a stray positional must be refused by name, got %v", err)
	}
	if questions, listErr := state.ListQuestions(dir, "all"); listErr != nil || len(questions) != 0 {
		t.Fatalf("negative: a refused command must write nothing, got %v (%v)", questions, listErr)
	}

	if err := dispatchCommand("state", []string{"question", "add", "--dir=" + dir, "--prompt=which", "--options=A,B"}); err != nil {
		t.Fatalf("positive: %v", err)
	}
	questions, err := state.ListQuestions(dir, "all")
	if err != nil || len(questions) != 1 || strings.Join(questions[0].Options, ",") != "A,B" {
		t.Fatalf("want one question with options A,B, got %+v (%v)", questions, err)
	}
}

// BUG-895: the shared parser dropped the "--" terminator itself, so a task description
// starting with a dash was parsed as an undefined flag even after "--".
func TestStateTaskAdd_TerminatorKeepsDashedDescription(t *testing.T) {
	dir := initStateFixture(t)
	const desc = "--leading-dash task"
	if err := dispatchCommand("state", []string{"task", "add", "--dir=" + dir, "--", desc}); err != nil {
		t.Fatalf("positive: a description after -- must be positional: %v", err)
	}
	if err := dispatchCommand("state", []string{"task", "add", "--dir=" + dir, desc}); err == nil {
		t.Fatal("negative: without -- the dashed description is an undefined flag")
	}
	tasks, err := state.ListTasks(dir)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	found := 0
	for _, task := range tasks {
		if task.Description == desc {
			found++
		}
	}
	if found != 1 {
		t.Fatalf("want the dashed description recorded once, got %+v", tasks)
	}
}

// caveman check is one of the commands whose FlagSet stopped at the first input path.
func TestCavemanCheck_FlagsAfterInputPath(t *testing.T) {
	terse := writeFixtureFile(t, t.TempDir(), "terse.md", cavemanTerse)
	if out, err := runCavemanCLI(t, "", "check", terse, "--kind=message"); err != nil || !strings.Contains(out, ": PASS") {
		t.Fatalf("positive: --kind after the input must bind, not be read as a file: err=%v\n%s", err, out)
	}
	if _, err := runCavemanCLI(t, "", "check", terse, "--kind=bogus"); err == nil || !strings.Contains(err.Error(), "unsupported kind") {
		t.Fatalf("negative: an invalid --kind after the input must be validated, got %v", err)
	}
	if _, err := runCavemanCLI(t, "", "check", terse, "--", "--kind=message"); err == nil {
		t.Fatal("boundary: after -- the token is an input path, which does not exist")
	}
}

// provenance parsed raw argv, so a positional before -file dropped it and the run failed
// with "-file is required" instead of naming the stray argument.
func TestProvenanceFlags_AnyOrderAndStrayPositional(t *testing.T) {
	got, err := parseProvenanceFlags([]string{"--builder=b", "--out", "p.json", "--file=a.tar.gz"})
	if err != nil || got.file != "a.tar.gz" || got.out != "p.json" || got.builder != "b" {
		t.Fatalf("positive: every flag binds, got %+v (%v)", got, err)
	}
	_, err = parseProvenanceFlags([]string{"--builder=b", "stray", "--file=a.tar.gz"})
	if err == nil || !strings.Contains(err.Error(), `"stray"`) {
		t.Fatalf("negative: a stray positional must be refused by name, got %v", err)
	}
	if _, err := parseProvenanceFlags([]string{"--builder=b", "--file=a.tar.gz", "--"}); err != nil {
		t.Fatalf("boundary: a trailing terminator adds no positional: %v", err)
	}
	_, err = parseProvenanceFlags([]string{"--builder=b", "--", "--file=a.tar.gz"})
	if err == nil || !strings.Contains(err.Error(), `"--file=a.tar.gz"`) {
		t.Fatalf("boundary: after -- a flag-shaped token is a positional, got %v", err)
	}
}
