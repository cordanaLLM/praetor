// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestTestStageTimeoutResolvesFromEnvironment(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		want     time.Duration
		wantNote string
	}{
		// Positive: nothing set keeps the shipped default and says nothing about it.
		{"unset", "", TestStageTimeout, ""},
		{"blank", "   ", TestStageTimeout, ""},
		// Positive: a usable value is honoured and announced, so a raised bound is visible
		// in the receipt rather than an invisible local difference.
		{"raised", "25m", 25 * time.Minute, "raised"},
		// Boundary: a value under the default, or equal to it, is honoured and announced as what
		// it is, not as a raise (#1419).
		{"lowered", "10m", 10 * time.Minute, "lowered"},
		{"at default", TestStageTimeout.String(), TestStageTimeout, "set to"},
		// Negative: an unusable value falls back rather than removing or shrinking the bound.
		{"unparseable", "soon", TestStageTimeout, "not a duration"},
		{"zero", "0s", TestStageTimeout, "not positive"},
		{"negative", "-5m", TestStageTimeout, "not positive"},
		// Boundary: the ceiling is inclusive, and one step past it clamps.
		{"at ceiling", "30m", MaxTestStageTimeout, "raised"},
		{"over ceiling", "31m", MaxTestStageTimeout, "clamped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, note := testStageTimeout(tc.raw)
			if got != tc.want {
				t.Errorf("bound for %q = %s, want %s", tc.raw, got, tc.want)
			}
			if tc.wantNote == "" {
				if note != "" {
					t.Errorf("bound for %q must be silent, got %q", tc.raw, note)
				}
				return
			}
			if !strings.Contains(note, tc.wantNote) {
				t.Errorf("note for %q = %q, want it to mention %q", tc.raw, note, tc.wantNote)
			}
		})
	}
}

// A stage killed by its own deadline is not a failing suite. Reporting it as one sends every
// reader to diagnose a change that was never the cause, which is what #100 documents happening.
func TestTestStageDistinguishesItsDeadlineFromAFailingSuite(t *testing.T) {
	ctx := context.Background()

	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	timedOut, _ := newTestConfig(t, repoDir, false)
	// Stand in for a suite the bound cuts off, with the bound's clock started as the suite
	// starts, so the real worktree creation before it cannot use the 50ms up.
	ran := false
	timedOut.run = blockedSuite(&ran, holdStageBound(timedOut))

	t.Setenv(TestStageTimeoutEnv, "50ms")
	_, err := runTestStage(ctx, timedOut)
	if err == nil || !ran {
		t.Fatalf("a stage cut off by its bound must fail after starting the suite: ran=%v err=%v", ran, err)
	}
	message := err.Error()
	if strings.Contains(message, "tests failed") {
		t.Errorf("a deadline must not be reported as a test failure, got %q", message)
	}
	for _, want := range []string{"race-detector tests", "bound firing", TestStageTimeoutEnv, "50ms"} {
		if !strings.Contains(message, want) {
			t.Errorf("deadline message must mention %q, got %q", want, message)
		}
	}
}

// Boundary (#1419): a suite that blocks forever is stopped at the shipped default bound and at a
// raised one, and both are reported as the bound firing with the bound's value, not as a failing
// suite. The bound's clock runs for a millisecond so the test does not wait out twenty minutes.
func TestTestStageCutsAHungSuiteAtTheDefaultAndARaisedBound(t *testing.T) {
	cases := []struct {
		name, raw string
		want      time.Duration
	}{
		{"default", "", TestStageTimeout},
		{"raised", "28m", 28 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repoDir := newHermeticGitRepo(t)
			seedGoModule(t, repoDir)
			cfg, _ := newTestConfig(t, repoDir, false)
			var seen time.Duration
			ran := false
			cfg.run = blockedSuite(&ran, holdStageBoundAt(cfg, time.Millisecond, &seen))
			t.Setenv(TestStageTimeoutEnv, tc.raw)
			_, err := runTestStage(context.Background(), cfg)
			if err == nil || !ran {
				t.Fatalf("a hung suite must be stopped after it started: ran=%v err=%v", ran, err)
			}
			if seen != tc.want {
				t.Errorf("the stage asked for a %s bound, want %s", seen, tc.want)
			}
			message := err.Error()
			if strings.Contains(message, "tests failed") {
				t.Errorf("a hung suite cut by the bound must not read as a test failure, got %q", message)
			}
			for _, want := range []string{"hit the " + tc.want.String() + " stage bound", "bound firing", TestStageTimeoutEnv} {
				if !strings.Contains(message, want) {
					t.Errorf("deadline message must mention %q, got %q", want, message)
				}
			}
		})
	}
}

