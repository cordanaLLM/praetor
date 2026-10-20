package dogfood

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/sentinel"
)

// MaxScheduleDuration leaves one minute for bookkeeping around the finite suite.
const MaxScheduleDuration = MaxSuiteDuration + time.Minute

// RunSchedule executes at most one due suite under a crash-safe advisory lock.
// It never deletes evidence, changes input pins, calls providers or pushes code.
// engine is the running binary's identity, which the suite report records as
// SuiteOptions.Engine documents.
func RunSchedule(ctx context.Context, path string, engine buildid.Identity) (*ScheduleReport, error) {
	return scheduleTick(ctx, path, true, time.Now, suiteRunnerAs(engine), nil)
}

// suiteRunnerAs runs a scheduled suite as the binary engine identifies, so a scheduled report
// names the same build as a suite run directly by that binary.
func suiteRunnerAs(engine buildid.Identity) scheduleRunner {
	return func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		opts.Engine = engine
		return RunSuite(ctx, opts)
	}
}

// ScheduleStatus reads inputs and state without creating files or running cases.
func ScheduleStatus(ctx context.Context, path string) (*ScheduleReport, error) {
	return scheduleTick(ctx, path, false, time.Now, RunSuite, nil)
}

// ScheduleStatusWithinRoot confines every embedded read path before access for hosted callers.
func ScheduleStatusWithinRoot(ctx context.Context, path, inputRoot string) (*ScheduleReport, error) {
	if inputRoot == "" || !filepath.IsAbs(inputRoot) {
		return nil, errors.New("schedule confinement requires an absolute input root")
	}
	return scheduleTickWithinRoot(ctx, path, inputRoot, false, time.Now, RunSuite, nil)
}

type scheduleRunner func(context.Context, SuiteOptions) (*SuiteReport, error)
type scheduleSession struct {
	snapshot   *scheduleSnapshot
	state      *scheduleState
	report     *ScheduleReport
	root       *os.Root
	now        func() time.Time
	runner     scheduleRunner
	hostReader func() (sentinel.HostStats, error)
}

func scheduleTick(ctx context.Context, path string, execute bool, now func() time.Time, runner scheduleRunner, hostReader func() (sentinel.HostStats, error)) (*ScheduleReport, error) {
	return scheduleTickWithinRoot(ctx, path, "", execute, now, runner, hostReader)
}

