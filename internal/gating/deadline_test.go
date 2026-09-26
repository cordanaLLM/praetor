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

// oldRunCap is the fixed whole-run deadline `gate run` applied before #314. Every bound above it
// was cut off at it, whatever PRAETOR_TEST_STAGE_TIMEOUT said.
const oldRunCap = 5 * time.Minute

func TestResolveRunBudgetDerivesTheRunDeadlineFromTheStageBound(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		bound    time.Duration
		wantNote string
	}{
		// Positive: a raised bound raises the run deadline with it, past the old fixed cap.
		{"raised past the old cap", "12m", 12 * time.Minute, "raised"},
		// Negative: an unusable value falls back to the default rather than removing the bound.
		{"unparseable", "soon", TestStageTimeout, "not a duration"},
		{"negative", "-5m", TestStageTimeout, "not positive"},
		// Boundary: unset keeps the default silently; the ceiling is inclusive; past it clamps.
		{"unset", "", TestStageTimeout, ""},
		{"exact ceiling", "30m", MaxTestStageTimeout, "raised"},
		{"over ceiling", "31m", MaxTestStageTimeout, "clamped"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			budget := ResolveRunBudget(tc.raw)
			if budget.StageBound != tc.bound || budget.Allowance != OtherStagesAllowance {
				t.Fatalf("budget for %q = %+v, want bound %s + allowance %s", tc.raw, budget, tc.bound, OtherStagesAllowance)
			}
			if got, want := budget.Timeout(), tc.bound+OtherStagesAllowance; got != want {
				t.Errorf("run deadline for %q = %s, want %s", tc.raw, got, want)
			}
			silentAsWanted := tc.wantNote != "" || budget.Note == ""
			if !silentAsWanted || !strings.Contains(budget.Note, tc.wantNote) {
				t.Errorf("note for %q = %q, want %q", tc.raw, budget.Note, tc.wantNote)
			}
		})
	}
	if got := ResolveRunBudget("30m").TimeoutSeconds(); got != int64((MaxTestStageTimeout + OtherStagesAllowance).Seconds()) {
		t.Errorf("the ceiling's run deadline must be exactly %s, got %ds", MaxTestStageTimeout+OtherStagesAllowance, got)
	}
}

// A subprocess bound derived from the deadline must never be shorter than the deadline itself, or
// the caller kills the gate before the gate can say which deadline fired.
func TestRunBudgetTimeoutSecondsRoundsUp(t *testing.T) {
	cases := []struct {
		budget RunBudget
		want   int64
	}{
		{RunBudget{StageBound: 180 * time.Second, Allowance: OtherStagesAllowance}, 480},
		{RunBudget{StageBound: 1500 * time.Millisecond}, 2},
		{RunBudget{StageBound: time.Nanosecond}, 1},
	}
	for _, tc := range cases {
		if got := tc.budget.TimeoutSeconds(); got != tc.want {
			t.Errorf("TimeoutSeconds(%s) = %d, want %d", tc.budget.Timeout(), got, tc.want)
		}
	}
}

func TestEnvRunBudgetReadsTheStageVariable(t *testing.T) {
	t.Setenv(TestStageTimeoutEnv, "12m")
	if got, want := EnvRunBudget(), ResolveRunBudget("12m"); got != want {
		t.Errorf("EnvRunBudget() = %+v, want %+v", got, want)
	}
	t.Setenv(TestStageTimeoutEnv, "")
	if got := EnvRunBudget().StageBound; got != TestStageTimeout {
		t.Errorf("unset variable must keep the %s default, got %s", TestStageTimeout, got)
	}
}

func TestWithRunDeadlineBoundsTheRunAndNamesItsCause(t *testing.T) {
	// Positive: the context's deadline is the budget's, and a raised bound lifts it past the old cap.
	budget := ResolveRunBudget("12m")
	ctx, cancel := WithRunDeadline(context.Background(), budget)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("a run context must carry a deadline (HISS-02)")
	}
	if left := time.Until(deadline); left <= budget.Timeout()-time.Minute || left > budget.Timeout() || left <= oldRunCap {
		t.Errorf("run deadline %s away, want about %s", left, budget.Timeout())
	}
	if cause := context.Cause(ctx); cause != nil {
		t.Errorf("a live run context has no cause, got %v", cause)
	}

	// Boundary: once it fires, the cause names the deadline and how it was composed.
	fired, cancelFired := WithRunDeadline(context.Background(), RunBudget{StageBound: time.Millisecond, Allowance: time.Millisecond})
	defer cancelFired()
	<-fired.Done()
	runDeadline, ok := errors.AsType[*RunDeadlineError](context.Cause(fired))
	if !ok {
		t.Fatalf("a fired run deadline must be its own cause, got %v", context.Cause(fired))
	}
	for _, want := range []string{"deadline of 2ms", "1ms race stage bound", "1ms for the other stages"} {
		if !strings.Contains(runDeadline.Error(), want) {
			t.Errorf("run deadline cause must mention %q, got %q", want, runDeadline)
		}
	}
}

