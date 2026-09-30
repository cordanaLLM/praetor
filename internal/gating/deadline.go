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
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
	"github.com/cordanaLLM/praetor/internal/util"
)

// OtherStagesAllowance is the part of a gate run's deadline granted to every stage except the
// race-detector tests: describing the tree, prefetch, the HISS scan, the security scanners, the
// flavor audit and the receipt. The whole-run deadline is the resolved race-stage bound, once per
// test suite, plus this allowance. A fixed whole-run deadline below the stage's ceiling cut the
// race stage short no matter what PRAETOR_TEST_STAGE_TIMEOUT asked for (#314).
const OtherStagesAllowance = 5 * time.Minute

// CargoStagesAllowance is what a Cargo.lock adds to OtherStagesAllowance: the bounds cargo fetch
// --locked and cargo audit run under, beside the Go prefetch and scanners the allowance already
// covers. Without it the two Cargo commands could spend the whole allowance on their own.
const CargoStagesAllowance = DefaultPrefetchTimeout + CargoAuditTimeout

// errStageBoundFired is the cause the race stage's own context carries when its bound fires. It
// is what lets the stage blame its bound only when that bound, and not its caller, stopped it.
var errStageBoundFired = errors.New("race stage bound fired")

// RunBudget is how a whole gate run's deadline is composed.
type RunBudget struct {
	// StageBound is the resolved race-stage bound. Every test suite the test stage runs gets
	// the whole of it, each in its own isolated worktree.
	StageBound time.Duration
	// Suites is how many test suites the test stage runs: one per language whose marker is at
	// the repository root (go.mod, Cargo.lock). Zero counts as one.
	Suites int
	// Allowance is the time granted to every other stage.
	Allowance time.Duration
	// Devcontainer is the image build bound (devcontainer.ImageBuildTimeout), reserved when the
	// run plans to build the repository's devcontainer (planDevcontainer), and zero otherwise.
	Devcontainer time.Duration
	// Note says why StageBound differs from the default; it is empty when it does not.
	Note string
}

// TestSuites is how many stage bounds the run deadline reserves for the test stage: Suites, and at
// least one.
func (b RunBudget) TestSuites() int {
	return max(b.Suites, 1)
}

// Timeout is the whole run's deadline: one stage bound per test suite plus the allowance for the
// other stages, plus the image build bound when the run builds a devcontainer.
func (b RunBudget) Timeout() time.Duration {
	return b.StageBound*time.Duration(b.TestSuites()) + b.Allowance + b.Devcontainer
}

// String renders the deadline with its composition, the form every report of it uses. A run with
// one test suite and no devcontainer keeps the form it had before either existed.
func (b RunBudget) String() string {
	image := ""
	if b.Devcontainer > 0 {
		image = fmt.Sprintf(" + %s to build the devcontainer image", b.Devcontainer)
	}
	if suites := b.TestSuites(); suites > 1 {
		return fmt.Sprintf("%s (%d test suites at a %s stage bound each + %s for the other stages%s)",
			b.Timeout(), suites, b.StageBound, b.Allowance, image)
	}
	return fmt.Sprintf("%s (%s race stage bound + %s for the other stages%s)",
		b.Timeout(), b.StageBound, b.Allowance, image)
}

// TimeoutSeconds is Timeout in whole seconds, rounded up so a caller bounding the gate as a
// subprocess never allows it less time than the gate allows itself.
func (b RunBudget) TimeoutSeconds() int64 {
	return int64(math.Ceil(b.Timeout().Seconds()))
}

// ResolveRunBudget resolves a run's budget from a raw PRAETOR_TEST_STAGE_TIMEOUT value through
// the parser the race stage uses, so the run deadline and the stage bound cannot disagree. It
// budgets one test suite and the Go stages; ResolveRepoRunBudget sizes it to a repository.
func ResolveRunBudget(raw string) RunBudget {
	bound, note := testStageTimeout(raw)
	return RunBudget{StageBound: bound, Suites: 1, Allowance: OtherStagesAllowance, Note: note}
}

// ResolveRepoRunBudget is ResolveRunBudget sized to the repository at repoDir. Where its root
// holds a Cargo.lock the Cargo stages run too: their prefetch and audit bounds join the allowance
// (CargoStagesAllowance), and beside a go.mod the Cargo suite is a second test suite with a stage
// bound of its own. A deadline sized for one suite let the Go suite and the Cargo commands spend
// the time the Cargo suite needed, so the run deadline cut it however high
// PRAETOR_TEST_STAGE_TIMEOUT was raised: the #314 failure, back for mixed repositories.
func ResolveRepoRunBudget(raw, repoDir string) RunBudget {
	budget := ResolveRunBudget(raw)
	if !util.FileExists(filepath.Join(repoDir, CargoLockFile)) {
		return budget
	}
	budget.Allowance += CargoStagesAllowance
	if util.FileExists(filepath.Join(repoDir, "go.mod")) {
		budget.Suites++
	}
	return budget
}

// EnvRunBudget resolves the budget of a run over repoDir from the process environment and this
// machine. It reserves devcontainer.ImageBuildTimeout exactly when the run would build the
// repository's devcontainer (planDevcontainer), so `gate run`, `gate deadline` and the pre-push
// hook that bounds the gate by it agree on the deadline.
func EnvRunBudget(repoDir string) RunBudget {
	budget := stageBudget(repoDir)
	ctx, cancel := context.WithTimeout(context.Background(), GitQueryTimeout)
	defer cancel()
	if plan, _ := planDevcontainer(ctx, repoDir, false, hostMachine()); plan != nil {
		budget.Devcontainer = devcontainer.ImageBuildTimeout
	}
	return budget
}

// stageBudget is the budget sized to repoDir from PRAETOR_TEST_STAGE_TIMEOUT alone: the stage bound
// and note a test stage runs under, which no devcontainer plan changes.
func stageBudget(repoDir string) RunBudget {
	return ResolveRepoRunBudget(os.Getenv(TestStageTimeoutEnv), repoDir)
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
