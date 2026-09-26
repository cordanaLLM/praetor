// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// maxPersonaFiles bounds the canonical persona scan (HISS-02).
const maxPersonaFiles = 64

// dispatchGateCommand runs a `... gate <args>` command line through the real `gate` dispatcher,
// starting after the word "gate". It reports false when the line names no gate subcommand.
func dispatchGateCommand(t *testing.T, line string) (bool, error) {
	t.Helper()
	fields := strings.Fields(line)
	for i := 0; i < len(fields)-1; i++ {
		if fields[i] == "gate" {
			_, err := captureStdout(t, func() error { return dispatchCommand("gate", fields[i+1:]) })
			return true, err
		}
	}
	return false, nil
}

// Positive: the command praetor generates for personas and task bodies parses against the real
// `gate run` flag set and reaches the pipeline aimed at the working directory, with and without
// --dry-run. The generated copies named --target=., an undefined flag, so every one of them
// exited before the pipeline started (BUG-298).
func TestRepoRunCommand_Positive_ParsesAgainstGateRun(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		call := recordPipeline(t)
		line := gating.RepoRunCommand
		if dryRun {
			line += " --dry-run"
		}
		ok, err := dispatchGateCommand(t, line)
		if !ok || err != nil {
			t.Fatalf("%q: dispatched=%v err=%v", line, ok, err)
		}
		if !call.called || call.repoDir != "." || call.dryRun != dryRun {
			t.Fatalf("%q: pipeline call %+v, want repoDir \".\" dryRun %v", line, *call, dryRun)
		}
	}
}

// Negative: the flag the generated personas used is undefined, and the pipeline never starts.
// This is the failure the constant exists to keep out of generated text.
func TestRepoRunCommand_Negative_TargetFlagIsUndefined(t *testing.T) {
	call := recordPipeline(t)
	_, err := dispatchGateCommand(t, "praetorctl gate run --target=. --dry-run")
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -target") {
		t.Fatalf("--target must be rejected as undefined, got %v", err)
	}
	if call.called {
		t.Fatal("a rejected flag must not start the pipeline")
	}
	if strings.Contains(gating.RepoRunCommand, "--target") {
		t.Fatalf("RepoRunCommand %q names the undefined --target flag", gating.RepoRunCommand)
	}
}

// Boundary: canonical personas are Markdown and cannot reference the Go constant, so every
// `gate run` command line they carry is parsed as written. The scan must find at least one line,
// or a green run proves nothing.
func TestCanonicalPersonaGateCommands_Boundary_ParseAgainstGateRun(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", ".agents", "agents", "*.md"))
	if err != nil {
		t.Fatalf("glob personas: %v", err)
	}
	if len(paths) > maxPersonaFiles {
		t.Fatalf("%d personas exceed the scan bound %d", len(paths), maxPersonaFiles)
	}
	checked := 0
	for i := 0; i < len(paths); i++ {
		data, readErr := os.ReadFile(paths[i])
		if readErr != nil {
			t.Fatalf("read %s: %v", paths[i], readErr)
		}
		checked += checkPersonaGateCommands(t, paths[i], string(data))
	}
	if checked == 0 {
		t.Fatal("no canonical persona carries a `gate run` command; the scan proves nothing")
	}
}

// checkPersonaGateCommands parses each `gate run` command line in one persona and returns how
// many it checked.
func checkPersonaGateCommands(t *testing.T, path, text string) int {
	t.Helper()
	lines := strings.Split(text, "\n")
	checked := 0
	for i := 0; i < len(lines); i++ {
		if !strings.Contains(lines[i], " gate run --") {
			continue
		}
		line := strings.TrimSpace(strings.ReplaceAll(lines[i], "`", ""))
		recordPipeline(t)
		ok, err := dispatchGateCommand(t, line)
		if !ok || err != nil {
			t.Errorf("%s:%d %q: dispatched=%v err=%v", path, i+1, line, ok, err)
		}
		checked++
	}
	return checked
}