// Boundary (#1419): the shipped default covers the slowest full race run measured on a loaded
// workstation, with half of it again to spare, and stays under the ceiling so an operator can
// still raise it. The old 3-minute default fails the first check.
func TestTestStageTimeoutDefaultCoversTheMeasuredRuns(t *testing.T) {
	// The engine's full race run at load 45 to 60 on 2026-10-17; adopters took 4 to 7 minutes.
	const slowestMeasured = 10*time.Minute + 5*time.Second
	if TestStageTimeout < slowestMeasured*3/2 {
		t.Errorf("default race stage bound %s is under 1.5 x the slowest measured run %s", TestStageTimeout, slowestMeasured)
	}
	if TestStageTimeout >= MaxTestStageTimeout {
		t.Errorf("default race stage bound %s leaves no room under the %s ceiling", TestStageTimeout, MaxTestStageTimeout)
	}
}

// The bound also covers creating the isolated worktree, and firing there is the same event. A
// one-nanosecond bound expires before git can start, so this reaches that path on every
// platform; the test above holds its bound back until the suite starts and never reaches it.
func TestTestStageAttributesABoundFiringDuringWorktreeCreation(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	testsRan := false
	cfg.run = func(runCtx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "test" {
			testsRan = true
		}
		return fakeRunner(&[]recordedCommand{}, "", nil)(runCtx, dir, name, args...)
	}

	t.Setenv(TestStageTimeoutEnv, "1ns")
	_, err := runTestStage(context.Background(), cfg)
	if err == nil {
		t.Fatal("a stage whose bound fired during worktree creation must fail")
	}
	message := err.Error()
	for _, want := range []string{"creating the isolated test worktree", "bound firing", TestStageTimeoutEnv, "1ns"} {
		if !strings.Contains(message, want) {
			t.Errorf("message must mention %q, got %q", want, message)
		}
	}
	if strings.Contains(message, "could not be created") || testsRan {
		t.Errorf("a bound firing was reported as a creation failure, or tests ran anyway: ran=%v %q", testsRan, message)
	}
}

// A raised bound must reach the receipt. A local override that changed the gate's strictness
// without appearing anywhere in its output would be an invisible difference between what one
// operator verified and what everyone else reads.
func TestTestStageSurfacesARaisedBoundAsItsReason(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)

	t.Setenv(TestStageTimeoutEnv, "12m")
	msg, err := runTestStage(context.Background(), cfg)
	if err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if !strings.Contains(msg, "12m") || !strings.Contains(msg, TestStageTimeoutEnv) {
		t.Errorf("a raised bound must be stated in the stage reason, got %q", msg)
	}
}

// Boundary against the test above: a suite that genuinely fails inside the bound must still
// read as a test failure, or the fix would trade one wrong diagnosis for another.
func TestTestStageStillReportsAGenuineFailure(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	failing, _ := newTestConfig(t, repoDir, false)
	failing.run = func(ctx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) == 5 && args[0] == "list" && args[1] == "-find" {
			return "example.test/pkg\n", nil
		}
		return fakeRunner(&[]recordedCommand{}, "FAIL example.test", errors.New("exit status 1"))(ctx, dir, name, args...)
	}

	_, err := runTestStage(context.Background(), failing)
	if err == nil {
		t.Fatal("a failing suite must fail the stage")
	}
	if !strings.Contains(err.Error(), "tests failed") {
		t.Errorf("a genuine failure must read as one, got %q", err)
	}
	if strings.Contains(err.Error(), "bound firing") {
		t.Errorf("a genuine failure must not be blamed on the bound, got %q", err)
	}
}