// Positive, end to end through the stage: under a run deadline derived from a 12m bound the race
// stage gets its whole 12 minutes. The old five-minute run cap made any bound above it unreachable.
func TestTestStageHonoursABoundBeyondTheOldRunCap(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	var testDeadline time.Time
	cfg.run = func(runCtx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "test" {
			testDeadline, _ = runCtx.Deadline()
		}
		return fakeRunner(&[]recordedCommand{}, "", nil)(runCtx, dir, name, args...)
	}

	t.Setenv(TestStageTimeoutEnv, "12m")
	ctx, cancel := WithRunDeadline(context.Background(), EnvRunBudget())
	defer cancel()
	if _, err := runTestStage(ctx, cfg); err != nil {
		t.Fatalf("stage failed: %v", err)
	}
	if left := time.Until(testDeadline); left <= 11*time.Minute || left > 12*time.Minute {
		t.Errorf("race tests were given %s, want the full 12m bound", left)
	}
}

// blockedSuite stands in for a suite a deadline cuts off: it reports the child's death only once
// its context is done, exactly as an exec-killed `go test` does. A non-nil arm runs as the suite
// starts, which is where the tests start the clock of a deadline held back by holdDeadline.
func blockedSuite(ran *bool, arm func()) commandRunner {
	return func(runCtx context.Context, dir, name string, args ...string) (string, error) {
		if name == "go" && len(args) > 0 && args[0] == "test" {
			*ran = true
			if arm != nil {
				arm()
			}
			<-runCtx.Done()
			return "ok github.com/example/pkg 1.4s", errors.New("signal: killed")
		}
		return fakeRunner(&[]recordedCommand{}, "", nil)(runCtx, dir, name, args...)
	}
}

// deadlineFunc builds a deadline under parent, the way production builds the one under test.
type deadlineFunc func(parent context.Context) (context.Context, context.CancelFunc)

// holdDeadline returns a context under parent that start's deadline ends, but whose clock runs
// only from arm on; the context then ends with that deadline's own cause. Only when it fires is
// held back: the cause, and so the attribution under test, is the one production produces.
//
// A millisecond deadline armed at the call instead raced the real `git worktree add` before the
// suite, and on a loaded host fired there, failing a test about the suite (BUG-988). Every test
// here that means to cut the suite arms its deadline from blockedSuite.
func holdDeadline(parent context.Context, start deadlineFunc) (context.Context, context.CancelFunc, func()) {
	held, release := context.WithCancelCause(parent)
	stop := context.CancelFunc(func() {})
	arm := func() {
		deadline, stopDeadline := start(parent)
		stop = stopDeadline
		context.AfterFunc(deadline, func() { release(context.Cause(deadline)) })
	}
	cancel := func() {
		release(nil)
		stop()
	}
	return held, cancel, arm
}

// holdStageBound holds back the clock of cfg's race stage bound, which the stage itself creates
// before its worktree, and returns the arm that starts it.
func holdStageBound(cfg *stageConfig) func() {
	var arm func()
	cfg.boundStage = func(ctx context.Context, bound time.Duration) (context.Context, context.CancelFunc) {
		held, cancel, armBound := holdDeadline(ctx, func(parent context.Context) (context.Context, context.CancelFunc) {
			return withStageBound(parent, bound)
		})
		arm = armBound
		return held, cancel
	}
	return func() { arm() }
}

