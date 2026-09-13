package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// MaxScheduleDuration leaves one minute for bookkeeping around the finite suite.
const MaxScheduleDuration = MaxSuiteDuration + time.Minute

// RunSchedule executes at most one due suite under a crash-safe advisory lock.
// It never deletes evidence, changes input pins, calls providers or pushes code.
func RunSchedule(ctx context.Context, path string) (*ScheduleReport, error) {
	return scheduleTick(ctx, path, true, time.Now, RunSuite)
}

// ScheduleStatus reads inputs and state without creating files or running cases.
func ScheduleStatus(ctx context.Context, path string) (*ScheduleReport, error) {
	return scheduleTick(ctx, path, false, time.Now, RunSuite)
}

// ScheduleStatusWithinRoot confines every embedded read path before access for hosted callers.
func ScheduleStatusWithinRoot(ctx context.Context, path, inputRoot string) (*ScheduleReport, error) {
	if inputRoot == "" || !filepath.IsAbs(inputRoot) {
		return nil, errors.New("schedule confinement requires an absolute input root")
	}
	return scheduleTickWithinRoot(ctx, path, inputRoot, false, time.Now, RunSuite)
}

type scheduleRunner func(context.Context, SuiteOptions) (*SuiteReport, error)
type scheduleSession struct {
	snapshot *scheduleSnapshot
	state    *scheduleState
	report   *ScheduleReport
	root     *os.Root
	now      func() time.Time
	runner   scheduleRunner
}

func scheduleTick(ctx context.Context, path string, execute bool, now func() time.Time, runner scheduleRunner) (*ScheduleReport, error) {
	return scheduleTickWithinRoot(ctx, path, "", execute, now, runner)
}

func scheduleTickWithinRoot(ctx context.Context, path, inputRoot string, execute bool, now func() time.Time, runner scheduleRunner) (report *ScheduleReport, err error) {
	if ctx == nil {
		return nil, errors.New("schedule requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxScheduleDuration)
	defer cancel()
	snapshot, err := prepareScheduleSnapshot(ctx, path, inputRoot, execute)
	if err != nil {
		return nil, err
	}
	report = &ScheduleReport{RunnerSHA256: snapshot.runnerSHA, RunnerIdentity: "configured_binary", Version: 1, Config: snapshot.config, Fingerprint: snapshot.fingerprint, Status: "due", Scope: "Finite local suite tick; retained-byte cap is an admission threshold, not an in-flight filesystem quota. No input pin updates, provider dispatch, deletion or promotion."}
	defer func() {
		if err != nil {
			report.Verified = false
		}
	}()
	root, err := openScheduleState(ctx, snapshot.config.StateDir, execute)
	if errors.Is(err, os.ErrNotExist) && !execute {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	lock, busy, err := lockSchedule(root, execute)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return report, err
	}
	if lock != nil {
		defer func() { err = errors.Join(err, lock.Close()) }()
	}
	if busy {
		report.Status = "busy"
		return report, nil
	}
	return report, inspectScheduleSession(ctx, snapshot, report, root, lock, execute, now, runner)
}

func inspectScheduleSession(ctx context.Context, snapshot *scheduleSnapshot, report *ScheduleReport, root *os.Root, lock *os.File, execute bool, now func() time.Time, runner scheduleRunner) error {
	state, err := readScheduleState(ctx, root)
	if err != nil {
		return err
	}
	if lock == nil && state.Attempts > 0 {
		return errors.New("persisted schedule is missing its lock file")
	}
	session := scheduleSession{snapshot: snapshot, state: state, report: report, root: root, now: now, runner: runner}
	if err := session.inspect(ctx, execute); err != nil {
		return err
	}
	if !execute || report.Status != "due" {
		return nil
	}
	return session.run(ctx)
}

func (s *scheduleSession) inspect(ctx context.Context, execute bool) error {
	usage, err := scanScheduleUsage(ctx, s.root)
	if err != nil {
		return err
	}
	if usage.runs != s.state.Attempts {
		return errors.New("retained attempt directories disagree with durable state; manual review required")
	}
	if s.state.LastAttempt != nil && s.state.LastAttempt.Status == "running" {
		s.state.LastAttempt.Status = "interrupted"
		s.state.LastAttempt.FinishedAt = s.now().UTC()
		if s.state.LastAttempt.FinishedAt.Before(s.state.LastAttempt.StartedAt) {
			s.state.LastAttempt.FinishedAt = s.state.LastAttempt.StartedAt
		}
		s.state.LastAttempt.Error = "Previous process exited before recording a result; retained evidence is incomplete"
		if execute {
			if err := saveScheduleJSON(s.root, "state.json", s.state); err != nil {
				return err
			}
		}
	}
	s.refreshReport()
	s.report.RetainedBytes, s.report.RetainedRuns = usage.bytes, usage.runs
	s.report.Status = s.decision()
	return nil
}

func (s *scheduleSession) refreshReport() {
	s.report.Attempts = s.state.Attempts
	s.report.ConsecutiveFailures = s.state.ConsecutiveFailures
	s.report.LastAttempt = s.state.LastAttempt
}

func (s *scheduleSession) decision() string {
	c, last := s.snapshot.config, s.state.LastAttempt
	if s.report.RetainedRuns >= c.MaxRuns || s.report.RetainedBytes >= c.MaxBytes {
		return "resource_blocked"
	}
	if last == nil {
		return "due"
	}
	changed := last.Fingerprint != s.snapshot.fingerprint
	if !changed && s.state.ConsecutiveFailures >= c.MaxConsecutiveFailures {
		return "circuit_blocked"
	}
	seconds := c.RetrySeconds
	anchor := last.FinishedAt
	if changed {
		anchor = last.StartedAt
	} else if last.Status == "verified" {
		seconds = c.IntervalSeconds
	}
	s.report.NextDue = anchor.Add(time.Duration(seconds) * time.Second)
	if s.now().Before(s.report.NextDue) {
		return "cooldown"
	}
	return "due"
}

func scheduleRunName(number int) string { return fmt.Sprintf("run-%06d", number) }

func (s *scheduleSession) run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	number := s.state.Attempts + 1
	name := scheduleRunName(number)
	if err := s.root.Mkdir(name, 0o700); err != nil {
		return err
	}
	if s.state.LastAttempt == nil || s.state.LastAttempt.Fingerprint != s.snapshot.fingerprint {
		s.state.ConsecutiveFailures = 0
	}
	s.state.Attempts = number
	s.state.ConsecutiveFailures++
	s.state.LastAttempt = &ScheduleAttempt{Number: number, Fingerprint: s.snapshot.fingerprint, ArtifactDir: filepath.Join(s.snapshot.config.StateDir, name), Status: "running", StartedAt: s.now().UTC()}
	s.report.Status = "running"
	s.refreshReport()
	if err := saveScheduleJSON(s.root, "state.json", s.state); err != nil {
		return err
	}
	suite, runErr := s.execute(ctx, name)
	s.report.Suite = suite
	if runErr == nil && (suite == nil || !suite.Verified || suite.Status != "verified") {
		runErr = errors.New("suite did not return a verified outcome")
	}
	bookkeeping, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Minute)
	defer cancel()
	runErr = errors.Join(runErr, s.planRepairs(bookkeeping))
	return s.finish(bookkeeping, runErr)
}

