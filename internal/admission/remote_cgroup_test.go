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

// listCores returns a pointer to the provided integer for testing optional core configuration.
func listCores(n int) *int { return &n }

// TestMeasuredExecutorCores_Positive_ListAndCgroup proves that core count resolves to the lower of list cores and cgroup quota.
func TestMeasuredExecutorCores_Positive_ListAndCgroup(t *testing.T) {
	got, source, err := measuredExecutorCores("home", listCores(30), []byte("1200000 100000"))
	if err != nil || got != 12 || source != "list and pod cpu.max" {
		t.Fatalf("cores = %d source %q err %v", got, source, err)
	}
}

// TestMeasuredExecutorCores_Negative_NoListAndUnlimitedCPU proves that missing list cores with unlimited cgroup CPU fails closed.
func TestMeasuredExecutorCores_Negative_NoListAndUnlimitedCPU(t *testing.T) {
	for _, raw := range []string{"max", "max 100000"} {
		_, _, err := measuredExecutorCores("home", nil, []byte(raw))
		if err == nil || err.Error() != "executor home: no core count (list cores missing, cpu.max unlimited)" {
			t.Fatalf("unlimited (%q) missing list: %v", raw, err)
		}
	}
}

// TestMeasuredExecutorCores_Boundary_SingleSource proves fallback to list cores or cgroup quotas when only one source is present.
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

// TestRemoteExecutorHeadroom_Positive_CgroupCapsMemAvailable proves that cgroup memory limits cap available host RAM headroom.
func TestRemoteExecutorHeadroom_Positive_CgroupCapsMemAvailable(t *testing.T) {
	stats := sentinel.HostStats{RAMFreeBytes: 20 * gib}
	maxBytes := []byte(strconv.FormatUint(16*gib, 10))
	cur := []byte(strconv.FormatUint(10*gib, 10))
	got, err := remoteExecutorHeadroom("home", stats, maxBytes, cur)
	if err != nil || got != 6*gib {
		t.Fatalf("headroom = %d err %v", got, err)
	}
}

// TestRemoteExecutorHeadroom_Negative_UnreadableCgroup proves that missing or unlimited cgroup memory values produce an error.
func TestRemoteExecutorHeadroom_Negative_UnreadableCgroup(t *testing.T) {
	stats := sentinel.HostStats{RAMFreeBytes: 20 * gib}
	if _, err := remoteExecutorHeadroom("home", stats, []byte("__MISSING__"), []byte("0")); err == nil {
		t.Fatal("unreadable memory.max accepted")
	}
	if _, err := remoteExecutorHeadroom("home", stats, []byte("max"), []byte("max")); err == nil {
		t.Fatal("unlimited memory.current accepted")
	}
}

// TestLimitsMemoryMaxBytes_Boundary_StepDefault proves parsing of standard memory limits into exact byte quantities.
func TestLimitsMemoryMaxBytes_Boundary_StepDefault(t *testing.T) {
	got, err := LimitsMemoryMaxBytes(Limits{MemoryHigh: "16G", MemoryMax: "24G", MemorySwapMax: "4G"})
	if err != nil || got != 24*gib {
		t.Fatalf("bytes = %d err %v", got, err)
	}
}

// TestStepParallelismJobs_Positive_TwoConcurrent proves that job slots divide evenly among concurrent admitted steps.
func TestStepParallelismJobs_Positive_TwoConcurrent(t *testing.T) {
	if got := StepParallelismJobs(12, 2); got != 6 {
		t.Fatalf("jobs = %d", got)
	}
	if got := StepParallelismJobs(12, 1); got != 12 {
		t.Fatalf("one step jobs = %d", got)
	}
}

// exit255Error runs a shell command that exits with status 255 to produce a test ExitError.
func exit255Error(t *testing.T) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := util.RunCommand(ctx, "", "sh", "-c", "exit 255")
	return err
}

// TestRemoteRunFailure_Boundary_StepExit255NotLost proves that a step exiting 255 with an exit marker is reported as a step failure rather than an eviction.
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

// TestRemoteRunFailure_Negative_SSH255WithoutMarker proves that an SSH exit code of 255 without an exit marker is classified as an eviction.
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

// plantStaleExecutorLease writes an unheld lock file to simulate an orphaned executor lease in tests.
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

// assertSingleStepAfterRelease verifies that releasing a lease leaves the directory clean and permits subsequent acquisition.
func assertSingleStepAfterRelease(t *testing.T, leaseDir string) {
	t.Helper()
	assertNoLeaseFiles(t, leaseDir)
	assertSingleLeaseRound(t)
	assertNoLeaseFiles(t, leaseDir)
}

// assertSingleLeaseRound acquires and immediately releases an executor lease to verify clean state.
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

// TestAdmitExecutorLease_Negative_UnreadableLeaseDirectoryRefused proves that an unreadable lease directory is refused with an informative error.
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

// assertNoLeaseFiles asserts that no lease lock files remain in the specified directory.
func assertNoLeaseFiles(t *testing.T, leaseDir string) {
	t.Helper()
	entries, err := os.ReadDir(leaseDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("lease files remained after step end: %v %v", entries, err)
	}
}

// runSubprocessLeaseHolder runs the lease-holding loop when invoked as a subprocess holder.
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

// startSubprocessLeaseHolder launches a background subprocess holding a lease and returns a stopper function.
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

// TestAdmitExecutorLease_Boundary_TempDirFallbackAndCap proves fallback to temporary directory leases and adherence to scan file limits.
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

// TestMeasuredExecutorCores_Negative_NprocMutant proves that unconstrained nproc values are not mistakenly used over cgroup limits.
func TestMeasuredExecutorCores_Negative_NprocMutant(t *testing.T) {
	got, _, err := measuredExecutorCores("home", listCores(30), []byte("1200000 100000"))
	if err != nil || got == 30 {
		t.Fatalf("nproc-style cores = %d, err %v", got, err)
	}
}