// Boundary for the helper every cut-suite test relies on: a held deadline does not fire before
// it is armed, fires after, and carries the deadline's own cause.
func TestHoldDeadlineStartsTheClockOnlyWhenArmed(t *testing.T) {
	budget := RunBudget{StageBound: time.Millisecond, Allowance: time.Millisecond}
	held, cancel, arm := holdDeadline(context.Background(), func(parent context.Context) (context.Context, context.CancelFunc) {
		return WithRunDeadline(parent, budget)
	})
	defer cancel()

	// Negative: long past the deadline's value, an unarmed hold is still live.
	time.Sleep(20 * budget.Timeout())
	if cause := context.Cause(held); cause != nil {
		t.Fatalf("an unarmed deadline must not fire, got %v", cause)
	}

	// Positive: once armed it fires with the run deadline's own cause.
	arm()
	<-held.Done()
	if cause, ok := errors.AsType[*RunDeadlineError](context.Cause(held)); !ok || cause.Budget != budget {
		t.Errorf("an armed hold must end with the run deadline's cause, got %v", context.Cause(held))
	}

	// Boundary: cancelling an armed hold that has fired keeps the deadline's cause.
	cancel()
	if cause, ok := errors.AsType[*RunDeadlineError](context.Cause(held)); !ok || cause.Budget != budget {
		t.Errorf("cancel after the firing must not replace its cause, got %v", context.Cause(held))
	}
}

// Negative: the run deadline firing during the race stage is reported as the run deadline, with
// its value, and not as the stage bound, which is what #314 observed: "hit the 30m0s stage bound"
// after 4m57s.
func TestTestStageBlamesTheRunDeadlineWhenItFiresFirst(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	budget := RunBudget{StageBound: 25 * time.Millisecond, Allowance: 25 * time.Millisecond}
	ctx, cancel, arm := holdDeadline(context.Background(), func(parent context.Context) (context.Context, context.CancelFunc) {
		return WithRunDeadline(parent, budget)
	})
	defer cancel()
	ran := false
	cfg.run = blockedSuite(&ran, arm)

	t.Setenv(TestStageTimeoutEnv, "10m")
	_, err := runTestStage(ctx, cfg)
	if err == nil || !ran {
		t.Fatalf("a stage cut off by the run deadline must fail after starting the suite: ran=%v err=%v", ran, err)
	}
	if cause, ok := errors.AsType[*RunDeadlineError](err); !ok || cause.Budget.Timeout() != 50*time.Millisecond {
		t.Errorf("the error must carry the 50ms run deadline as its cause, got %v", err)
	}
	message := err.Error()
	for _, want := range []string{"race-detector tests", "did not finish: the gate run's deadline of 50ms", "fired first, not the 10m0s stage bound", TestStageTimeoutEnv} {
		if !strings.Contains(message, want) {
			t.Errorf("message must mention %q, got %q", want, message)
		}
	}
	for _, wrong := range []string{"hit the 10m0s stage bound", "bound firing", "tests failed"} {
		if strings.Contains(message, wrong) {
			t.Errorf("a run deadline must not be reported as %q, got %q", wrong, message)
		}
	}
}

// Negative: a caller deadline that is not a gate run's still is not the stage bound. The
// gatekeeper agent and any library caller reach the pipeline with a plain deadline.
func TestTestStageBlamesAPlainCallerDeadlineOnTheCaller(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	ctx, cancel, arm := holdDeadline(context.Background(), func(parent context.Context) (context.Context, context.CancelFunc) {
		return context.WithTimeout(parent, 50*time.Millisecond)
	})
	defer cancel()
	ran := false
	cfg.run = blockedSuite(&ran, arm)

	t.Setenv(TestStageTimeoutEnv, "10m")
	_, err := runTestStage(ctx, cfg)
	if err == nil || !ran || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a caller deadline must fail the stage after starting the suite and stay inspectable: ran=%v err=%v", ran, err)
	}
	message := err.Error()
	if !strings.Contains(message, "race-detector tests") || !strings.Contains(message, "its caller stopped it") ||
		strings.Contains(message, "hit the 10m0s stage bound") {
		t.Errorf("a caller deadline must be blamed on the caller, got %q", message)
	}
}

