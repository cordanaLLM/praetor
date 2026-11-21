// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

const holdExecutorLeaseEnv = "PRAETOR_TEST_HOLD_EXECUTOR_LEASE"

func listCores(n int) *int { return &n }

func TestMeasuredExecutorCores_Positive_ListAndCgroup(t *testing.T) {
	got, source, err := measuredExecutorCores("home", listCores(30), []byte("1200000 100000"))
	if err != nil || got != 12 || source != "list and pod cpu.max" {
		t.Fatalf("cores = %d source %q err %v", got, source, err)
	}
}

func TestMeasuredExecutorCores_Negative_NoListAndUnlimitedCPU(t *testing.T) {
	for _, raw := range []string{"max", "max 100000"} {
		_, _, err := measuredExecutorCores("home", nil, []byte(raw))
		if err == nil || err.Error() != "executor home: no core count (list cores missing, cpu.max unlimited)" {
			t.Fatalf("unlimited (%q) missing list: %v", raw, err)
		}
	}
}

func TestMeasuredExecutorCores_Boundary_SingleSource(t *testing.T) {
	got, source, err := measuredExecutorCores("home", listCores(8), []byte("max 100000"))
	if err != nil || got != 8 || source != "list cores" {
		t.Fatalf("list only = %d %q %v", got, source, err)
	}
	got, source, err = measuredExecutorCores("home", nil, []byte("1200000 100000"))
	if err != nil || got != 12 || source != "pod cpu.max" {
		t.Fatalf("cgroup only = %d %q %v", got, source, err)
	}
	got, source, err = measuredExecutorCores("home", nil, []byte("50000 100000"))
	if err != nil || got != 1 || source != "pod cpu.max" {
		t.Fatalf("fractional cgroup floors to 1 = %d %q %v", got, source, err)
	}
}

func TestRemoteExecutorHeadroom_Positive_CgroupCapsMemAvailable(t *testing.T) {
	stats := sentinel.HostStats{RAMFreeBytes: 20 * gib}
	maxBytes := []byte(strconv.FormatUint(16*gib, 10))
	cur := []byte(strconv.FormatUint(10*gib, 10))
	got, err := remoteExecutorHeadroom("home", stats, maxBytes, cur)
	if err != nil || got != 6*gib {
		t.Fatalf("headroom = %d err %v", got, err)
	}
}

func TestRemoteExecutorHeadroom_Negative_UnreadableCgroup(t *testing.T) {
	stats := sentinel.HostStats{RAMFreeBytes: 20 * gib}
	if _, err := remoteExecutorHeadroom("home", stats, []byte("__MISSING__"), []byte("0")); err == nil {
		t.Fatal("unreadable memory.max accepted")
	}
	if _, err := remoteExecutorHeadroom("home", stats, []byte("max"), []byte("max")); err == nil {
		t.Fatal("unlimited memory.current accepted")
	}
}

func TestLimitsMemoryMaxBytes_Boundary_StepDefault(t *testing.T) {
	got, err := LimitsMemoryMaxBytes(Limits{MemoryHigh: "16G", MemoryMax: "24G", MemorySwapMax: "4G"})
	if err != nil || got != 24*gib {
		t.Fatalf("bytes = %d err %v", got, err)
	}
}

func TestStepParallelismJobs_Positive_TwoConcurrent(t *testing.T) {
	if got := StepParallelismJobs(12, 2); got != 6 {
		t.Fatalf("jobs = %d", got)
	}
	if got := StepParallelismJobs(12, 1); got != 12 {
		t.Fatalf("one step jobs = %d", got)
	}
}

func exit255Error(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := util.RunCommand(ctx, "", "sh", "-c", "exit 255")
	return err
}

func TestRemoteRunFailure_Boundary_StepExit255NotLost(t *testing.T) {
	w := &remoteStepStderr{}
	if _, err := w.Write([]byte("praetor-admit-step-complete ")); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("255\n")); err != nil {
		t.Fatal(err)
	}
	code, ferr := remoteRunFailure(context.Background(), "home", w.stepFinished, exit255Error(t))
	if ferr != nil || code != 255 {
		t.Fatalf("step 255 = %d %v", code, ferr)
	}
}

func TestRemoteRunFailure_Negative_SSH255WithoutMarker(t *testing.T) {
	code, ferr := remoteRunFailure(context.Background(), "home", false, exit255Error(t))
	if code != 255 || ferr == nil || ferr.Error() != "executor home lost the step mid-run (evicted or unreachable)" {
		t.Fatalf("eviction = %d %v", code, ferr)
	}
}

// Negative (mutant): a naive nproc=30 reading must not become build parallelism when cpu.max caps at 12.
func TestAdmitExecutorLease_Positive_TwoConcurrentSteps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("remote executor leases require Unix advisory file locks")
	}
	if runSubprocessLeaseHolder(t) {
		return
	}
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	leaseDir, stale := plantStaleExecutorLease(t, runtimeDir, "home")
	stopOther := startSubprocessLeaseHolder(t, "home", "TestAdmitExecutorLease_Positive_TwoConcurrentSteps")
	concurrent, release, err := AdmitExecutorLease("home")
	if err != nil {
		t.Fatal(err)
	}
	if concurrent != 2 || StepParallelismJobs(12, concurrent) != 6 {
		t.Fatalf("two concurrent steps across processes: concurrent=%d jobs=%d", concurrent, StepParallelismJobs(12, concurrent))
	}
	if _, err := os.Stat(stale); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale lock file was not removed: %v", err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	stopOther()
	assertSingleStepAfterRelease(t, leaseDir)
}

