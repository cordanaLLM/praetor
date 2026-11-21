// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
	"github.com/cordanaLLM/praetor/internal/util"
)

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
	defer func() {
		ReleaseExecutorLease("home")
		ReleaseExecutorLease("home")
	}()
	if got := AdmitExecutorLease("home"); got != 1 {
		t.Fatalf("first lease = %d", got)
	}
	if got := StepParallelismJobs(12, AdmitExecutorLease("home")); got != 6 {
		t.Fatalf("two concurrent jobs = %d", got)
	}
}

func TestMeasuredExecutorCores_Negative_NprocMutant(t *testing.T) {
	got, _, err := measuredExecutorCores("home", listCores(30), []byte("1200000 100000"))
	if err != nil || got == 30 {
		t.Fatalf("nproc-style cores = %d, err %v", got, err)
	}
}
