// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"
)

// Order engine-gate-private-gocache-20261025: deterministic concurrent-prune replay.
const gateCacheMiss = "open /home/test/.cache/go-build/ab/entry-a: no such file or directory"

func TestGoToolchainCacheMiss(t *testing.T) {
	for _, command := range []string{"vet", "build", "list"} {
		t.Run(command, func(t *testing.T) {
			for _, failures := range []int{0, 1, 2} {
				checkToolchainCacheRetry(t, command, failures)
			}
		})
	}
}

func checkToolchainCacheRetry(t *testing.T, command string, failures int) {
	t.Helper()
	dir := t.TempDir()
	cfg, _ := newTestConfig(t, dir, false)
	var log bytes.Buffer
	cfg.admissionLog = &log
	calls := 0
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cfg.run = toolchainCacheRunner(t, ctx, dir, command, failures, &calls)
	out, err := cfg.goToolchain().run(ctx, dir, "go", command, "./...")
	wantCalls := 1
	wantNotes := 0
	if failures > 0 {
		wantCalls, wantNotes = 2, 1
	}
	if calls != wantCalls || (err != nil) != (failures == 2) ||
		strings.Count(log.String(), "go-build cache miss, retried once") != wantNotes {
		t.Fatalf("%s failures=%d calls=%d err=%v log=%q", command, failures, calls, err, log.String())
	}
	if err == nil && out != "second result" {
		t.Fatalf("output=%q", out)
	}
	t.Logf("%s failures=%d calls=%d error=%t notes=%d", command, failures, calls, err != nil, wantNotes)
}

func toolchainCacheRunner(t *testing.T, ctx context.Context, dir, command string, failures int, calls *int) commandRunner {
	t.Helper()
	return func(gotCtx context.Context, gotDir, name string, args ...string) (string, error) {
		*calls++
		if gotCtx != ctx || gotDir != dir || name != "go" || !slices.Equal(args, []string{command, "./..."}) {
			t.Fatalf("retry changed command: %s %s %v", gotDir, name, args)
		}
		if *calls <= failures {
			return gateCacheMiss, errors.New("exit status 1")
		}
		return "second result", nil
	}
}

func TestGoSecurityCacheMiss(t *testing.T) {
	for _, command := range []string{"go", "govulncheck"} {
		t.Run(command, func(t *testing.T) {
			checkSecurityStageCacheRetry(t, command)
		})
	}
}

func checkSecurityStageCacheRetry(t *testing.T, command string) {
	t.Helper()
	dir := securityModule(t, "")
	cfg, _ := newTestConfig(t, dir, false)
	var log bytes.Buffer
	cfg.admissionLog = &log
	base := govulnScanner(t, dir, "module", nil)
	calls := 0
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == command {
			calls++
			if calls == 1 {
				return gateCacheMiss, errors.New("exit status 1")
			}
		}
		if name == "govulncheck" {
			return cleanGovulnStream, nil
		}
		return base(ctx, dir, name, args...)
	}
	if _, err := runGoSecurityModule(t.Context(), cfg, goModule{file: "go.mod", dir: dir}); err != nil || calls != 2 {
		t.Fatalf("calls=%d err=%v log=%q", calls, err, log.String())
	}
	if strings.Count(log.String(), "go-build cache miss, retried once") != 1 {
		t.Fatalf("log=%q", log.String())
	}
}

func TestGoToolchainCacheMissNetwork(t *testing.T) {
	dir := t.TempDir()
	cfg, _ := newTestConfig(t, dir, false)
	var log bytes.Buffer
	cfg.admissionLog = &log
	failure := errors.New("network unavailable")
	calls := 0
	cfg.run = func(context.Context, string, string, ...string) (string, error) {
		calls++
		return "partial", failure
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	for _, name := range []string{"vet", "build", "list"} {
		out, err := cfg.goToolchain().run(ctx, dir, "go", name, "./...")
		if !errors.Is(err, failure) || out != "partial" {
			t.Fatalf("%s result changed: %q %v", name, out, err)
		}
	}
	if calls != 3 || log.Len() != 0 {
		t.Fatalf("calls=%d log=%q", calls, log.String())
	}
}

func TestGoToolchainCacheMissInContainer(t *testing.T) {
	repo, _ := devcontainerRepo(t)
	runtime := newFakeRuntime("go", "gcc")
	runtime.failTest = errors.New(gateCacheMiss)
	cfg := devcontainerConfig(t, repo, runtime)
	var log bytes.Buffer
	cfg.admissionLog = &log
	if err := resolveExecution(t.Context(), cfg); err != nil {
		t.Fatal(err)
	}
	base := cfg.run
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		out, err := base(ctx, dir, name, args...)
		if err != nil && strings.Contains(err.Error(), gateCacheMiss) {
			runtime.failTest = nil
		}
		return out, err
	}
	before := len(runtime.inside("go"))
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if _, err := cfg.goToolchain().run(ctx, repo, "go", "test", "./..."); err != nil {
		t.Fatalf("container cache retry: %v", err)
	}
	if calls := len(runtime.inside("go")) - before; calls != 2 ||
		strings.Count(log.String(), "go-build cache miss, retried once") != 1 {
		t.Fatalf("calls=%d log=%q", calls, log.String())
	}
}
