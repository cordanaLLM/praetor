package main

import (
	"errors"
	"flag"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

// epicCommandPattern captures the arguments of every `praetorctl ...` command span a generated
// pre-migration epic writes.
var epicCommandPattern = regexp.MustCompile("`praetorctl ([^`]+)`")

// maxEpicCommands bounds the command scan (HISS-02).
const maxEpicCommands = 64

// signalledEpic generates the epic of a Go repository carrying every signal that adds a
// command-bearing step: a Helm chart and runner routing declared in its fleet tier.
func signalledEpic(t *testing.T) *needs.PreMigrationEpic {
	t.Helper()
	repo := t.TempDir()
	writeFixtureFile(t, repo, "go.mod", "module example.org/consumer\ngo 1.27\n")
	writeFixtureFile(t, repo, "Chart.yaml", "apiVersion: v2\nname: consumer\nversion: 0.1.0\n")
	writeFixtureFile(t, repo, ".config/fleet.yaml", "runners:\n  default: example-runner-set-linux-amd64\n")
	epic, err := needs.GeneratePreMigrationEpic(t.Context(), repo, needs.FrameworkSource{}, nil)
	if err != nil {
		t.Fatalf("epic generation failed: %v", err)
	}
	return epic
}

// epicCommands returns every distinct praetorctl command line the epic's checklist and task
// bodies name, in the order they first appear.
func epicCommands(t *testing.T, epic *needs.PreMigrationEpic) []string {
	t.Helper()
	texts := []string{epic.ChecklistMarkdown}
	for _, child := range epic.ChildIssues {
		texts = append(texts, child.Body)
	}
	var commands []string
	for _, text := range texts {
		for _, match := range epicCommandPattern.FindAllStringSubmatch(text, maxEpicCommands) {
			if !slices.Contains(commands, match[1]) {
				commands = append(commands, match[1])
			}
		}
	}
	return commands
}

// probeCommand dispatches a command line with -h appended: its flag set parses every flag the
// line names and stops at help before the command runs. A line naming an undefined flag or
// subcommand fails instead.
func probeCommand(t *testing.T, line string) error {
	t.Helper()
	fields := strings.Fields(line)
	_, err := captureStdout(t, func() error { return dispatchCommand(fields[0], append(fields[1:], "-h")) })
	if errors.Is(err, flag.ErrHelp) {
		return nil
	}
	return err
}

// Positive: every praetorctl command a generated epic names parses against the real CLI, so an
// epic can no longer hand an adopter an undefined flag such as `gate run --target` (#296).
func TestEpicCommands_Positive_ParseAgainstTheCLI(t *testing.T) {
	call := recordPipeline(t)
	for _, line := range epicCommands(t, signalledEpic(t)) {
		if err := probeCommand(t, line); err != nil {
			t.Errorf("`praetorctl %s` does not parse: %v", line, err)
		}
	}
	if call.called {
		t.Fatal("a help probe started the gating pipeline")
	}
}

// Negative: the probe rejects what the CLI rejects, so a green run is evidence.
func TestEpicCommands_Negative_ProbeRejectsUndefinedFlags(t *testing.T) {
	recordPipeline(t)
	for _, line := range []string{"gate run --target=.", "sync --lock-rulesets"} {
		if err := probeCommand(t, line); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
			t.Errorf("`praetorctl %s` probe = %v, want an undefined-flag error", line, err)
		}
	}
}

// Boundary: the scan finds the commands the conditional steps add, so the positive test cannot
// pass on an epic that names none.
func TestEpicCommands_Boundary_ScanFindsConditionalCommands(t *testing.T) {
	commands := epicCommands(t, signalledEpic(t))
	for _, want := range []string{"ci filter", "gate run --path=.", "sync --remote", "gate keygen", "gate verify", "paperclip harness", "audit"} {
		if !slices.Contains(commands, want) {
			t.Errorf("epic commands %q lack %q", commands, want)
		}
	}
}
