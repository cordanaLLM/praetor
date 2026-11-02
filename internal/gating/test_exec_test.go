// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sandbox"
)

// This fails on the original host-network command, before any namespace is needed.
func TestGoRaceLoopbackExec(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	dir := newGoModuleDir(t)
	cfg, recorded := newTestConfig(t, dir, false)
	cfg.goos = "linux"
	msg, err := runGoRaceCommand(t.Context(), cfg, goModule{file: "go.mod", dir: dir}, dir, time.Minute, raceScopeConfig{})
	if err != nil {
		t.Fatal(err)
	}
	runs := testInvocations(*recorded)
	if len(runs) != 1 || !slices.Contains(runs[0].args, "-exec") || !strings.Contains(msg, "loopback-only network namespace") {
		t.Fatalf("test binaries retain host loopback: commands=%+v message=%q", runs, msg)
	}
}

func TestGoTestNamespacePolicy(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	for _, goos := range []string{"darwin", "windows"} {
		execFlag, note, err := GoTestExec(goos, "/engine")
		if err != nil || execFlag != "" || !strings.Contains(note, goos+" has no Linux network namespaces") {
			t.Fatalf("%s: exec=%q note=%q error=%v", goos, execFlag, note, err)
		}
	}
}

func TestGoTestNamespaceCI(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	for _, variable := range []string{"CI", "GITHUB_ACTIONS"} {
		t.Run(variable, func(t *testing.T) {
			t.Setenv(variable, "true")
			execFlag, note, err := GoTestExec("linux", "/engine")
			if err != nil || execFlag != "" || !strings.Contains(note, "CI execution unchanged") {
				t.Fatalf("exec=%q note=%q error=%v", execFlag, note, err)
			}
		})
	}
}

func TestGoTestNamespaceWrapperPaths(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	for _, executable := range []string{"/space here/engine", "/single'quote/engine", "/double\"quote/engine"} {
		execFlag, _, err := GoTestExec("linux", executable)
		if err != nil || !strings.Contains(execFlag, executable) || !strings.HasSuffix(execFlag, " go test-exec") {
			t.Fatalf("exec=%q error=%v", execFlag, err)
		}
	}
	for _, executable := range []string{"", "/both'\"quotes"} {
		if _, _, err := GoTestExec("linux", executable); err == nil {
			t.Fatalf("accepted unusable wrapper path %q", executable)
		}
	}
}

func TestGoRaceNamespaceUnavailableNeverFallsBack(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	dir := newGoModuleDir(t)
	cfg, _ := newTestConfig(t, dir, false)
	cfg.goos = "linux"
	denied := &sandbox.UnavailableError{Facility: "bwrap user/network namespaces (loopback-only)", Cause: errors.New("planted EPERM")}
	var calls []recordedCommand
	base := fakeRunner(&calls, "", nil)
	cfg.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		out, err := base(ctx, dir, name, args...)
		if len(args) > 0 && args[0] == "test" {
			return denied.Error(), denied
		}
		return out, err
	}
	_, err := runGoRaceCommand(t.Context(), cfg, goModule{file: "go.mod"}, dir, time.Minute, raceScopeConfig{})
	var unavailable *sandbox.UnavailableError
	if !errors.As(err, &unavailable) || !strings.Contains(err.Error(), "planted EPERM") || len(testInvocations(calls)) != 1 {
		t.Fatalf("namespace failure hidden or retried: error=%v calls=%+v", err, calls)
	}
}

func TestGoRaceNamespaceExecInsideScope(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	dir := newGoModuleDir(t)
	cfg, recorded := newTestConfig(t, dir, false)
	cfg.goos = "linux"
	limits, err := resolveScopeLimits()
	if err != nil {
		t.Fatal(err)
	}
	_, err = runGoRaceCommand(t.Context(), cfg, goModule{file: "go.mod"}, dir, time.Minute, raceScopeConfig{useScope: true, limits: limits})
	if err != nil {
		t.Fatal(err)
	}
	last := (*recorded)[len(*recorded)-1]
	if last.name != "systemd-run" || !slices.Contains(last.args, "-exec") || !slices.Contains(last.args, "--expand-environment=no") {
		t.Fatalf("scope lost binary isolation: %+v", last)
	}
}

func TestGoNamespaceExecMountedInDevcontainer(t *testing.T) {
	t.Setenv("CI", "")
	t.Setenv("GITHUB_ACTIONS", "")
	cfg := &stageConfig{goos: "linux", container: &devcontainerExec{mounts: []string{"/repo"}}}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		execFlag, _, err := cfg.goTestExec()
		if err != nil || !strings.Contains(execFlag, executable) {
			t.Fatalf("exec=%q error=%v", execFlag, err)
		}
	}
	if !slices.Equal(cfg.container.mounts, []string{"/repo", executable}) {
		t.Fatalf("wrapper mount missing or duplicated: %q", cfg.container.mounts)
	}
}