func scheduleTickWithinRoot(ctx context.Context, path, inputRoot string, execute bool, now func() time.Time, runner scheduleRunner, hostReader func() (sentinel.HostStats, error)) (report *ScheduleReport, err error) {
	if ctx == nil {
		return nil, errors.New("schedule requires a context")
	}
	ctx, cancel := context.WithTimeout(ctx, MaxScheduleDuration)
	defer cancel()
	snapshot, err := prepareScheduleSnapshot(ctx, path, inputRoot, execute)
	if err != nil {
		return nil, err
	}
	report = &ScheduleReport{RunnerSHA256: snapshot.runnerSHA, RunnerIdentity: "configured_binary", Version: 1, Config: snapshot.config, Fingerprint: snapshot.fingerprint, Status: "due", Scope: "Finite local suite tick; retained-byte cap is an admission threshold, not an in-flight filesystem quota. No input pin updates, provider dispatch or promotion; deletion only of the oldest retained runs under retention prune-oldest."}
	defer func() {
		if err != nil {
			report.Verified = false
		}
	}()
	root, err := openScheduleRoot(ctx, snapshot.config.StateDir, execute)
	if err != nil {
		return report, err
	}
	if root == nil {
		return report, nil
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	release, busy, err := acquireScheduleLock(root, execute)
	if err != nil {
		return report, err
	}
	if release != nil {
		defer func() { err = errors.Join(err, release()) }()
	}
	if busy {
		report.Status = "busy"
		return report, nil
	}
	return report, inspectScheduleSession(ctx, snapshot, report, root, release != nil, execute, now, runner, hostReader)
}

// openScheduleRoot opens the state directory, returning nil when nonexistent on dry run.
func openScheduleRoot(ctx context.Context, stateDir string, execute bool) (*os.Root, error) {
	root, err := openScheduleState(ctx, stateDir, execute)
	if errors.Is(err, os.ErrNotExist) && !execute {
		return nil, nil
	}
	return root, err
}

// acquireScheduleLock attempts to lock schedule root, ignoring ErrNotExist.
func acquireScheduleLock(root *os.Root, execute bool) (func() error, bool, error) {
	release, busy, err := lockSchedule(root, execute)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	return release, busy, nil
}

func inspectScheduleSession(ctx context.Context, snapshot *scheduleSnapshot, report *ScheduleReport, root *os.Root, locked, execute bool, now func() time.Time, runner scheduleRunner, hostReader func() (sentinel.HostStats, error)) error {
	state, err := readScheduleState(ctx, root)
	if err != nil {
		return err
	}
	if !locked && state.Attempts > 0 {
		return errors.New("persisted schedule is missing its lock file")
	}
	session := scheduleSession{snapshot: snapshot, state: state, report: report, root: root, now: now, runner: runner, hostReader: hostReader}
	if err := session.inspect(ctx, execute); err != nil {
		return err
	}
	if !execute || report.Status != "due" {
		return nil
	}
	return session.runWhenHostAdmitted(ctx)
}

func (s *scheduleSession) runWhenHostAdmitted(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	read := s.hostReader
	if read == nil {
		read = admission.HostReader(s.snapshot.config.SourceRoot)
	}
	stats, err := read()
	if err != nil {
		return fmt.Errorf("schedule host admission: read host: %w", err)
	}
	verdict := admission.Judge(stats, admission.HostCappedPolicy(stats))
	s.report.HostReason = verdict.Reason
	if !verdict.Admit {
		s.report.Status = "host_held"
		return nil
	}
	return s.run(ctx)
}

func (s *scheduleSession) inspect(ctx context.Context, execute bool) error {
	usage, err := scanScheduleUsage(ctx, s.root)
	if err != nil {
		return err
	}
	if err := s.resumePrune(usage, execute); err != nil {
		return err
	}
	expectedRuns := s.state.Attempts - s.state.PrunedThrough
	if usage.runs != expectedRuns {
		return errors.New("retained attempt directories disagree with durable state; manual review required")
	}
	if expectedRuns > 0 && (usage.minRun != s.state.PrunedThrough+1 || usage.maxRun != s.state.Attempts) {
		return errors.New("retained attempt directories disagree with durable state; manual review required")
	}
	if err := s.handleInterrupted(ctx, execute); err != nil {
		return err
	}
	if err := s.pruneOldest(ctx, usage, execute); err != nil {
		return err
	}
	s.refreshReport()
	s.report.RetainedBytes, s.report.RetainedRuns = usage.bytes, usage.runs
	s.report.Status = s.decision(usage.entries)
	return nil
}

func (s *scheduleSession) handleInterrupted(ctx context.Context, execute bool) error {
	if s.state.LastAttempt == nil || s.state.LastAttempt.Status != "running" {
		return nil
	}
	s.state.LastAttempt.Status = "interrupted"
	s.state.LastAttempt.FinishedAt = s.now().UTC()
	if s.state.LastAttempt.FinishedAt.Before(s.state.LastAttempt.StartedAt) {
		s.state.LastAttempt.FinishedAt = s.state.LastAttempt.StartedAt
	}
	s.state.LastAttempt.Error = "Previous process exited before recording a result; retained evidence is incomplete"
	if execute {
		return saveScheduleJSON(ctx, s.root, "state.json", s.state)
	}
	return nil
}

func (s *scheduleSession) refreshReport() {
	s.report.Attempts = s.state.Attempts
	s.report.ConsecutiveFailures = s.state.ConsecutiveFailures
	s.report.PrunedThrough = s.state.PrunedThrough
	s.report.LastAttempt = s.state.LastAttempt
}

func (s *scheduleSession) decision(entries int) string {
	if s.isResourceBlocked(entries) {
		return "resource_blocked"
	}
	c, last := s.snapshot.config, s.state.LastAttempt
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

// isResourceBlocked checks whether retained runs or bytes exceed configured limits.
func (s *scheduleSession) isResourceBlocked(entries int) bool {
	if entries > maxScheduleEntries {
		s.report.Reason = fmt.Sprintf("retained evidence exceeds %d entries", maxScheduleEntries)
		return true
	}
	c := s.snapshot.config
	blocked := s.report.RetainedRuns >= c.MaxRuns
	if c.Retention == "prune-oldest" {
		blocked = s.report.RetainedRuns > c.MaxRuns
	}
	return blocked || s.report.RetainedBytes >= c.MaxBytes
}

func (s *scheduleSession) pruneOldest(ctx context.Context, usage *scheduleUsage, execute bool) error {
	if s.snapshot.config.Retention != "prune-oldest" {
		return nil
	}
	for step := 0; step < 1024; step++ {
		if !s.shouldPrune(usage.runs, usage.entries, usage.bytes) {
			break
		}
		nextPrune := s.state.PrunedThrough + 1
		if !s.canPruneRun(nextPrune) {
			break
		}
		if err := s.applyPrune(ctx, usage, nextPrune, execute); err != nil {
			return err
		}
	}
	return nil
}

// resumePrune finishes a prune a crash interrupted: pruneOldest persists pruned_through before it removes
// the directory, so a process that died between the two leaves run directories numbered at or below
// pruned_through. They are already given up by durable state; without this every later tick would
// refuse the disagreement and the schedule would stay stopped. A dry run only names them.
func (s *scheduleSession) resumePrune(usage *scheduleUsage, execute bool) error {
	for step := 0; step < 1024 && usage.runs > 0 && usage.minRun <= s.state.PrunedThrough; step++ {
		name := scheduleRunName(usage.minRun)
		if execute {
			if err := s.root.RemoveAll(name); err != nil {
				return err
			}
		}
		s.report.PrunedRuns = append(s.report.PrunedRuns, name)
		usage.forget(name)
	}
	return nil
}

func (s *scheduleSession) shouldPrune(retainedRuns, entries int, bytes int64) bool {
	c := s.snapshot.config
	return retainedRuns > c.MaxRuns || entries > maxScheduleEntries/2 || bytes >= c.MaxBytes
}

func (s *scheduleSession) canPruneRun(number int) bool {
	if number >= s.state.Attempts {
		return false
	}
	if s.state.LastAttempt != nil && s.state.LastAttempt.Number == number && s.state.LastAttempt.Status == "running" {
		return false
	}
	return true
}

func (s *scheduleSession) applyPrune(ctx context.Context, usage *scheduleUsage, number int, execute bool) error {
	name := scheduleRunName(number)
	if execute {
		s.state.PrunedThrough = number
		if err := saveScheduleJSON(ctx, s.root, "state.json", s.state); err != nil {
			return err
		}
		if err := s.root.RemoveAll(name); err != nil {
			return err
		}
	} else {
		s.state.PrunedThrough = number
	}
	s.report.PrunedRuns = append(s.report.PrunedRuns, name)
	usage.forget(name)
	return nil
}

// forget drops the oldest retained run, name, from the usage counts after it was removed or would be.
func (u *scheduleUsage) forget(name string) {
	u.runs--
	u.minRun++
	u.entries = max(u.entries-u.runEntries[name], 0)
	u.bytes = max(u.bytes-u.runBytes[name], 0)
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
	if err := saveScheduleJSON(ctx, s.root, "state.json", s.state); err != nil {
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
	writeErr := s.snapshot.materialize(ctx, attempt, s.root, "inputs")
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
	persistErr := saveScheduleJSON(ctx, s.root, "state.json", s.state)
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
