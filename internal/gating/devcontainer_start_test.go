// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Positive: a container that starts slowly but within the start bound is entered, and the go
// probe is the run that waited for it (#1260).
func TestDevcontainerSlowStartWithinTheBoundIsEntered(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go")
	runtime.startDelay = 200 * time.Millisecond
	cfg := devcontainerConfig(t, repo, runtime)
	cfg.containerStart = 10 * time.Second
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("a start within the bound must enter the image: %v", err)
	}
	if cfg.container == nil || len(runtime.inside("sh")) != 1 {
		t.Fatalf("the image was not entered after one probe: container %v, probes %d", cfg.container, len(runtime.inside("sh")))
	}
}

// Negative: a container that does not start within the bound rejects the run with a message
// naming the container start and its bound, not a missing or broken go, and releases the run tag.
func TestDevcontainerStartBeyondTheBoundSaysTheContainerDidNotStart(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go")
	runtime.startDelay = 5 * time.Second
	cfg := devcontainerConfig(t, repo, runtime)
	cfg.containerStart = 50 * time.Millisecond
	err := resolveExecution(t.Context(), cfg)
	if err == nil {
		t.Fatal("a container that did not start within the bound was entered")
	}
	message := err.Error()
	for _, want := range []string{"did not start within 50ms", "does not mean go is missing", fakeImageID} {
		if !strings.Contains(message, want) {
			t.Errorf("message %q does not name %q", message, want)
		}
	}
	if strings.Contains(message, "probing image") {
		t.Errorf("a start cut by its bound must not read as a failed go probe: %q", message)
	}
	if cfg.container != nil || len(runtime.released) != 1 {
		t.Errorf("an image not entered must release its run tag: container %v, released %q", cfg.container, runtime.released)
	}
}

// Negative: a probe that fails for another reason within the bound keeps the go probe message.
func TestDevcontainerProbeFailureWithinTheBoundKeepsItsMessage(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go")
	runtime.failWhich["go"] = context.Canceled
	cfg := devcontainerConfig(t, repo, runtime)
	err := resolveExecution(t.Context(), cfg)
	if err == nil || !strings.Contains(err.Error(), "probing image") || strings.Contains(err.Error(), "did not start within") {
		t.Fatalf("a probe failure before the bound must keep the probe message, got %v", err)
	}
}

// Boundary: the start bound is the run budget's allowance for the stages other than the tests,
// far above the git query limit that refused pushes; it grows with the Cargo stages' allowance,
// and the probe of a run without a test override starts under it.
func TestDevcontainerStartBoundFollowsTheGateBudget(t *testing.T) {
	repo, resolved := devcontainerRepo(t)
	goBudget, err := stageBudget(t.Context(), resolved)
	if err != nil {
		t.Fatalf("resolve Go budget: %v", err)
	}
	goOnly := devcontainerStartBound(goBudget)
	if goOnly != OtherStagesAllowance || goOnly <= GitQueryTimeout {
		t.Fatalf("a Go repository's start bound is %s, want the %s allowance above the %s git query limit",
			goOnly, OtherStagesAllowance, GitQueryTimeout)
	}
	mixed := t.TempDir()
	writeFile(t, filepath.Join(mixed, "go.mod"), "module example.com/mixed\n")
	writeFile(t, filepath.Join(mixed, CargoLockFile), "version = 3\n")
	mixedBudget, err := stageBudget(t.Context(), mixed)
	if err != nil {
		t.Fatalf("resolve mixed budget: %v", err)
	}
	if got, want := devcontainerStartBound(mixedBudget), OtherStagesAllowance+CargoStagesAllowance; got != want {
		t.Fatalf("with a Cargo.lock the start bound is %s, want %s", got, want)
	}

	runtime := newFakeRuntime("go")
	cfg := devcontainerConfig(t, repo, runtime)
	before := time.Now()
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(runtime.runDeadlines) != 1 {
		t.Fatalf("want one container run, got %d", len(runtime.runDeadlines))
	}
	deadline := runtime.runDeadlines[0]
	if deadline.Before(before.Add(goOnly)) || deadline.After(time.Now().Add(goOnly)) {
		t.Errorf("the probe ran under the deadline %s, want %s after it started", deadline.Sub(before), goOnly)
	}

	short, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	runtime = newFakeRuntime("go")
	cfg = devcontainerConfig(t, repo, runtime)
	if err := resolveExecution(short, cfg); err != nil {
		t.Fatalf("resolve under a run deadline: %v", err)
	}
	if runDeadline, _ := short.Deadline(); !runtime.runDeadlines[0].Equal(runDeadline) {
		t.Errorf("a run deadline sooner than the start bound must bound the probe: %s, want %s",
			runtime.runDeadlines[0], runDeadline)
	}
}
