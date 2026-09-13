package dogfood

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	if err := os.WriteFile(path, data, 0o600); err != nil {
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
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil || status.Status != "due" || status.Verified {
		t.Fatalf("status: %+v %v", status, err)
	}
	if _, err := os.Lstat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("status created state")
	}
	report, err := RunSchedule(context.Background(), path)
	if err != nil || !report.Verified || report.Suite == nil || !report.Suite.Verified || report.Attempts != 1 {
		t.Fatalf("run: %+v %v", report, err)
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

func TestScheduleTimingCircuitAndChangedInput(t *testing.T) {
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
	if err := saveScheduleJSON(root, "state.json", state); err != nil {
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
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, invalid := range []context.Context{nil, ctx} {
		if _, err := RunSchedule(invalid, path); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	if _, err := RunSchedule(ctx, path); !errors.Is(err, context.Canceled) {
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
	if err != nil || got.IntervalSeconds != 86400 || got.RetrySeconds != 3600 || got.MaxRuns != 32 || got.MaxBytes != 2<<30 {
		t.Fatalf("defaults %+v %v", got, err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadScheduleConfig(context.Background(), path); err == nil {
		t.Fatal("public schedule accepted")
	}
}

func TestScheduleStateCorruptionAndReadonly(t *testing.T) {
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
	if err != nil || info.Mode().Perm() != 0o400 {
		t.Fatal("mode broadened")
	}
}

func TestScheduleSnapshotPinsAndWithinRoot(t *testing.T) {
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
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	cfg.RunnerBinary = filepath.Join(t.TempDir(), "other-runner")
	publicWrite(t, cfg.RunnerBinary, "different executable bytes", 0o700)
	writeScheduleFixture(t, path, cfg)
	status, err := ScheduleStatus(context.Background(), path)
	if err != nil || status.RunnerSHA256 == "" || status.RunnerIdentity != "configured_binary" {
		t.Fatalf("configured status %+v %v", status, err)
	}
	if _, err := RunSchedule(context.Background(), path); err == nil {
		t.Fatal("actual process mismatch allowed")
	}
	if _, err := os.Stat(cfg.StateDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("mismatch wrote state")
	}
}

func TestScheduleRetainedAttemptTypesAndSequence(t *testing.T) {
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
	for _, budget := range []float64{0.01, 0} {
		path, cfg := scheduleFixture(t, "")
		policy := repairTestPolicy(t)
		policy.MaxCost = budget
		cfg.RepairPolicy = &policy
		writeScheduleFixture(t, path, cfg)
		report, err := RunSchedule(context.Background(), path)
		if err == nil || report.Verified || report.Status != "failed" || report.Suite == nil {
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
	if err := saveScheduleJSON(root, "state.json", state); err != nil {
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
