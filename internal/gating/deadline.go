// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"
)

// OtherStagesAllowance is the part of a gate run's deadline granted to every stage except the
// race-detector tests: describing the tree, prefetch, the HISS scan, the security scanners, the
// flavor audit and the receipt. The whole-run deadline is the resolved race-stage bound plus this
// allowance. A fixed whole-run deadline below the stage's ceiling cut the race stage short no
// matter what PRAETOR_TEST_STAGE_TIMEOUT asked for (#314).
const OtherStagesAllowance = 5 * time.Minute

// errStageBoundFired is the cause the race stage's own context carries when its bound fires. It
// is what lets the stage blame its bound only when that bound, and not its caller, stopped it.
var errStageBoundFired = errors.New("race stage bound fired")

// RunBudget is how a whole gate run's deadline is composed.
type RunBudget struct {
	// StageBound is the resolved race-stage bound.
	StageBound time.Duration
	// Allowance is the time granted to every other stage.
	Allowance time.Duration
	// Note says why StageBound differs from the default; it is empty when it does not.
	Note string
}

// Timeout is the whole run's deadline: the stage bound plus the allowance for the other stages.
func (b RunBudget) Timeout() time.Duration {
	return b.StageBound + b.Allowance
}

// String renders the deadline with its composition, the form every report of it uses.
func (b RunBudget) String() string {
	return fmt.Sprintf("%s (%s race stage bound + %s for the other stages)",
		b.Timeout(), b.StageBound, b.Allowance)
}

// TimeoutSeconds is Timeout in whole seconds, rounded up so a caller bounding the gate as a
// subprocess never allows it less time than the gate allows itself.
func (b RunBudget) TimeoutSeconds() int64 {
	return int64(math.Ceil(b.Timeout().Seconds()))
}

// ResolveRunBudget resolves a run's budget from a raw PRAETOR_TEST_STAGE_TIMEOUT value through
// the parser the race stage uses, so the run deadline and the stage bound cannot disagree.
func ResolveRunBudget(raw string) RunBudget {
	bound, note := testStageTimeout(raw)
	return RunBudget{StageBound: bound, Allowance: OtherStagesAllowance, Note: note}
}

// EnvRunBudget resolves the run's budget from the process environment.
func EnvRunBudget() RunBudget {
	return ResolveRunBudget(os.Getenv(TestStageTimeoutEnv))
}

// RunDeadlineError is the cause a run context carries once its deadline has fired. It names the
// deadline's value and how it was composed, so a stage cut off by the run can say so.
type RunDeadlineError struct {
	Budget RunBudget
}

// Error names the run deadline that fired and its composition.
func (e *RunDeadlineError) Error() string {
	return "the gate run's deadline of " + e.Budget.String()
}

// WithRunDeadline bounds a whole gate run by budget.Timeout() (HISS-02). Once the deadline has
// fired, context.Cause is a *RunDeadlineError on the returned context and on every derived
// context the firing ended; a derived context whose own deadline fired first keeps its own cause.
func WithRunDeadline(parent context.Context, budget RunBudget) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, budget.Timeout(), &RunDeadlineError{Budget: budget})
}

// withStageBound bounds the race stage by its own resolved bound. When the run deadline is
// sooner, the returned context ends with the run's cause instead of errStageBoundFired.
func withStageBound(parent context.Context, bound time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeoutCause(parent, bound, errStageBoundFired)
}

// cutError explains a stage context that ended before the stage's work did, or returns nil while
// the context is live. context.Cause records whichever deadline fired first, so the stage bound is
// blamed only when the stage's own deadline fired. Blaming it for the run deadline sent an
// operator to raise a bound the stage never reached: "hit the 30m0s stage bound" after 4m57s.
func cutError(stageCtx context.Context, what string, bound time.Duration, dir, output string) error {
	cause := context.Cause(stageCtx)
	switch {
	case cause == nil:
		return nil
	case errors.Is(cause, errStageBoundFired):
		return stageBoundError(what, bound, dir, output)
	default:
		return callerCutError(what, cause, bound, dir, output)
	}
}

// callerCutError reports a stage stopped by its caller's context rather than by its own bound.
func callerCutError(what string, cause error, bound time.Duration, dir, output string) error {
	if runDeadline, ok := errors.AsType[*RunDeadlineError](cause); ok {
		return fmt.Errorf(
			"%s in %s did not finish: %w fired first, not the %s stage bound, and this is not a "+
				"test failure. The stages before it used more than the %s allowance; %s raises the "+
				"stage bound and the run deadline together (maximum %s). Output up to the cut: %s",
			what, dir, cause, bound, runDeadline.Budget.Allowance, TestStageTimeoutEnv,
			MaxTestStageTimeout, output)
	}
	return fmt.Errorf(
		"%s in %s did not finish: its caller stopped it (%w), not the %s stage bound, and this "+
			"is not a test failure. Output up to the cut: %s",
		what, dir, cause, bound, output)
}

// attributeRunCut names the caller's cause on a stage error that the cause produced, so a scanner
// killed by the run deadline reads as a cut rather than as a finding. An error that already
// carries the cause, as the race stage's own report does, is returned unchanged.
func attributeRunCut(ctx context.Context, name string, err error) error {
	cause := context.Cause(ctx)
	if err == nil || cause == nil || errors.Is(err, cause) {
		return err
	}
	return fmt.Errorf("stage %q did not finish: %w fired first, so this is not a finding: %w",
		name, cause, err)
}
