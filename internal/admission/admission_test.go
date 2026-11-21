// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package admission

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/sentinel"
)

const hogEnv = "ADMISSION_TEST_HOG"

const testExecutor = `{"name":"edge","ssh_host":"edge.example.invalid","ssh_user":"builder","workspace_root":"/scratch/builds","cores":1,"memory_gib":1,"class":"cpu-offload"}`

// TestReadExecutors_JSONSchema proves that ReadExecutors enforces the JSON schema, catches duplicates, and rejects malformed fields.
func TestReadExecutors_JSONSchema(t *testing.T) {
	for _, tc := range []struct{ name, json, want string }{
		{"minimum", "[" + testExecutor + "]", ""},
		{"optional cores", "[" + strings.Replace(testExecutor, `"cores":1,`, "", 1) + "]", ""},
		{"duplicate key", "[" + strings.Replace(testExecutor, `"name":`, `"Name":"other","name":`, 1) + "]", "duplicate"},
		{"duplicate name", "[" + testExecutor + "," + testExecutor + "]", "duplicate executor name"},
		{"unknown property", "[" + strings.Replace(testExecutor, `"cores":`, `"cpus":`, 1) + "]", "unknown field"},
		{"unknown class", "[" + strings.Replace(testExecutor, "cpu-offload", "gpu-offload", 1) + "]", "unknown executor class"},
		{"invalid host", "[" + strings.Replace(testExecutor, "edge.example.invalid", "-F", 1) + "]", "invalid SSH"},
		{"invalid workspace", "[" + strings.Replace(testExecutor, "/scratch/builds", "/scratch/../builds", 1) + "]", "workspace_root"},
		{"zero cores", "[" + strings.Replace(testExecutor, `"cores":1`, `"cores":0`, 1) + "]", "cores must be"},
		{"empty", "[]", "requires 1"},
		{"oversized", strings.Repeat(" ", maxExecutorListBytes+1), "exceeds the read limit"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "executors.json")
			if err := os.WriteFile(file, []byte(tc.json), 0o600); err != nil {
				t.Fatal(err)
			}
			executors, err := ReadExecutors(file)
			assertReadExecutorsCase(t, tc.name, executors, err, tc.want)
		})
	}
}

// assertReadExecutorsCase asserts that ReadExecutors returns the expected error substring or valid executor attributes.
func assertReadExecutorsCase(t *testing.T, name string, executors []Executor, err error, want string) {
	t.Helper()
	if want != "" {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("wanted %q, got %v", want, err)
		}
		return
	}
	wantCores := name != "optional cores"
	if err != nil || len(executors) != 1 || executors[0].MemoryGiB != 1 || (executors[0].Cores != nil) != wantCores {
		t.Fatalf("executor %s: %+v, %v", name, executors, err)
	}
}

// TestRemoteReadings_RequireMeasuredThresholds proves that admission statistics parsing requires valid measured units and numeric thresholds.
func TestRemoteReadings_RequireMeasuredThresholds(t *testing.T) {
	const memory = "MemTotal: 12582912 kB\nMemAvailable: 12582912 kB\nSwapTotal: 15728640 kB\nSwapFree: 15728640 kB\n"
	for _, tc := range []struct {
		name, load, memory string
		admit, invalid     bool
	}{
		{"exact floor", "1.25 0 0", memory, true, false},
		{"zero available", "0 0 0", strings.Replace(memory, "MemAvailable: 12582912", "MemAvailable: 0", 1), false, false},
		{"missing available", "0 0 0", strings.Replace(memory, "MemAvailable", "MemFree", 1), false, true},
		{"invalid units", "0 0 0", strings.Replace(memory, "MemAvailable: 12582912 kB", "MemAvailable: 12582912 MB", 1), false, true},
		{"NaN load", "NaN 0 0", memory, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stats, err := sentinel.ReadLinuxAdmissionStats(strings.NewReader(tc.load), strings.NewReader(tc.memory))
			if (err != nil) != tc.invalid {
				t.Fatalf("measurements %+v, error %v", stats, err)
			}
			if err == nil {
				policy := DefaultPolicy()
				policy.MaxLoad1 = defaultMaxLoadPerCore
				if verdict := Judge(stats, policy); verdict.Admit != tc.admit {
					t.Fatalf("thresholds: %+v", verdict)
				}
			}
		})
	}
}