func (s *scheduleSession) execute(ctx context.Context, name string) (*SuiteReport, error) {
	attempt, err := s.root.OpenRoot(name)
	if err != nil {
		return nil, err
	}
	writeErr := s.snapshot.materialize(ctx, attempt, "inputs")
	if err := errors.Join(writeErr, attempt.Close()); err != nil {
		return nil, err
	}
	dir := s.state.LastAttempt.ArtifactDir
	if s.snapshot.config.RepairPolicy != nil {
		if err := ValidateRepairPolicy(ctx, s.snapshotPolicy()); err != nil {
			return nil, err
		}
	}
	return s.runner(ctx, SuiteOptions{ConfigPath: filepath.Join(dir, "inputs", "suite.json"), SourceRoot: filepath.Join(dir, "inputs", "source"), ArtifactDir: filepath.Join(dir, "suite"), Stage: "verify", AllowRemote: s.snapshot.config.AllowRemote})
}

func (s *scheduleSession) finish(ctx context.Context, runErr error) error {
	a := s.state.LastAttempt
	a.FinishedAt = s.now().UTC()
	if a.FinishedAt.Before(a.StartedAt) {
		a.FinishedAt = a.StartedAt
		runErr = errors.Join(runErr, errors.New("wall clock moved backwards during attempt"))
	}
	a.Status = "failed"
	a.Error = "Suite or schedule bookkeeping failed; inspect private attempt evidence"
	if runErr == nil {
		a.Status = "verified"
		a.Error = ""
		s.state.ConsecutiveFailures = 0
	}
	// Bookkeeping still runs after cancellation; no additional suite work starts.
	usage, scanErr := scanScheduleUsage(ctx, s.root)
	if scanErr != nil {
		a.Status = "failed"
		a.Error = "Retained evidence accounting failed"
		if runErr == nil {
			s.state.ConsecutiveFailures = 1
		}
	}
	if usage != nil {
		s.report.RetainedBytes, s.report.RetainedRuns = usage.bytes, usage.runs
	}
	persistErr := saveScheduleJSON(s.root, "state.json", s.state)
	s.report.Status = a.Status
	s.report.Verified = runErr == nil && scanErr == nil && persistErr == nil
	if persistErr != nil {
		s.report.Status = "failed"
	}
	s.refreshReport()
	return errors.Join(runErr, scanErr, persistErr)
}

func (s *scheduleSession) planRepairs(ctx context.Context) error {
	if s.snapshot.config.RepairPolicy == nil || s.report.Suite == nil || s.report.Suite.Verified {
		return nil
	}
	policy := s.snapshotPolicy()
	dir := s.state.LastAttempt.ArtifactDir
	plan, planErr := PlanRepairs(ctx, s.report.Suite, policy)
	if plan == nil {
		return planErr
	}
	saveErr := SaveRepairPlan(ctx, filepath.Join(dir, "repairs"), plan)
	return errors.Join(planErr, saveErr)
}

func prepareScheduleSnapshot(ctx context.Context, path, inputRoot string, execute bool) (*scheduleSnapshot, error) {
	snapshot, err := loadScheduleSnapshotWithinRoot(ctx, path, inputRoot)
	if err != nil {
		return nil, err
	}
	if execute {
		if err := verifyScheduleRunner(ctx, snapshot.runnerSHA); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

func (s *scheduleSession) snapshotPolicy() RepairPolicy {
	policy := *s.snapshot.config.RepairPolicy
	dir := s.state.LastAttempt.ArtifactDir
	policy.RoutingConfig = filepath.Join(dir, "inputs", "routing.json")
	if policy.UsagePath != "" {
		policy.UsagePath = filepath.Join(dir, "inputs", "usage.json")
	}
	return policy
}
