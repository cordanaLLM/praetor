// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// goTelemetryMode asks the go command which telemetry mode it reads with home as HOME and no
// XDG_CONFIG_HOME, the environment the devcontainer gives it.
func goTelemetryMode(t *testing.T, home string) string {
	t.Helper()
	goBin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx, err = util.WithCommandEnvironment(ctx, []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home, "GOTOOLCHAIN=local"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := util.RunCommand(ctx, home, goBin, "env", "GOTELEMETRY")
	if err != nil {
		t.Fatalf("go env GOTELEMETRY: %v", err)
	}
	return out
}

// Positive (#1429): the go command reads mode local from a fresh HOME, so the Go tools of every
// gate run would write the shared counter files; once the devcontainer home records off, the
// same go command reports off.
func TestDisableContainerTelemetry_Positive_GoReadsOff(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("os.UserConfigDir is HOME/.config only on Linux, the container's platform")
	}
	home := t.TempDir()
	if got := goTelemetryMode(t, home); got != "local" {
		t.Fatalf("a fresh HOME reads telemetry %q, want the default local", got)
	}
	if err := disableContainerTelemetry(home); err != nil {
		t.Fatal(err)
	}
	if got := goTelemetryMode(t, home); got != "off" {
		t.Fatalf("after disableContainerTelemetry go reads telemetry %q, want off", got)
	}
}

// Negative: a mode file holding another mode, local or on, with or without its date, is
// rewritten to off.
func TestDisableContainerTelemetry_Negative_OtherModeRewritten(t *testing.T) {
	for _, text := range []string{"local", "on 2026-01-01\n", "local 2026-10-17"} {
		home := t.TempDir()
		modeFile := filepath.Join(home, filepath.FromSlash(containerTelemetryModeRel))
		if err := os.MkdirAll(filepath.Dir(modeFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(modeFile, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := disableContainerTelemetry(home); err != nil {
			t.Fatalf("%q: %v", text, err)
		}
		if got, err := os.ReadFile(modeFile); err != nil || string(got) != "off\n" {
			t.Fatalf("%q: mode file %q, %v; want off", text, got, err)
		}
	}
}

// Boundary: a file that already says off, with its date, keeps its bytes; a mode path that
// cannot be written fails the home's preparation, naming telemetry, instead of running the
// gate with the shared counters.
func TestDisableContainerTelemetry_Boundary_KeptOffAndUnwritable(t *testing.T) {
	home := t.TempDir()
	modeFile := filepath.Join(home, filepath.FromSlash(containerTelemetryModeRel))
	if err := os.MkdirAll(filepath.Dir(modeFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(modeFile, []byte("off 2026-10-17"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := disableContainerTelemetry(home); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(modeFile); err != nil || string(got) != "off 2026-10-17" {
		t.Fatalf("an off mode file was rewritten: %q, %v", got, err)
	}
	blocked := t.TempDir()
	if err := os.WriteFile(filepath.Join(blocked, ".config"), []byte("a file, not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := disableContainerTelemetry(blocked); err == nil {
		t.Fatal("an unwritable telemetry directory was accepted")
	}
}
