// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
)

// goEnvRunner answers `go env <name>` from a table and fails any other command, so a
// test cannot pass by accident on a host that happens to have a real toolchain.
func goEnvRunner(values map[string]string) commandRunner {
	return func(_ context.Context, _, name string, args ...string) (string, error) {
		if name != "go" || len(args) != 2 || args[0] != "env" {
			return "", errors.New("unexpected command: " + name + " " + strings.Join(args, " "))
		}
		value, ok := values[args[1]]
		if !ok {
			return "", errors.New("unexpected go env variable: " + args[1])
		}
		return value + "\n", nil
	}
}

func TestRaceDetectorAvailableWhenCgoAndCompilerResolve(t *testing.T) {
	t.Setenv("CGO_ENABLED", "")
	cfg := &stageConfig{
		run:      goEnvRunner(map[string]string{"CGO_ENABLED": "1", "CC": "gcc"}),
		lookPath: func(string) (string, error) { return "/usr/bin/gcc", nil },
	}
	available, reason := raceDetectorAvailable(t.Context(), cfg)
	if !available || reason != "" {
		t.Fatalf("race detector reported unavailable: %q", reason)
	}
}

// The case that made the gate unusable: the toolchain claims cgo and names a compiler
// that is not installed. A stock Windows Go reports exactly this, so checking
// CGO_ENABLED alone would answer "available" and the stage would fail with
// `# runtime/cgo` and every package unbuildable.
func TestRaceDetectorUnavailableWhenNamedCompilerIsAbsent(t *testing.T) {
	t.Setenv("CGO_ENABLED", "")
	cfg := &stageConfig{
		run:      goEnvRunner(map[string]string{"CGO_ENABLED": "1", "CC": "gcc"}),
		lookPath: func(string) (string, error) { return "", os.ErrNotExist },
	}
	available, reason := raceDetectorAvailable(t.Context(), cfg)
	if available {
		t.Fatal("race detector reported available with no compiler on PATH")
	}
	if !strings.Contains(reason, "gcc") || !strings.Contains(reason, "not on PATH") {
		t.Fatalf("reason does not name the absent compiler: %q", reason)
	}
}

func TestRaceDetectorUnavailableWhenCgoIsDisabled(t *testing.T) {
	t.Setenv("CGO_ENABLED", "0")
	cfg := &stageConfig{
		run:      goEnvRunner(map[string]string{"CGO_ENABLED": "0", "CC": "gcc"}),
		lookPath: func(string) (string, error) { return "/usr/bin/gcc", nil },
	}
	available, reason := raceDetectorAvailable(t.Context(), cfg)
	if available {
		t.Fatal("race detector reported available with CGO_ENABLED=0")
	}
	if !strings.Contains(reason, "CGO_ENABLED=0") {
		t.Fatalf("reason does not name the disabled cgo: %q", reason)
	}
}

// Boundary: a skip must state its reason. An empty reason would be the silent
// non-run HISS-21 forbids, and would read in the report as an unexplained pass.
func TestRaceDetectorSkipAlwaysStatesAReason(t *testing.T) {
	t.Setenv("CGO_ENABLED", "")
	for name, cfg := range map[string]*stageConfig{
		"no compiler": {
			run:      goEnvRunner(map[string]string{"CGO_ENABLED": "1", "CC": "gcc"}),
			lookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
		"no CC named": {
			run:      goEnvRunner(map[string]string{"CGO_ENABLED": "1", "CC": ""}),
			lookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
		"go env unreadable": {
			run: func(context.Context, string, string, ...string) (string, error) {
				return "", errors.New("go missing")
			},
			lookPath: func(string) (string, error) { return "", os.ErrNotExist },
		},
	} {
		t.Run(name, func(t *testing.T) {
			available, reason := raceDetectorAvailable(t.Context(), cfg)
			if available {
				t.Fatal("expected unavailable")
			}
			if strings.TrimSpace(reason) == "" {
				t.Fatal("skip reported without a reason")
			}
		})
	}
}