func plantStaleExecutorLease(t *testing.T, runtimeDir, executor string) (string, string) {
	t.Helper()
	leaseDir := filepath.Join(runtimeDir, "praetor-admit-leases", executor)
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(leaseDir, "999999-stale.lock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return leaseDir, stale
}

func assertSingleStepAfterRelease(t *testing.T, leaseDir string) {
	t.Helper()
	assertNoLeaseFiles(t, leaseDir)
	assertSingleLeaseRound(t)
	assertNoLeaseFiles(t, leaseDir)
}

func assertSingleLeaseRound(t *testing.T) {
	t.Helper()
	concurrent, release, err := AdmitExecutorLease("home")
	if err != nil || concurrent != 1 || StepParallelismJobs(12, concurrent) != 12 {
		t.Fatalf("single step lease round: concurrent=%d err=%v", concurrent, err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
}

func TestAdmitExecutorLease_Negative_UnreadableLeaseDirectoryRefused(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("unreadable directory permission check requires non-root Unix")
	}
	runtimeDir := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	leaseDir := filepath.Join(runtimeDir, "praetor-admit-leases", "home")
	// nosemgrep: go.lang.correctness.permissions.file_permission.incorrect-default-permission -- a lease directory needs the owner search bit; 0o700 is least privilege
	if err := os.MkdirAll(leaseDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(leaseDir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Remove(leaseDir); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Error(err)
		}
	})
	_, release, err := AdmitExecutorLease("home")
	if release != nil {
		if closeErr := release(); closeErr != nil {
			t.Error(closeErr)
		}
	}
	if err == nil || !strings.Contains(err.Error(), "executor home: lease directory") {
		t.Fatalf("unreadable lease directory did not refuse by name: %v", err)
	}
}

func assertNoLeaseFiles(t *testing.T, leaseDir string) {
	t.Helper()
	entries, err := os.ReadDir(leaseDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("lease files remained after step end: %v %v", entries, err)
	}
}

func runSubprocessLeaseHolder(t *testing.T) bool {
	t.Helper()
	name := os.Getenv(holdExecutorLeaseEnv)
	if name == "" {
		return false
	}
	_, release, err := AdmitExecutorLease(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stdout.Write([]byte{1}); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
	if err := release(); err != nil {
		t.Fatal(err)
	}
	return true
}

func startSubprocessLeaseHolder(t *testing.T, executor, testName string) func() {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	readyR, readyW := io.Pipe()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	var stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		runErr := util.RunCommandPassthrough(ctx, "", "env", stdinR, readyW, &stderr,
			holdExecutorLeaseEnv+"="+executor, self, "-test.run=^"+testName+"$")
		if closeErr := readyW.CloseWithError(runErr); closeErr != nil {
			runErr = errors.Join(runErr, closeErr)
		}
		done <- runErr
	}()
	var ack [1]byte
	if _, err := io.ReadFull(readyR, ack[:]); err != nil {
		cancel()
		t.Fatalf("subprocess lease holder failed to start: %v (%s)", errors.Join(err, stdinW.Close(), stdinR.Close()), stderr.String())
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			defer cancel()
			closeErr := stdinW.Close()
			_, drainErr := io.Copy(io.Discard, readyR)
			if err := errors.Join(closeErr, drainErr, <-done, stdinR.Close(), readyR.Close()); err != nil {
				t.Fatalf("subprocess lease holder: %v (%s)", err, stderr.String())
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

func TestAdmitExecutorLease_Boundary_TempDirFallbackAndCap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("remote executor leases require Unix advisory file locks")
	}
	t.Setenv("XDG_RUNTIME_DIR", "")
	tmpDir := t.TempDir()
	t.Setenv("TMPDIR", tmpDir)
	assertSingleLeaseRound(t)
	leaseDir := filepath.Join(tmpDir, "praetor-admit-"+strconv.Itoa(os.Getuid()), executorLeasesDir, "home")
	for i := range maxExecutorLeaseFiles + 4 {
		if err := os.WriteFile(filepath.Join(leaseDir, strconv.Itoa(i)+"-stale.lock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	assertSingleLeaseRound(t)
	if entries, err := os.ReadDir(leaseDir); err != nil || len(entries) != 4 {
		t.Fatalf("wanted 4 unprobed files past cap %d, got %d (%v)", maxExecutorLeaseFiles, len(entries), err)
	}
}

func TestMeasuredExecutorCores_Negative_NprocMutant(t *testing.T) {
	got, _, err := measuredExecutorCores("home", listCores(30), []byte("1200000 100000"))
	if err != nil || got == 30 {
		t.Fatalf("nproc-style cores = %d, err %v", got, err)
	}
}
