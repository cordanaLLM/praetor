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
		{"raised", "10m", 10 * time.Minute, "raised"},
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
	recorded := &[]recordedCommand{}
	// Stand in for a suite the bound cuts off: the runner reports the child's death after
	// its context is already past the deadline, exactly as an exec-killed `go test` does.
	timedOut.run = func(runCtx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "test" {
			<-runCtx.Done()
			return "ok github.com/example/pkg 1.4s", errors.New("signal: killed")
		}
		return fakeRunner(recorded, "", nil)(runCtx, dir, name, args...)
	}

	t.Setenv(TestStageTimeoutEnv, "50ms")
	_, err := runTestStage(ctx, timedOut)
	if err == nil {
		t.Fatal("a stage cut off by its bound must fail")
	}
	message := err.Error()
	if strings.Contains(message, "tests failed") {
		t.Errorf("a deadline must not be reported as a test failure, got %q", message)
	}
	for _, want := range []string{"bound firing", TestStageTimeoutEnv, "50ms"} {
		if !strings.Contains(message, want) {
			t.Errorf("deadline message must mention %q, got %q", want, message)
		}
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
	failing.run = fakeRunner(&[]recordedCommand{}, "FAIL example.test", errors.New("exit status 1"))

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
