//go:build apicompatgate

// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type mockTracker struct {
	downloads int
	lists     int
	failCount int
	failErr   error
}

func (m *mockTracker) run(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
	if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
		m.downloads++
		if m.downloads <= m.failCount {
			return m.failErr
		}
		return nil
	}
	if len(args) >= 1 && args[0] == "list" {
		m.lists++
		return nil
	}
	return nil
}

// Positive: first download fails then succeeds on attempt 2, list runs once, one wait of 5 s.
func TestBuildModule_Positive_FirstDownloadFailsThenSucceeds(t *testing.T) {
	t.Parallel()
	tracker := &mockTracker{
		failCount: 1,
		failErr:   errors.New("simulated proxy stream INTERNAL_ERROR"),
	}
	var (
		waits  []time.Duration
		stderr strings.Builder
	)
	wait := func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	err := buildModule(t.Context(), "/mock/root", "my/mod", &stderr, tracker.run, wait)
	if err != nil {
		t.Fatalf("buildModule failed: %v", err)
	}
	if tracker.downloads != 2 || tracker.lists != 1 {
		t.Errorf("downloads = %d, lists = %d, want 2 and 1", tracker.downloads, tracker.lists)
	}
	if !slices.Equal(waits, []time.Duration{5 * time.Second}) {
		t.Errorf("waits = %v, want [5s]", waits)
	}
	wantMsg := "go mod download failed for my/mod (attempt 1 of 3): simulated proxy stream INTERNAL_ERROR"
	if !strings.Contains(stderr.String(), wantMsg) {
		t.Errorf("stderr missing expected attempt 1 line:\n%s", stderr.String())
	}
}

// Negative: every download fails, error after 3 attempts naming module, list never runs.
func TestBuildModule_Negative_EveryDownloadFails(t *testing.T) {
	t.Parallel()
	tracker := &mockTracker{
		failCount: 3,
		failErr:   errors.New("persistent connection refused"),
	}
	var (
		waits  []time.Duration
		stderr strings.Builder
	)
	wait := func(ctx context.Context, d time.Duration) error {
		waits = append(waits, d)
		return nil
	}

	err := buildModule(t.Context(), "/mock/root", "pkg/badmod", &stderr, tracker.run, wait)
	if err == nil {
		t.Fatal("buildModule succeeded, want persistent download error")
	}
	if tracker.downloads != 3 || tracker.lists != 0 {
		t.Errorf("downloads = %d, lists = %d, want 3 and 0", tracker.downloads, tracker.lists)
	}
	wantWaits := []time.Duration{5 * time.Second, 15 * time.Second}
	if !slices.Equal(waits, wantWaits) {
		t.Errorf("waits = %v, want %v", waits, wantWaits)
	}
	if !strings.Contains(err.Error(), "pkg/badmod") || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error %q does not name module and 3 attempts", err.Error())
	}
	for _, attempt := range []string{"attempt 1 of 3", "attempt 2 of 3", "attempt 3 of 3"} {
		if !strings.Contains(stderr.String(), attempt) {
			t.Errorf("stderr missing %q:\n%s", attempt, stderr.String())
		}
	}
}

// Boundary: cancel during wait returns at once.
func TestBuildModule_Boundary_CancelDuringWaitReturnsAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	runner := func(runCtx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
			cancel()
			return errors.New("initial failure")
		}
		return nil
	}

	start := time.Now()
	err := buildModule(ctx, "/mock/root", "cancelmod", io.Discard, runner, defaultWait)
	elapsed := time.Since(start)

	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if elapsed >= 2*time.Second {
		t.Errorf("cancel took %v, expected immediate return without waiting full backoff", elapsed)
	}
}

// Negative: exception for module plus download failure still fails gate.
func TestGate_Negative_ExceptionForModulePlusDownloadFailureStillFailsGate(t *testing.T) {
	t.Parallel()
	repo := repository{
		root:  "/mock/root",
		today: time.Now().UTC(),
		exceptions: []exception{
			{Path: "excepted/go.mod", Reason: "needs libfoo", Expires: "2099-12-31"},
		},
		runner: func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
			if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
				return errors.New("proxy error")
			}
			return nil
		},
		wait: func(ctx context.Context, d time.Duration) error {
			return nil
		},
	}
	plan := modulePlan{
		compared: []module{
			{dir: "excepted", path: "example.com/excepted"},
		},
	}
	stdout := &printer{w: io.Discard}
	stderr := &printer{w: io.Discard}

	results, err := compareEach(t.Context(), repo, plan, "checker", checkerSites{}, nil, stdout, stderr)
	if err == nil {
		t.Fatalf("compareEach succeeded (results = %+v), want failure when download fails even with live exception", results)
	}
	if !strings.Contains(err.Error(), "excepted") || !strings.Contains(err.Error(), "3 attempts") {
		t.Errorf("error %q does not name excepted module and attempts", err.Error())
	}
}

// Boundary: base code without download is red against download retry requirement.
func TestBuildModule_Boundary_BaseCodeWithoutDownloadRed(t *testing.T) {
	t.Parallel()
	priorPath := filepath.Join("..", "testdata", "prior", "api-gate-main-before-modfetch-retry.go.txt")
	priorData, err := os.ReadFile(priorPath)
	if err != nil {
		t.Fatalf("read prior gate text: %v", err)
	}
	if strings.Contains(string(priorData), "go mod download") {
		t.Fatal("prior gate text unexpectedly contains go mod download")
	}

	currentData, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read current main.go: %v", err)
	}
	if !strings.Contains(string(currentData), "go mod download") {
		t.Fatal("current main.go must contain go mod download retry")
	}

	baseCodeBuildModule := func(ctx context.Context, root, dir string, stderr io.Writer, run toolRunner) error {
		moduleDir := filepath.Join(root, filepath.FromSlash(dir))
		return run(ctx, moduleDir, io.Discard, stderr, "go", "list", "-export", "./...")
	}

	var downloadRan bool
	mockRunner := func(ctx context.Context, dir string, stdout, errOut io.Writer, name string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "download" {
			downloadRan = true
		}
		return nil
	}

	if err := baseCodeBuildModule(t.Context(), "/mock/root", "mod", io.Discard, mockRunner); err != nil {
		t.Fatalf("baseCodeBuildModule failed: %v", err)
	}
	if downloadRan {
		t.Fatal("base code unexpectedly ran download")
	}
}
