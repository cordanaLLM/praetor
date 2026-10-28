// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

func TestScaledSyncProbeTimeout_3D(t *testing.T) {
	for _, tc := range []struct {
		name    string
		entries int
		want    time.Duration
	}{
		{"empty", 0, 5 * time.Second},
		{"below increment", 999, 5 * time.Second},
		{"first increment", 1_000, 6 * time.Second},
		{"large fixture", 10_052, 15 * time.Second},
		{"below ceiling", 54_999, 59 * time.Second},
		{"at ceiling", 55_000, 60 * time.Second},
		{"over ceiling", 56_000, 60 * time.Second},
		{"huge count", math.MaxInt, 60 * time.Second},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := scaledSyncProbeTimeout(t.Context(), tc.entries); got != tc.want {
				t.Fatalf("%d entries: bound %s, want %s", tc.entries, got, tc.want)
			}
		})
	}
	t.Run("parent deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		if got := scaledSyncProbeTimeout(ctx, 10_052); got <= 0 || got > time.Second {
			t.Fatalf("parent deadline did not cap the bound: %s", got)
		}
	})
}

func TestScaledSyncProbeDeadline_3D(t *testing.T) {
	root := syncFixture(t)
	ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()
	for _, probe := range stateGitProbes("available") {
		t.Run(probe.label, func(t *testing.T) {
			_, err := runSyncProbe(ctx, root, probe, 10_052)
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("parent deadline lost: %v", err)
			}
			requireErrorContains(t, err, probe.label, "10052 index entries", "bound 1ns")
		})
	}
}

// A slow unstaged diff (the probe that ran out of time on a loaded macOS runner) exceeds the
// old fixed bound on every platform. Only that probe is slowed, so the case costs two six-second
// calls (binding and verification readback) instead of every tree probe.
func TestStateSyncSlowRunner_3D(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("slow-runner fixture needs the Go toolchain to build git: %v", err)
	}
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatalf("slow-runner fixture needs real git: %v", err)
	}
	root := syncFixture(t)
	stageIndexEntries(t, root, 10_050)
	testsupport.RunFixtureGit(t, root, []string{"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "bulk"})
	dir := t.TempDir()
	testsupport.BuildExecutable(t, dir, "git", fmt.Sprintf(slowRunnerGitSource, realGit))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	start := time.Now()
	if _, err := SyncState(t.Context(), root, "slow runner"); err != nil {
		t.Fatalf("10,052 entries with a six-second unstaged diff failed to sync: %v", err)
	}
	t.Logf("slow-runner sync and readback: %s", time.Since(start))
	t.Run("earlier caller deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
		defer cancel()
		_, err := runSyncProbe(ctx, root, stateGitProbes("available")[4], 10_052)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("slow unstaged diff ignored caller deadline: %v", err)
		}
		requireErrorContains(t, err, "unstaged diff", "10052 index entries", "bound")
	})
	t.Run("small index retains fixed bound", func(t *testing.T) {
		_, err := runSyncProbe(t.Context(), root, stateGitProbes("available")[4], 0)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("slow unstaged diff at zero entries: %v; want deadline", err)
		}
		requireErrorContains(t, err, "unstaged diff", "0 index entries", "bound 5s")
	})
}

// The real executable path is embedded before PATH changes; the probe environment
// deliberately drops arbitrary environment variables. No shell or platform skip.
const slowRunnerGitSource = `package main
import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"time"
)
func main() {
	diff, cached := false, false
	for i := 1; i < len(os.Args); i++ {
		diff = diff || os.Args[i] == "diff"
		cached = cached || os.Args[i] == "--cached"
	}
	if diff && !cached {
		time.Sleep(6 * time.Second)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, %q, os.Args[1:]...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
`
