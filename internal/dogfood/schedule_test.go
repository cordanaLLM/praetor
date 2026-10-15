package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/buildid"
	"github.com/cordanaLLM/praetor/internal/util"
)

func scheduleFixture(t *testing.T, record string) (string, *ScheduleConfig) {
	t.Helper()
	opts := suiteOptions(t, suiteFixture(t, record))
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	binary, binaryErr := os.Executable()
	if binaryErr != nil {
		t.Fatal(binaryErr)
	}
	cfg := &ScheduleConfig{RunnerBinary: binary, Version: 1, SuiteConfig: opts.ConfigPath, SourceRoot: source, StateDir: filepath.Join(t.TempDir(), "state"), IntervalSeconds: 60, RetrySeconds: 60, MaxConsecutiveFailures: 3, MaxRuns: 32, MaxBytes: 2 << 30}
	path := filepath.Join(t.TempDir(), "schedule.json")
	writeScheduleFixture(t, path, cfg)
	return path, cfg
}

func writeScheduleFixture(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	// The private writer, so the fixture is private on Windows too, where the DACL decides.
	if err := util.WriteFileSecure(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func fixedScheduleTime() time.Time { return time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC) }
func verifiedScheduleRunner(_ context.Context, _ SuiteOptions) (*SuiteReport, error) {
	return &SuiteReport{Version: 1, Status: "verified", Verified: true}, nil
}
func failedScheduleRunner(_ context.Context, _ SuiteOptions) (*SuiteReport, error) {
	return &SuiteReport{Version: 1, Status: "failed"}, errors.New("fixture failure")
}

func TestScheduleActualSuiteAndReadOnlyStatus(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil || status.Status != "due" || status.Verified {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := os.Lstat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created state")
	}
	report, err := RunSchedule(context.Background(), path, buildid.Identity{Release: "v1.9.0-scheduled"})
	if err != nil || !report.Verified || report.Suite == nil || !report.Suite.Verified || report.Attempts != 1 {
		t.Fatalf("run: %+v %v", report, err)
	}
	if got := report.Suite.Engine["version"]; got != "v1.9.0-scheduled" {
		t.Fatalf("scheduled suite recorded engine_build.version %q", got)
	}
	original, err := os.ReadFile(filepath.Join(cfg.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	status, err = ScheduleStatus(context.Background(), path)
	if err != nil || status.Status != "cooldown" || status.LastAttempt.Status != "verified" || status.Verified {
		t.Fatalf("status: %+v %v", status, err)
	}
	after, err := os.ReadFile(filepath.Join(cfg.StateDir, "state.json"))
	if err != nil || string(after) != string(original) {
		t.Fatal("status changed metadata")
	}
	assertSuitePrivate(t, cfg.StateDir)
}

// A schedule whose repair router declares only the repair task loads: the source repository's
// register rows (this repository's .standards.yaml) are checked against its own router (#765).
func TestScheduleAcceptsNarrowRepairRouter(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	policy := repairTestPolicy(t)
	policy.RoutingConfig = repairTestFile(t, narrowRepairRoutingFixture)
	cfg.RepairPolicy = &policy
	writeScheduleFixture(t, path, cfg)
	if report, err := ScheduleStatus(context.Background(), path); err != nil || report == nil || report.Config.RepairPolicy == nil {
		t.Fatalf("narrow repair router refused: %+v %v", report, err)
	}
}

func TestScheduleCanonicalizesCallerRepairRegisterPolicy(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	policy := repairTestPolicy(t)
	policy.Register = "docs"
	policy.RegisterSource = "tasks.ci_debugging"
	policy.MaxOutputTokens = 256
	policy.PromptRegister = "docs"
	policy.PromptRegisterSource = "surfaces.prompts"
	policy.RegisterManifestSHA256 = strings.Repeat("0", 64)
	cfg.RepairPolicy = &policy
	writeScheduleFixture(t, path, cfg)

	report, err := ScheduleStatus(context.Background(), path)
	if err != nil || report == nil || report.Config.RepairPolicy == nil {
		t.Fatalf("canonical repair policy unavailable: %+v %v", report, err)
	}
	canonical := report.Config.RepairPolicy
	if canonical.Register != "internal" || canonical.RegisterSource != "surfaces.agent" ||
		canonical.MaxOutputTokens != 0 || canonical.PromptRegister != "internal" ||
		canonical.PromptRegisterSource != "surfaces.prompts" || canonical.RegisterManifestSHA256 == policy.RegisterManifestSHA256 {
		t.Fatalf("caller tuple was not replaced from source manifest: %+v", canonical)
	}
	policy.Register = "social"
	policy.RegisterSource = "tasks.other"
	policy.MaxOutputTokens = 8192
	policy.PromptRegister = "social"
	policy.PromptRegisterSource = "surfaces.agent"
	policy.RegisterManifestSHA256 = strings.Repeat("f", 64)
	cfg.RepairPolicy = &policy
	writeScheduleFixture(t, path, cfg)
	second, err := ScheduleStatus(context.Background(), path)
	if err != nil || second.Fingerprint != report.Fingerprint {
		t.Fatalf("ignored caller tuple changed canonical fingerprint: first=%s second=%+v err=%v", report.Fingerprint, second, err)
	}
}

func TestScheduleTimingCircuitAndChangedInput(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	now := fixedScheduleTime()
	clock := func() time.Time { return now }
	for i := 0; i < 3; i++ {
		report, err := scheduleTick(context.Background(), path, true, clock, failedScheduleRunner)
		if err == nil || report.Status != "failed" || report.ConsecutiveFailures != i+1 {
			t.Fatalf("attempt%d: %+v %v", i, report, err)
		}
		now = now.Add(time.Minute)
	}
	blocked, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || blocked.Status != "circuit_blocked" || blocked.Attempts != 3 {
		t.Fatalf("circuit %+v %v", blocked, err)
	}
	cfg.IntervalSeconds = 61
	writeScheduleFixture(t, path, cfg)
	report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || !report.Verified || report.Attempts != 4 || report.ConsecutiveFailures != 0 {
		t.Fatalf("changed: %+v %v", report, err)
	}
	now = now.Add(60 * time.Second)
	report, err = scheduleTick(context.Background(), path, true, clock, failedScheduleRunner)
	if err != nil || report.Status != "cooldown" || report.Attempts != 4 {
		t.Fatalf("early %+v %v", report, err)
	}
	now = now.Add(time.Second)
	report, err = scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || report.Attempts != 5 {
		t.Fatalf("boundary %+v %v", report, err)
	}
}

func TestScheduleOverlapAndCrashRecovery(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	entered, release := make(chan struct{}), make(chan struct{})
	runner := func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		close(entered)
		<-release
		return verifiedScheduleRunner(ctx, opts)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, runner); err != nil {
			t.Error(err)
		}
	}()
	<-entered
	busy, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner)
	if err != nil || busy.Status != "busy" {
		t.Errorf("overlap %+v %v", busy, err)
	}
	close(release)
	wg.Wait()
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.LastAttempt.Status = "running"
	state.LastAttempt.FinishedAt = time.Time{}
	state.ConsecutiveFailures = 1
	if err := saveScheduleJSON(t.Context(), root, "state.json", state); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	report, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner)
	if err != nil || report.LastAttempt.Status != "interrupted" || report.Status != "cooldown" || report.ConsecutiveFailures != 1 {
		t.Fatalf("interruption %+v %v", report, err)
	}
}

