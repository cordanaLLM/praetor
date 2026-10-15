// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// cargoCall is one cargo invocation runCargoHook made.
type cargoCall struct{ dir, args string }

// recordingCargo records each cargo invocation and fails the ones run in a directory of failIn.
func recordingCargo(calls *[]cargoCall, failIn ...string) func(context.Context, string, string, ...string) (string, error) {
	return func(_ context.Context, dir, name string, args ...string) (string, error) {
		*calls = append(*calls, cargoCall{dir: filepath.ToSlash(dir), args: name + " " + strings.Join(args, " ")})
		if slices.Contains(failIn, filepath.Base(dir)) {
			return "warning: unused variable", errors.New("exit status 101")
		}
		return "", nil
	}
}

// The Rust pre-commit jobs run through `praetorctl cargo fmt|clippy`, once in every tracked Cargo
// workspace (#1102): a workspace in worker/ got neither, because the job ran only where the
// repository root held a Cargo.toml.
func TestCargoHook_3D(t *testing.T) {
	repo := t.TempDir()
	writeFixtureFile(t, repo, "worker/Cargo.toml", "[workspace]\nmembers = [\"crates/*\"]\n")
	writeFixtureFile(t, repo, "worker/crates/a/Cargo.toml", "[package]\nname = \"a\"\n")
	writeFixtureFile(t, repo, "api/Cargo.toml", "[package]\nname = \"api\"\n")
	initGitFixture(t, repo)

	// Positive: each workspace once, a member never on its own, with the gate's own arguments.
	for tool, args := range map[string]string{
		"fmt":    "cargo fmt --all --check",
		"clippy": "cargo clippy --workspace --all-targets -- -D warnings",
	} {
		var calls []cargoCall
		var out strings.Builder
		if err := runCargoHook(t.Context(), repo, tool, recordingCargo(&calls), &out); err != nil {
			t.Fatalf("cargo %s: %v", tool, err)
		}
		want := []cargoCall{{filepath.ToSlash(filepath.Join(repo, "api")), args}, {filepath.ToSlash(filepath.Join(repo, "worker")), args}}
		if !slices.Equal(calls, want) {
			t.Errorf("cargo %s calls = %+v, want %+v", tool, calls, want)
		}
		if !strings.Contains(out.String(), "cargo "+tool+" worker/Cargo.toml: passed") {
			t.Errorf("cargo %s output = %q, want worker named", tool, out.String())
		}
	}

	// Boundary: one failing workspace fails the job and names its manifest; the others still run.
	var calls []cargoCall
	err := runCargoHook(t.Context(), repo, "clippy", recordingCargo(&calls, "api"), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "api/Cargo.toml") || len(calls) != 2 {
		t.Errorf("failing api workspace: err %v after %d calls, want api/Cargo.toml named after 2", err, len(calls))
	}

	// Negative: no tracked workspace runs no cargo and says why; an unknown tool is refused.
	empty := t.TempDir()
	writeFixtureFile(t, empty, "README.md", "x\n")
	initGitFixture(t, empty)
	calls = nil
	var out strings.Builder
	if err := runCargoHook(t.Context(), empty, "fmt", recordingCargo(&calls), &out); err != nil || len(calls) != 0 ||
		!strings.Contains(out.String(), "cargo fmt: no tracked Cargo workspaces, skipping") {
		t.Errorf("no workspace: err %v, calls %+v, output %q", err, calls, out.String())
	}
	if err := runCargo([]string{"build"}); err == nil {
		t.Error("praetorctl cargo build was accepted")
	}
}