// TestMain lets the test binary re-run itself as a memory hog for the planted spike.
func TestMain(m *testing.M) {
	if os.Getenv(hogEnv) == "1" {
		hog := make([]byte, 1<<30)
		for i := 0; i < len(hog); i += 4096 {
			hog[i] = 1
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

var policy = Policy{MinAvailableBytes: 12 * gib, MinSwapFreeBytes: 15 * gib, MaxLoad1: 40}

func host(availGiB, swapGiB uint64, load float64) sentinel.HostStats {
	return sentinel.HostStats{RAMTotalBytes: 64 * gib, RAMFreeBytes: availGiB * gib, SwapMeasured: true, SwapTotalBytes: 100 * gib,
		SwapFreeBytes: swapGiB * gib, CPULoadMeasured: true, CPULoad1Min: load}
}

// Positive: a host above every bound admits, naming what it read.
func TestJudge_Positive_AdmitsAboveEveryBound(t *testing.T) {
	stats := host(20, 50, 10)
	v := Judge(stats, policy)
	if !v.Admit || !strings.Contains(v.Reason, "available 20.0 GiB") {
		t.Fatalf("verdict %+v", v)
	}
	if got := ReadingsText(stats); got != "available 20.0 GiB, swap free 50.0 GiB" {
		t.Fatalf("readings text %q", got)
	}
}

// Negative: memory, swap or load past its bound holds the step and names which one.
func TestJudge_Negative_HoldsBelowABound(t *testing.T) {
	for want, stats := range map[string]sentinel.HostStats{
		"available 8.0 GiB < 12.0 GiB": host(8, 50, 10), "swap free 4.0 GiB < 15.0 GiB": host(20, 4, 10), "load 60.0 > 40.0": host(20, 50, 60),
	} {
		if v := Judge(stats, policy); v.Admit || v.Reason != want {
			t.Errorf("verdict %+v, want held for %q", v, want)
		}
		if got := ReadingsText(stats); !strings.Contains(got, "available") || !strings.Contains(got, "swap free") {
			t.Errorf("readings text %q", got)
		}
	}
}

// Boundary: exactly at a bound admits; unmeasured swap and load are not judged; a platform with
// no memory source admits and says so.
func TestJudge_Boundary_EdgesAndUnmeasured(t *testing.T) {
	if !Judge(host(12, 15, 40), policy).Admit {
		t.Fatal("a host exactly at the bounds was held")
	}
	s := host(20, 0, 99)
	s.SwapMeasured, s.CPULoadMeasured = false, false
	if v := Judge(s, policy); !v.Admit || !strings.Contains(v.Reason, "swap free unmeasured") {
		t.Fatalf("unmeasured swap/load verdict %+v", v)
	}
	if got := ReadingsText(s); got != "available 20.0 GiB, swap free unmeasured" {
		t.Fatalf("unmeasured readings text %q", got)
	}
	if v := Judge(sentinel.HostStats{}, policy); !v.Admit || !strings.Contains(v.Reason, "admitted unjudged") {
		t.Fatalf("no memory source verdict %+v", v)
	}
}

func waiter(readings []sentinel.HostStats, readErr error, maxWait time.Duration, log *bytes.Buffer) (Waiter, *int) {
	calls := 0
	return Waiter{Policy: policy, MaxWait: maxWait, Poll: time.Millisecond, Log: log,
		Read: func() (sentinel.HostStats, error) {
			calls++
			if readErr != nil {
				return sentinel.HostStats{}, readErr
			}
			return readings[min(calls-1, len(readings)-1)], nil
		},
		Sleep: func(context.Context, time.Duration) error { return nil }}, &calls
}

// Positive (planted): a step started under the threshold is held, with a line saying why, and
// admitted once the host frees memory.
func TestWait_Positive_HeldThenAdmitted(t *testing.T) {
	var log bytes.Buffer
	w, calls := waiter([]sentinel.HostStats{host(4, 50, 10), host(4, 50, 10), host(20, 50, 10)}, nil, time.Hour, &log)
	v, err := w.Wait(context.Background(), "verify-all")
	if err != nil || !v.Admit || *calls != 3 || strings.Count(log.String(), "admission wait verify-all: available 4.0 GiB") != 2 {
		t.Fatalf("verdict %+v, err %v, %d readings, log %q", v, err, *calls, log.String())
	}
}

// Negative: a host that stays under the threshold refuses the step once MaxWait passes; a failed
// reading refuses at once (fail closed).
func TestWait_Negative_RefusedOnTimeoutAndReadError(t *testing.T) {
	var log bytes.Buffer
	w, _ := waiter([]sentinel.HostStats{host(4, 50, 10)}, nil, 0, &log)
	if _, err := w.Wait(context.Background(), "gate"); err == nil || !strings.Contains(err.Error(), "still held") {
		t.Fatalf("a host under the threshold was not refused: %v", err)
	}
	w, calls := waiter(nil, errors.New("meminfo unreadable"), time.Hour, &log)
	if _, err := w.Wait(context.Background(), "gate"); err == nil || !strings.Contains(err.Error(), "refused") || *calls != 1 {
		t.Fatalf("a failed reading was not refused at once: %v after %d readings", err, *calls)
	}
}

// Boundary: a cancelled context ends the wait with the context's error.
func TestWait_Boundary_CancelledContext(t *testing.T) {
	var log bytes.Buffer
	w, _ := waiter([]sentinel.HostStats{host(4, 50, 10)}, nil, time.Hour, &log)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w.Sleep = SleepContext
	if _, err := w.Wait(ctx, "gate"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: %v", err)
	}
}

// Positive and negative: limits systemd reads as intended pass; fractions, signs and missing
// units fail.
func TestLimitsCheck(t *testing.T) {
	if err := (Limits{MemoryHigh: "16G", MemoryMax: "512M", MemorySwapMax: "0"}).Check(); err != nil {
		t.Fatal(err)
	}
	if err := (Limits{MemoryHigh: "16G", MemoryMax: "24G", MemorySwapMax: "4G"}).Check(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Limits{{"16", "24G", "0"}, {"1.5G", "24G", "0"}, {"16G", "-1G", "0"}, {"", "24G", "0"}, {"16G", "infinity", "0"},
		{"16G", "24G", ""}, {"16G", "24G", "-1"}} {
		if bad.Check() == nil {
			t.Errorf("limits %+v accepted", bad)
		}
	}
}

// Boundary: the scope argv carries both limits and --expand-environment=no, and a label with
// path characters becomes a safe, bounded unit name.
func TestScopeArgsAndUnitName(t *testing.T) {
	args := strings.Join(ScopeArgs("admit-x", Limits{MemoryHigh: "8G", MemoryMax: "12G", MemorySwapMax: "2G"}, []string{"make", "verify-all"}), " ")
	for _, want := range []string{"--user --scope", "--expand-environment=no", "--property=MemoryHigh=8G", "--property=MemoryMax=12G",
		"--property=MemorySwapMax=2G", "-- make verify-all"} {
		if !strings.Contains(args, want) {
			t.Errorf("scope args %q lack %q", args, want)
		}
	}
	name := unitName("go test ./... -race/"+strings.Repeat("x", 80), time.Date(2026, 10, 17, 18, 0, 0, 0, time.UTC))
	if strings.ContainsAny(name, " /") || !strings.HasPrefix(name, "admit-go-test-") || len(name) > 80 {
		t.Fatalf("unit name %q", name)
	}
}

func requireScopes(t *testing.T) {
	t.Helper()
	if ok, why := ScopesAvailable(context.Background()); !ok {
		t.Skipf("no user scopes here: %s", why)
	}
}

// Positive (read-back): the step runs inside its scope, which carries the MemoryMax it was given.
func TestRun_Positive_ScopeCarriesTheLimit(t *testing.T) {
	requireScopes(t)
	var out, errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	step := Step{Label: "readback", Limits: Limits{MemoryHigh: "96M", MemoryMax: "128M", MemorySwapMax: "0"}, Stdout: &out, Stderr: &errOut,
		Argv: []string{"sh", "-c", `cat "/sys/fs/cgroup$(cut -d: -f3 /proc/self/cgroup)/memory.max"`}}
	if err := Run(ctx, step); err != nil {
		t.Fatalf("run: %v (%s)", err, errOut.String())
	}
	if got := strings.TrimSpace(out.String()); got != "134217728" {
		t.Fatalf("memory.max inside the scope = %q, want 134217728 (128M)", got)
	}
}

// Negative (planted spike): a step that tries to hold 1 GiB under MemoryMax 128M and no swap is
// killed, and only the step: the test process that started it keeps running and reads the
// step's failure. With MemoryMax alone the kernel swapped the hog out and it finished (measured
// 2026-10-17), so the swap bound is part of the containment.
func TestRun_Negative_SpikeKillsOnlyTheStep(t *testing.T) {
	requireScopes(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var errOut bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	// MemoryHigh at MemoryMax: below it the kernel throttles the hog instead of killing it, and a
	// throttled step only ends at this test's deadline (measured 2026-10-17).
	step := Step{Label: "spike", Limits: Limits{MemoryHigh: "128M", MemoryMax: "128M", MemorySwapMax: "0"}, Stdout: &bytes.Buffer{}, Stderr: &errOut,
		Argv: []string{"env", hogEnv + "=1", self, "-test.run=^$"}}
	started := time.Now()
	runErr := Run(ctx, step)
	var exit *exec.ExitError
	if !errors.As(runErr, &exit) || exit.ExitCode() == 0 || ctx.Err() != nil {
		t.Fatalf("the spiking step was not killed by its limit: %v, context %v, after %s (%s)", runErr, ctx.Err(), time.Since(started), errOut.String())
	}
	if os.Getpid() <= 0 {
		t.Fatal("unreachable: the test process is gone")
	}
}

// Negative: an empty command and limits that do not parse are refused before anything starts.
func TestRun_Negative_RefusedBeforeStart(t *testing.T) {
	if err := Run(context.Background(), Step{Label: "x", Limits: Limits{MemoryHigh: "8G", MemoryMax: "12G", MemorySwapMax: "0"}}); err == nil {
		t.Fatal("an empty command ran")
	}
	if err := Run(context.Background(), Step{Label: "x", Argv: []string{"true"}, Limits: Limits{MemoryHigh: "8", MemoryMax: "12G", MemorySwapMax: "0"}}); err == nil {
		t.Fatal("unparsable limits ran")
	}
}
