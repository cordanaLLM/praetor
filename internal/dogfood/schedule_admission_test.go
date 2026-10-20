package dogfood

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/admission"
	"github.com/cordanaLLM/praetor/internal/sentinel"
)

func admittedScheduleHost() (sentinel.HostStats, error) {
	return sentinel.HostStats{RAMTotalBytes: 64 << 30, RAMFreeBytes: 32 << 30,
		SwapMeasured: true, SwapTotalBytes: 64 << 30, SwapFreeBytes: 32 << 30,
		CPULoadMeasured: true}, nil
}

func scheduleAdmissionFixture(t *testing.T) (string, *ScheduleConfig, []byte) {
	t.Helper()
	requireScheduling(t)
	path, cfg := scheduleFixture(t, suiteFixtureRecord+"\n")
	report, err := scheduleTick(t.Context(), path, true, fixedScheduleTime, failedScheduleRunner, admittedScheduleHost)
	if err == nil || report.Status != "failed" || report.ConsecutiveFailures != 1 {
		t.Fatalf("seed failed attempt: %+v %v", report, err)
	}
	return path, cfg, scheduleStateBytes(t, cfg)
}

func scheduleStateBytes(t *testing.T, cfg *ScheduleConfig) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(cfg.StateDir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func assertScheduleNoAttempt(t *testing.T, cfg *ScheduleConfig, before []byte) {
	t.Helper()
	if after := scheduleStateBytes(t, cfg); !bytes.Equal(before, after) {
		t.Fatalf("state changed: before %s after %s", before, after)
	}
	if _, err := os.Lstat(filepath.Join(cfg.StateDir, "run-000002")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected attempt directory: %v", err)
	}
}

func dueScheduleTime() time.Time { return fixedScheduleTime().Add(time.Minute) }

func TestScheduleHostHeldPreservesAttemptState(t *testing.T) {
	path, cfg, before := scheduleAdmissionFixture(t)
	stats, err := admittedScheduleHost()
	if err != nil {
		t.Fatal(err)
	}
	bound := admission.HostCappedPolicy(stats).MaxLoad1
	stats.CPULoad1Min = bound + 1
	reads, runs := 0, 0
	read := func() (sentinel.HostStats, error) { reads++; return stats, nil }
	runner := func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		runs++
		return verifiedScheduleRunner(ctx, opts)
	}
	report, err := scheduleTick(t.Context(), path, true, dueScheduleTime, runner, read)
	wantReason := fmt.Sprintf("load %.1f > %.1f", bound+1, bound)
	if err != nil || report.Status != "host_held" || report.HostReason != wantReason {
		t.Fatalf("host hold: %+v %v; want %q", report, err, wantReason)
	}
	if reads != 1 || runs != 0 || report.Attempts != 1 || report.ConsecutiveFailures != 1 || report.Verified {
		t.Fatalf("reads %d runs %d report %+v", reads, runs, report)
	}
	assertScheduleNoAttempt(t, cfg, before)
	assertScheduleHostJSON(t, report, wantReason)
}

func assertScheduleHostJSON(t *testing.T, report *ScheduleReport, wantReason string) {
	t.Helper()
	data, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var encoded struct {
		Status     string `json:"status"`
		HostReason string `json:"host_reason"`
	}
	if err := json.Unmarshal(data, &encoded); err != nil {
		t.Fatal(err)
	}
	if encoded.Status != "host_held" || encoded.HostReason != wantReason {
		t.Fatalf("missing host hold in JSON: %s", data)
	}
}

func TestScheduleHostAdmissionAtLoadBoundaryRunsOnce(t *testing.T) {
	path, _, _ := scheduleAdmissionFixture(t)
	stats, err := admittedScheduleHost()
	if err != nil {
		t.Fatal(err)
	}
	stats.CPULoad1Min = admission.HostCappedPolicy(stats).MaxLoad1
	reads, runs := 0, 0
	read := func() (sentinel.HostStats, error) { reads++; return stats, nil }
	runner := func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		runs++
		return verifiedScheduleRunner(ctx, opts)
	}
	report, err := scheduleTick(t.Context(), path, true, dueScheduleTime, runner, read)
	if err != nil || report.Status != "verified" || !report.Verified || report.Attempts != 2 || report.ConsecutiveFailures != 0 {
		t.Fatalf("admitted at load boundary: %+v %v", report, err)
	}
	if reads != 1 || runs != 1 || !strings.Contains(report.HostReason, "load") {
		t.Fatalf("reads %d runs %d reason %q", reads, runs, report.HostReason)
	}
}

func TestScheduleHostReadErrorPreservesAttemptState(t *testing.T) {
	path, cfg, before := scheduleAdmissionFixture(t)
	wantErr := errors.New("host metrics unavailable")
	reads, runs := 0, 0
	read := func() (sentinel.HostStats, error) { reads++; return sentinel.HostStats{}, wantErr }
	runner := func(ctx context.Context, opts SuiteOptions) (*SuiteReport, error) {
		runs++
		return verifiedScheduleRunner(ctx, opts)
	}
	report, err := scheduleTick(t.Context(), path, true, dueScheduleTime, runner, read)
	if !errors.Is(err, wantErr) || !strings.Contains(err.Error(), "host") {
		t.Fatalf("host read error lost: %+v %v", report, err)
	}
	if reads != 1 || runs != 0 || report.Attempts != 1 || report.ConsecutiveFailures != 1 || report.Verified {
		t.Fatalf("reads %d runs %d report %+v", reads, runs, report)
	}
	assertScheduleNoAttempt(t, cfg, before)
}

func TestScheduleStatusDoesNotReadHost(t *testing.T) {
	path, cfg, before := scheduleAdmissionFixture(t)
	read := func() (sentinel.HostStats, error) {
		t.Error("status read host")
		return sentinel.HostStats{}, errors.New("host must not be read")
	}
	runner := func(context.Context, SuiteOptions) (*SuiteReport, error) {
		t.Error("status ran suite")
		return nil, errors.New("suite must not run")
	}
	report, err := scheduleTick(t.Context(), path, false, dueScheduleTime, runner, read)
	if err != nil || report.Status != "due" || report.HostReason != "" {
		t.Fatalf("status: %+v %v", report, err)
	}
	status, err := ScheduleStatus(t.Context(), path)
	if err != nil || status.Status != "due" || status.HostReason != "" {
		t.Fatalf("public status: %+v %v", status, err)
	}
	assertScheduleNoAttempt(t, cfg, before)
}