// Boundary: the stage's own bound firing first, under a run deadline further out, is still the
// stage bound. The fix must not trade one wrong attribution for the other.
func TestTestStageStillBlamesItsOwnBoundUnderARunDeadline(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	ran := false
	cfg.run = blockedSuite(&ran, holdStageBound(cfg))

	t.Setenv(TestStageTimeoutEnv, "50ms")
	ctx, cancel := WithRunDeadline(context.Background(), EnvRunBudget())
	defer cancel()
	_, err := runTestStage(ctx, cfg)
	if err == nil || !ran {
		t.Fatalf("a stage cut off by its bound must fail after starting the suite: ran=%v err=%v", ran, err)
	}
	message := err.Error()
	for _, want := range []string{"race-detector tests hit the 50ms stage bound", "bound firing", TestStageTimeoutEnv} {
		if !strings.Contains(message, want) {
			t.Errorf("message must mention %q, got %q", want, message)
		}
	}
	if strings.Contains(message, "gate run's deadline") {
		t.Errorf("the stage's own bound must not be reported as the run deadline, got %q", message)
	}
}

// Negative: the run deadline can fire before the worktree exists, and is reported as itself there.
func TestTestStageBlamesTheRunDeadlineDuringWorktreeCreation(t *testing.T) {
	repoDir := newHermeticGitRepo(t)
	seedGoModule(t, repoDir)
	cfg, _ := newTestConfig(t, repoDir, false)
	ran := false
	cfg.run = blockedSuite(&ran, nil)

	t.Setenv(TestStageTimeoutEnv, "10m")
	ctx, cancel := WithRunDeadline(context.Background(), RunBudget{StageBound: time.Nanosecond})
	defer cancel()
	<-ctx.Done()
	_, err := runTestStage(ctx, cfg)
	if err == nil || ran {
		t.Fatalf("an expired run must fail before the suite starts: ran=%v err=%v", ran, err)
	}
	message := err.Error()
	for _, want := range []string{"creating the isolated test worktree", "gate run's deadline of 1ns"} {
		if !strings.Contains(message, want) {
			t.Errorf("message must mention %q, got %q", want, message)
		}
	}
	for _, wrong := range []string{"could not be created", "hit the 10m0s stage bound"} {
		if strings.Contains(message, wrong) {
			t.Errorf("an expired run must not be reported as %q, got %q", wrong, message)
		}
	}
}

// A scanner killed by the run deadline fails with whatever it printed, which read as a finding.
// executeStage names the deadline on every stage, not only on the race stage that reports its own.
func TestExecuteStageAttributesARunCut(t *testing.T) {
	cfg, _ := newTestConfig(t, t.TempDir(), false)
	scanner := errors.New("govulncheck found vulnerabilities: ")
	failing := stage{"Security & SCA Scan", func(context.Context, *stageConfig) (string, error) { return "", scanner }}

	// Negative: a live run leaves a genuine failure exactly as the stage reported it.
	if err := executeStage(context.Background(), failing, cfg); !errors.Is(err, scanner) || err.Error() != scanner.Error() {
		t.Fatalf("a failure in a live run must be returned unchanged, got %v", err)
	}

	// Positive: under an expired run the failure is attributed to the run deadline.
	expired, cancel := WithRunDeadline(context.Background(), RunBudget{StageBound: time.Nanosecond})
	defer cancel()
	<-expired.Done()
	err := executeStage(expired, failing, cfg)
	if !errors.Is(err, scanner) || !errors.Is(err, context.Cause(expired)) {
		t.Fatalf("a cut stage must keep both its own error and the cause, got %v", err)
	}
	got := cfg.rep.Stages[len(cfg.rep.Stages)-1].Message
	for _, want := range []string{`stage "Security & SCA Scan" did not finish: the gate run's deadline of 1ns`, "not a finding", "govulncheck"} {
		if !strings.Contains(got, want) {
			t.Errorf("stage message must mention %q, got %q", want, got)
		}
	}

	// Boundary: a stage that passes, or already names the cause, is left alone under an expired run.
	passing := stage{"Pass", func(context.Context, *stageConfig) (string, error) { return "", nil }}
	if err := executeStage(expired, passing, cfg); err != nil {
		t.Errorf("a passing stage must stay passing, got %v", err)
	}
	selfReported := stage{"Race-Detector Tests", func(ctx context.Context, _ *stageConfig) (string, error) {
		return "", callerCutError("race-detector tests", context.Cause(ctx), time.Minute, "wt", "")
	}}
	if err := executeStage(expired, selfReported, cfg); err == nil {
		t.Fatal("a stage reporting its own cut must still fail")
	}
	if got := cfg.rep.Stages[len(cfg.rep.Stages)-1].Message; strings.Count(got, "gate run's deadline") != 1 {
		t.Errorf("a stage reporting its own cut must not be attributed twice, got %q", got)
	}
}