func TestScheduleQuotaIncludesPartialGitAndCache(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.MaxBytes = 1 << 20
	writeScheduleFixture(t, path, cfg)
	if err := os.Mkdir(cfg.StateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(cfg.StateDir, "cache", ".git")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	publicWrite(t, filepath.Join(dir, "pack"), strings.Repeat("x", 1<<20), 0o600)
	report, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner)
	if err != nil || report.Status != "resource_blocked" || report.Attempts != 0 || report.RetainedBytes < 1<<20 {
		t.Fatalf("cap %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "pack")); err != nil {
		t.Fatal("evidence disappeared")
	}
}

func TestScheduleFailurePrefixAndFalseGreen(t *testing.T) {
	requireScheduling(t)
	for _, runner := range []scheduleRunner{
		func(context.Context, SuiteOptions) (*SuiteReport, error) { return nil, nil },
		func(context.Context, SuiteOptions) (*SuiteReport, error) {
			return &SuiteReport{Verified: true, Status: "running"}, nil
		},
		failedScheduleRunner,
	} {
		path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
		report, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, runner)
		if err == nil || report.Verified || report.Status != "failed" || report.ConsecutiveFailures != 1 {
			t.Fatalf("false green %+v %v", report, err)
		}
		if _, err := os.Stat(filepath.Join(cfg.StateDir, "run-000001", "inputs", "suite.json")); err != nil {
			t.Fatal("missing retained inputs")
		}
	}
}

func TestScheduleCancelledAndNilContext(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, invalid := range []context.Context{nil, ctx} {
		if _, err := RunSchedule(invalid, path, buildid.Identity{}); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	if _, err := RunSchedule(ctx, path, buildid.Identity{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
	if _, err := os.Stat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancel created evidence")
	}
	runner := func(ctx context.Context, _ SuiteOptions) (*SuiteReport, error) { return nil, context.Canceled }
	report, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, runner)
	if !errors.Is(err, context.Canceled) || report.Status != "failed" {
		t.Fatalf("running cancel: %+v %v", report, err)
	}
}

func TestScheduleConfigStrictAndBoundary(t *testing.T) {
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	valid, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		strings.Replace(string(valid), `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(string(valid), `"version":1`, `"Version":1`, 1),
		strings.Replace(string(valid), `"allow_remote":false`, `"allow_remote":null`, 1),
		strings.Replace(string(valid), `"interval_seconds":60`, `"interval_seconds":0`, 1),
		strings.Replace(string(valid), `"max_consecutive_failures":3`, `"max_consecutive_failures":4`, 1),
		strings.Replace(string(valid), `"max_runs":32`, `"max_runs":1025`, 1),
		strings.Replace(string(valid), `"max_bytes":2147483648`, `"max_bytes":1048575`, 1),
		strings.Replace(string(valid), `"version":1`, `"retention":"unknown","version":1`, 1),
		strings.Replace(string(valid), `"allow_remote":false,`, ``, 1),
		string(valid) + `{}`,
	}
	for i, value := range bad {
		publicWrite(t, path, value, 0o600)
		if _, err := LoadScheduleConfig(context.Background(), path); err == nil {
			t.Errorf("invalid config%d accepted", i)
		}
	}
	minimal := map[string]any{"version": 1, "suite_config": cfg.SuiteConfig, "source_root": cfg.SourceRoot, "state_dir": cfg.StateDir, "allow_remote": false, "runner_binary": cfg.RunnerBinary}
	writeScheduleFixture(t, path, minimal)
	got, err := LoadScheduleConfig(context.Background(), path)
	if err != nil || got.IntervalSeconds != 86400 || got.RetrySeconds != 3600 || got.MaxRuns != 32 || got.MaxBytes != 2<<30 || got.Retention != "block" {
		t.Fatalf("defaults %+v %v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	// Only this assertion is platform-bound. Where the mode is not the file's protection
	// (Windows, whose DACL decides, #101), a 0644 mode does not make the config readable by
	// others, so the refusal cannot be provoked by mode. The nine strict-config cases and the
	// defaults above still run everywhere; skipping the whole test on that account would have
	// discarded them to avoid one assertion.
	if !util.ModeIsProtection() {
		t.Log("public-schedule refusal is provoked by mode, which does not protect files here")
		return
	}
	if _, err := LoadScheduleConfig(context.Background(), path); err == nil {
		t.Fatal("public schedule accepted")
	}
}

func TestScheduleStateCorruptionAndReadonly(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cfg.StateDir, "state.json")
	valid, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		strings.Replace(string(valid), `"consecutive_failures": 0,`, "", 1),
		strings.Replace(string(valid), `"attempts": 1`, `"attempts": 0`, 1),
		strings.Replace(string(valid), `"version": 1`, `"version": 1,"version":1`, 1),
		strings.Replace(string(valid), `"status": "verified"`, `"status": "planned"`, 1),
		`{"version":1,"attempts":0,"consecutive_failures":0,"last_attempt":null}`,
	}
	for i, value := range bad {
		publicWrite(t, statePath, value, 0o600)
		if _, err := ScheduleStatus(context.Background(), path); err == nil {
			t.Errorf("state%d accepted", i)
		}
	}
	publicWrite(t, statePath, string(valid), 0o600)
	if err := os.Chmod(statePath, 0o400); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return fixedScheduleTime().Add(time.Hour) }
	report, err := scheduleTick(context.Background(), path, true, now, verifiedScheduleRunner)
	if err == nil || report.Verified {
		t.Fatalf("readonly state modified %+v %v", report, err)
	}
	info, err := os.Stat(statePath)
	if err != nil || (util.ModeIsProtection() && info.Mode().Perm() != 0o400) {
		t.Fatal("mode broadened")
	}
}

func TestScheduleSnapshotPinsAndWithinRoot(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	first, err := loadScheduleSnapshot(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	runner := func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		publicWrite(t, cfg.SuiteConfig, `{"corrupt":true}`, 0o600)
		return RunSuite(ctx, opts)
	}
	report, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, runner)
	if err != nil || !report.Verified || report.Fingerprint != first.fingerprint {
		t.Fatalf("snapshot drift %+v %v", report, err)
	}
	if _, err := ScheduleStatusWithinRoot(context.Background(), path, filepath.Dir(path)); err == nil {
		t.Fatal("embedded external paths allowed")
	}
	if _, err := ScheduleStatusWithinRoot(context.Background(), path, ""); err == nil {
		t.Fatal("empty root allowed")
	}
}

func TestScheduleRunCeilingAndOrphanedPartial(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.MaxRuns = 1
	writeScheduleFixture(t, path, cfg)
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	later := func() time.Time { return fixedScheduleTime().Add(time.Hour) }
	report, err := scheduleTick(context.Background(), path, true, later, verifiedScheduleRunner)
	if err != nil || report.Status != "resource_blocked" || report.Attempts != 1 {
		t.Fatalf("run ceiling %+v %v", report, err)
	}
	if err := os.Mkdir(filepath.Join(cfg.StateDir, "run-000002"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := ScheduleStatus(context.Background(), path); err == nil {
		t.Fatal("orphaned partial ignored")
	}
}

func TestScheduleRunnerIdentityMismatchAndStatus(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.RunnerBinary = filepath.Join(t.TempDir(), "other-runner")
	publicWrite(t, cfg.RunnerBinary, "different executable bytes", 0o700)
	writeScheduleFixture(t, path, cfg)
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil || status.RunnerSHA256 == "" || status.RunnerIdentity != "configured_binary" {
		t.Fatalf("configured status %+v %v", status, err)
	}
	if _, err := RunSchedule(context.Background(), path, buildid.Identity{}); err == nil {
		t.Fatal("actual process mismatch allowed")
	}
	if _, err := os.Stat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("mismatch wrote state")
	}
}

func TestScheduleRetainedAttemptTypesAndSequence(t *testing.T) {
	requireScheduling(t)
	for _, replacement := range []string{"run-junk", "file", "symlink"} {
		path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
		if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
			t.Fatal(err)
		}
		original := filepath.Join(cfg.StateDir, "run-000001")
		retained := filepath.Join(cfg.StateDir, "retained-original")
		if replacement == "run-junk" {
			retained = filepath.Join(cfg.StateDir, replacement)
		}
		if err := os.Rename(original, retained); err != nil {
			t.Fatal(err)
		}
		if replacement == "file" {
			publicWrite(t, original, "not a directory", 0o600)
		}
		if replacement == "symlink" {
			if err := os.Symlink(retained, original); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := ScheduleStatus(context.Background(), path); err == nil {
			t.Fatalf("accepted %s", replacement)
		}
	}
}

func TestSchedulePrivateMalformedUnicode(t *testing.T) {
	path, _ := scheduleFixture(t, suiteFixtureRecord+"\n")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	malformed := strings.Replace(string(data), `"version":1`, `"version":1,"repair_policy":{"routing_config":"\ud800","task":"debug","input_tokens":1,"output_tokens":1,"max_cost":1}`, 1)
	publicWrite(t, path, malformed, 0o600)
	if _, err := LoadScheduleConfig(context.Background(), path); err == nil {
		t.Fatal("lossy Unicode accepted")
	}
}

func TestScheduleFailureCreatesBoundedPlanAndKeepsBlocked(t *testing.T) {
	requireScheduling(t)
	for _, budget := range []float64{0.01, 0} {
		path, cfg := scheduleFixture(t, "")
		policy := repairTestPolicy(t)
		policy.MaxCost = budget
		cfg.RepairPolicy = &policy
		writeScheduleFixture(t, path, cfg)
		report, err := RunSchedule(context.Background(), path, buildid.Identity{})
		if err == nil || report == nil || report.Verified || report.Status != "failed" || report.Suite == nil {
			t.Fatalf("failure %+v %v", report, err)
		}
		data, readErr := os.ReadFile(filepath.Join(cfg.StateDir, "run-000001", "repairs", "plan.json"))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var plan RepairPlan
		if err := json.Unmarshal(data, &plan); err != nil {
			t.Fatal(err)
		}
		if len(plan.Jobs) != 1 {
			t.Fatalf("jobs %+v", plan)
		}
		if budget == 0 && !errors.Is(err, ErrRepairsBlocked) {
			t.Fatalf("lost blocked status %v", err)
		}
		if plan.Policy.RoutingConfig == policy.RoutingConfig {
			t.Fatal("planner did not use snapshot")
		}
	}
}

func TestScheduleCompletedFailureRetainsPlanAfterCancellation(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, "")
	policy := repairTestPolicy(t)
	cfg.RepairPolicy = &policy
	writeScheduleFixture(t, path, cfg)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := func(runCtx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		report, err := RunSuite(runCtx, opts)
		cancel()
		return report, err
	}
	report, err := scheduleTick(ctx, path, true, fixedScheduleTime, runner)
	if err == nil || report.Status != "failed" {
		t.Fatalf("failure %+v %v", report, err)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "run-000001", "repairs", "plan.json")); err != nil {
		t.Fatal("cancellation discarded failure triage:", err)
	}
}

func TestScheduleInterruptedClockRollbackKeepsValidState(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.LastAttempt.Status = "running"
	state.LastAttempt.FinishedAt = time.Time{}
	state.ConsecutiveFailures = 1
	if err := saveScheduleJSON(t.Context(), root, "state.json", state); err != nil {
		t.Fatal(err)
	}
	clock := func() time.Time { return fixedScheduleTime().Add(-time.Hour) }
	report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || report.Status != "cooldown" || report.LastAttempt.FinishedAt.Before(report.LastAttempt.StartedAt) {
		t.Fatalf("clock rollback %+v %v", report, err)
	}
	if _, err := readScheduleState(context.Background(), root); err != nil {
		t.Fatal("rollback corrupted state:", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulePruneOldestPositive(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.MaxRuns = 5
	writeScheduleFixture(t, path, cfg)
	now := fixedScheduleTime()
	for i := 1; i <= 5; i++ {
		clock := func() time.Time { return now.Add(time.Duration(i) * time.Hour) }
		report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
		if err != nil || !report.Verified || report.Attempts != i {
			t.Fatalf("run %d failed: %+v %v", i, report, err)
		}
	}
	cfg.Retention = "prune-oldest"
	cfg.MaxRuns = 4
	writeScheduleFixture(t, path, cfg)

	clock := func() time.Time { return now.Add(6 * time.Hour) }
	report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || !report.Verified {
		t.Fatalf("tick 6 failed: %+v %v", report, err)
	}
	if report.Attempts != 6 || report.PrunedThrough != 1 {
		t.Fatalf("unexpected state: attempts=%d pruned_through=%d", report.Attempts, report.PrunedThrough)
	}
	if len(report.PrunedRuns) != 1 || report.PrunedRuns[0] != "run-000001" {
		t.Fatalf("expected pruned_runs [run-000001], got %v", report.PrunedRuns)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "run-000001")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("run-000001 not removed: %v", err)
	}
	for i := 2; i <= 6; i++ {
		if _, err := os.Stat(filepath.Join(cfg.StateDir, scheduleRunName(i))); err != nil {
			t.Fatalf("run %d missing: %v", i, err)
		}
	}
}

func TestSchedulePruneOldestBoundaryEntries(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.Retention = "prune-oldest"
	cfg.MaxRuns = 10
	writeScheduleFixture(t, path, cfg)
	now := fixedScheduleTime()
	for i := 1; i <= 2; i++ {
		clock := func() time.Time { return now.Add(time.Duration(i) * time.Hour) }
		if _, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner); err != nil {
			t.Fatal(err)
		}
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := scanScheduleUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	origMax := maxScheduleEntries
	defer func() { maxScheduleEntries = origMax }()
	maxScheduleEntries = usage.entries * 2

	clock := func() time.Time { return now.Add(3 * time.Hour) }
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.PrunedRuns) != 0 {
		t.Fatalf("expected 0 pruned runs at exact boundary, got %v", status.PrunedRuns)
	}

	extra := filepath.Join(cfg.StateDir, "run-000001", "extra.txt")
	if err := os.WriteFile(extra, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || !report.Verified {
		t.Fatalf("tick with boundary+1 failed: %+v %v", report, err)
	}
	if len(report.PrunedRuns) != 1 || report.PrunedRuns[0] != "run-000001" {
		t.Fatalf("expected run-000001 pruned when above boundary, got %v", report.PrunedRuns)
	}
}

func TestScheduleBlockModeEntryCapResourceBlocked(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.Retention = "block"
	writeScheduleFixture(t, path, cfg)
	now := fixedScheduleTime()
	if _, err := scheduleTick(context.Background(), path, true, func() time.Time { return now }, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := scanScheduleUsage(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	origMax := maxScheduleEntries
	defer func() { maxScheduleEntries = origMax }()
	maxScheduleEntries = usage.entries - 1

	later := func() time.Time { return now.Add(2 * time.Hour) }
	report, err := scheduleTick(context.Background(), path, true, later, verifiedScheduleRunner)
	if err != nil {
		t.Fatalf("expected nil error on resource_blocked, got %v", err)
	}
	if report.Status != "resource_blocked" {
		t.Fatalf("expected status resource_blocked, got %s", report.Status)
	}
	expectedReason := fmt.Sprintf("retained evidence exceeds %d entries", maxScheduleEntries)
	if report.Reason != expectedReason {
		t.Fatalf("expected reason %q, got %q", expectedReason, report.Reason)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "run-000001")); err != nil {
		t.Fatalf("expected run-000001 retained, stat failed: %v", err)
	}
}

func TestSchedulePrunedThroughValidation(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.PrunedThrough = state.Attempts + 1
	if err := validateScheduleState(state, cfg.StateDir); err == nil {
		t.Fatal("pruned_through > attempts was accepted")
	}
	state.PrunedThrough = -1
	if err := validateScheduleState(state, cfg.StateDir); err == nil {
		t.Fatal("negative pruned_through was accepted")
	}
	state.Attempts = 3
	state.PrunedThrough = 1
	state.LastAttempt.Number = 3
	state.LastAttempt.ArtifactDir = filepath.Join(cfg.StateDir, "run-000003")
	if err := saveScheduleJSON(t.Context(), root, "state.json", state); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.StateDir, "run-000002"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(cfg.StateDir, "run-000004"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := scanScheduleUsage(context.Background(), root); err == nil {
		t.Fatal("gap was accepted by scanScheduleUsage")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSchedulePruneOldestDryRunDeletesNothing(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.MaxRuns = 5
	writeScheduleFixture(t, path, cfg)
	now := fixedScheduleTime()
	for i := 1; i <= 5; i++ {
		clock := func() time.Time { return now.Add(time.Duration(i) * time.Hour) }
		if _, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Retention = "prune-oldest"
	cfg.MaxRuns = 4
	writeScheduleFixture(t, path, cfg)

	status, err := ScheduleStatus(context.Background(), path)
	if err != nil {
		t.Fatalf("ScheduleStatus failed: %v", err)
	}
	if len(status.PrunedRuns) != 1 || status.PrunedRuns[0] != "run-000001" {
		t.Fatalf("expected PrunedRuns [run-000001], got %v", status.PrunedRuns)
	}
	if _, err := os.Stat(filepath.Join(cfg.StateDir, "run-000001")); err != nil {
		t.Fatalf("run-000001 missing after dry run: %v", err)
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if state.PrunedThrough != 0 {
		t.Fatalf("expected disk state pruned_through 0, got %d", state.PrunedThrough)
	}
}

func TestScheduleOldStateLoadsWithoutPrunedThrough(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	if _, err := scheduleTick(context.Background(), path, true, fixedScheduleTime, verifiedScheduleRunner); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(cfg.StateDir, "state.json")
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if err := json.Unmarshal(data, &obj); err != nil {
		t.Fatal(err)
	}
	delete(obj, "pruned_through")
	clean, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, clean, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatalf("readScheduleState failed on old state: %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if state.PrunedThrough != 0 {
		t.Fatalf("expected pruned_through 0, got %d", state.PrunedThrough)
	}
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil || status.PrunedThrough != 0 {
		t.Fatalf("ScheduleStatus failed on old state: %+v %v", status, err)
	}
}

// requireScheduling skips a case where dogfood scheduling cannot run, naming why.
//
// Scheduling identifies its own executable through Linux procfs, and the non-Linux
// build already says so: scheduleExecutableSHA returns "dogfood scheduling requires
// Linux procfs executable identity". These cases did not ask. On Windows the tick
// returned (nil, err), and TestScheduleTimingCircuitAndChangedInput then read
// report.Status off the nil report and panicked -- neither of the two states HISS-21
// permits, and a panic that takes the rest of the package's cases down with it. The
// reason printed is the production code's own, not a restatement of it.
func requireScheduling(t *testing.T) {
	t.Helper()
	if _, err := scheduleExecutableSHA(context.Background()); err != nil {
		t.Skipf("scheduling unavailable on this platform: %v", err)
	}
}

// Boundary: a prune a crash interrupted after pruned_through was saved and before the directory was
// removed leaves run-000001 behind with pruned_through 1. A status run names it and deletes nothing;
// the next tick removes it and runs, instead of refusing the disagreement for good.
func TestSchedulePruneOldestResumesAnInterruptedPrune(t *testing.T) {
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	now := fixedScheduleTime()
	for i := 1; i <= 3; i++ {
		clock := func() time.Time { return now.Add(time.Duration(i) * time.Hour) }
		if _, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner); err != nil {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	cfg.Retention = "prune-oldest"
	writeScheduleFixture(t, path, cfg)
	root, err := openScheduleState(context.Background(), cfg.StateDir, false)
	if err != nil {
		t.Fatal(err)
	}
	state, err := readScheduleState(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	state.PrunedThrough = 1
	if err := saveScheduleJSON(t.Context(), root, "state.json", state); err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	leftover := filepath.Join(cfg.StateDir, "run-000001")
	clock := func() time.Time { return now.Add(4 * time.Hour) }
	status, err := scheduleTick(context.Background(), path, false, clock, verifiedScheduleRunner)
	if err != nil || len(status.PrunedRuns) != 1 || status.PrunedRuns[0] != "run-000001" {
		t.Fatalf("status run: %+v %v; want run-000001 named", status, err)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("status run deleted the leftover: %v", err)
	}
	report, err := scheduleTick(context.Background(), path, true, clock, verifiedScheduleRunner)
	if err != nil || !report.Verified || report.Attempts != 4 {
		t.Fatalf("tick after the interrupted prune: %+v %v", report, err)
	}
	if _, err := os.Stat(leftover); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("leftover run-000001 not removed: %v", err)
	}
}
