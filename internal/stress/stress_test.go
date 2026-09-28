package stress

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/astmerge"
	"github.com/cordanaLLM/praetor/internal/baseline"
	"github.com/cordanaLLM/praetor/internal/compiler"
	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

func TestProperty_TranspileIdempotency(t *testing.T) {
	tr := compiler.NewTranspiler()
	for i := 0; i < 50; i++ {
		content := fmt.Sprintf("# Section %d\nInvariant: HISS-%02d\n`code`\n", i, i%16+1)
		res1, err := tr.CompileContent(content)
		if err != nil {
			t.Fatalf("first compile failed: %v", err)
		}
		res2, err := tr.CompileContent(content)
		if err != nil {
			t.Fatalf("second compile failed: %v", err)
		}

		if len(res1.Files) != len(res2.Files) {
			t.Fatalf("file count mismatch: %d vs %d", len(res1.Files), len(res2.Files))
		}
		for j := range res1.Files {
			if res1.Files[j].Content != res2.Files[j].Content {
				t.Fatalf("idempotency violated for %s", res1.Files[j].RelativePath)
			}
			if res1.Files[j].LineCount > compiler.MaxLineBudget {
				t.Fatalf("budget exceeded for %s: %d lines", res1.Files[j].RelativePath, res1.Files[j].LineCount)
			}
		}
	}
}

func TestProperty_RatchetMonotonicity(t *testing.T) {
	for i := 1; i <= 30; i++ {
		b := &baseline.Baseline{
			Version:          1,
			TotalInfractions: i,
			Infractions: []baseline.Infraction{
				{RuleID: "HISS-04", FilePath: "a.go", Fingerprint: "fp_a"},
			},
		}

		// Clean file touched with zero infractions: must pass
		cleanRes := baseline.EvaluateRatchet(b, []baseline.Infraction{}, []string{"b.go"})
		if !cleanRes.Passed {
			t.Fatalf("expected clean debt reduction to pass at iteration %d", i)
		}

		// Touched file with infraction: must always fail
		touchedViolations := []baseline.Infraction{{RuleID: "HISS-04", FilePath: "a.go", Fingerprint: "fp_a"}}
		failRes := baseline.EvaluateRatchet(b, touchedViolations, []string{"a.go"})
		if failRes.Passed {
			t.Fatalf("expected touched file with infraction to fail at iteration %d", i)
		}
	}
}

func TestProperty_ASTMergeOrthogonalIndependence(t *testing.T) {
	base := "package main\n\nfunc Main() {}\n"
	for i := 0; i < 20; i++ {
		ours := fmt.Sprintf("package main\n\nfunc Main() {}\nfunc Ours%d() string { return \"ours\" }\n", i)
		theirs := fmt.Sprintf("package main\n\nfunc Main() {}\nfunc Theirs%d() string { return \"theirs\" }\n", i)

		res, err := astmerge.Merge(base, ours, theirs)
		if err != nil {
			t.Fatalf("merge failed at iteration %d: %v", i, err)
		}
		if !res.Clean {
			t.Fatalf("expected clean merge, got conflicts: %+v", res.Conflicts)
		}
		if !strings.Contains(res.MergedCode, fmt.Sprintf("Ours%d", i)) ||
			!strings.Contains(res.MergedCode, fmt.Sprintf("Theirs%d", i)) {
			t.Fatalf("merged code missing symbols at iteration %d", i)
		}
	}
}

// setupTestGitRepo creates the fixture repository the worktree stress test operates on.
//
// It runs git under a hermetic environment. Inherited configuration decided the outcome
// before: a developer carrying commit.gpgsign, a core.hooksPath or a template directory in
// their global config failed the commit here, and the stress suite then reported their
// machine rather than the code under test.
func setupTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	env := testsupport.HermeticGitEnv(t)
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		cmd.Env = env
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("failed setup cmd %v: %v: %s", c, err, strings.TrimSpace(string(out)))
		}
	}
}

func TestStress_ConcurrentWorktreeOperations(t *testing.T) {
	tmpDir := t.TempDir()
	setupTestGitRepo(t, tmpDir)

	mgr := worktree.NewManager(tmpDir)

	var wg sync.WaitGroup
	errCh := make(chan error, 10)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(taskIdx int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			taskID := fmt.Sprintf("stress-task-%d", taskIdx)
			wt, err := mgr.Create(ctx, taskID, "HEAD")
			if err != nil {
				errCh <- fmt.Errorf("create worktree %d: %w", taskIdx, err)
				return
			}

			if err := mgr.Remove(ctx, wt.TaskID, true); err != nil {
				errCh <- fmt.Errorf("remove worktree %d: %w", taskIdx, err)
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("concurrent worktree error: %v", err)
	}
}

const (
	// stressWorkers and stressIterations shape the concurrent compiler and scanner run.
	stressWorkers    = 20
	stressIterations = 10
	// stressBudgetFloor is each worker's deadline wherever the measured schedule fits inside
	// it: the fixed deadline the test had before it scaled (#558).
	stressBudgetFloor = 5 * time.Second
	// stressBudgetCeiling caps a scaled deadline, so a host that measures pathologically slow
	// still fails well inside go test's default ten-minute timeout.
	stressBudgetCeiling = 2 * time.Minute
	// stressSlack is the headroom over the ideal schedule for process-start and scheduler
	// contention between the workers and the other test binaries go test runs beside them.
	stressSlack = 4
	// stressCalibrationRuns is how many serial iterations the budget is measured over; the
	// slowest counts, so a cold first scan cannot understate the cost.
	stressCalibrationRuns = 3
)

// stressWorkerBudget is the deadline each worker gets for its iterations. The deadline is a
// hang guard (HISS-02), not a latency assertion, so it follows the host: the slowest measured
// serial iteration, times the iterations, times the rounds in which the workers share the
// CPUs, times stressSlack, clamped to [stressBudgetFloor, stressBudgetCeiling]. One scan is
// dominated by the Git file listing it starts (hiss.gitVisiblePaths): about half a
// millisecond on Linux, where the floor wins by orders of magnitude, and a process start on
// the Windows runner that is far slower, where the fixed 5 s let 20 workers time out (#558).
func stressWorkerBudget(serial time.Duration, workers, iterations, cpus int) time.Duration {
	cpus = max(cpus, 1)
	rounds := (max(workers, 0) + cpus - 1) / cpus
	scaled := serial * time.Duration(iterations*rounds*stressSlack)
	return min(max(scaled, stressBudgetFloor), stressBudgetCeiling)
}

// stressIteration is one worker iteration: a scan of dir, then a compile of a small context.
func stressIteration(ctx context.Context, tr *compiler.Transpiler, dir string, workerID, iter int) error {
	rep, err := hiss.Scan(ctx, dir, hiss.ScanOptions{MaxFuncLOC: 60})
	if err != nil {
		return fmt.Errorf("worker %d scan failed: %w", workerID, err)
	}
	if rep == nil {
		return fmt.Errorf("worker %d scan returned no report", workerID)
	}
	content := fmt.Sprintf("# Agent Worker %d-%d\nmake verify-all\n", workerID, iter)
	if _, err := tr.CompileContent(content); err != nil {
		return fmt.Errorf("worker %d compile failed: %w", workerID, err)
	}
	return nil
}

// measureStressIteration returns the slowest of stressCalibrationRuns serial iterations.
func measureStressIteration(t *testing.T, tr *compiler.Transpiler, dir string) time.Duration {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), stressBudgetCeiling)
	defer cancel()
	var slowest time.Duration
	for run := 0; run < stressCalibrationRuns; run++ {
		began := time.Now()
		if err := stressIteration(ctx, tr, dir, -1, run); err != nil {
			t.Fatalf("calibration: %v", err)
		}
		slowest = max(slowest, time.Since(began))
	}
	return slowest
}

func TestStress_ConcurrentCompilerAndScanner(t *testing.T) {
	tmpDir := t.TempDir()
	goCode := "package stress\n\nfunc Worker() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "worker.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	tr := compiler.NewTranspiler()
	serial := measureStressIteration(t, tr, tmpDir)
	budget := stressWorkerBudget(serial, stressWorkers, stressIterations, runtime.GOMAXPROCS(0))
	t.Logf("slowest serial iteration %s, worker budget %s", serial, budget)
	var wg sync.WaitGroup
	errCh := make(chan error, stressWorkers)

	for i := 0; i < stressWorkers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), budget)
			defer cancel()

			for iter := 0; iter < stressIterations; iter++ {
				if err := stressIteration(ctx, tr, tmpDir, workerID, iter); err != nil {
					errCh <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Errorf("stress failure: %v", err)
	}
}

// TestStressWorkerBudget: a slow host's measured cost scales the deadline above the floor
// (positive); a fast host's stays at the floor, never looser (negative); the floor crossing,
// the ceiling, the round boundary at the CPU count, a zero CPU count and a zero cost are
// exact (boundary).
func TestStressWorkerBudget(t *testing.T) {
	cases := []struct {
		name                 string
		serial               time.Duration
		workers, iters, cpus int
		want                 time.Duration
	}{
		{"windows runner", 100 * time.Millisecond, 20, 10, 4, 20 * time.Second},
		{"linux host", 600 * time.Microsecond, 20, 10, 32, stressBudgetFloor},
		{"just under the floor", 124 * time.Millisecond, 1, 10, 1, stressBudgetFloor},
		{"just over the floor", 126 * time.Millisecond, 1, 10, 1, 5040 * time.Millisecond},
		{"ceiling", 10 * time.Second, 20, 10, 4, stressBudgetCeiling},
		{"workers equal cpus", 200 * time.Millisecond, 4, 10, 4, 8 * time.Second},
		{"one worker more", 200 * time.Millisecond, 5, 10, 4, 16 * time.Second},
		{"no cpu count", 200 * time.Millisecond, 2, 10, 0, 16 * time.Second},
		{"no cost", 0, 20, 10, 4, stressBudgetFloor},
	}
	for _, tc := range cases {
		if got := stressWorkerBudget(tc.serial, tc.workers, tc.iters, tc.cpus); got != tc.want {
			t.Errorf("%s: budget %s, want %s", tc.name, got, tc.want)
		}
	}
}

const (
	// memoryStabilityIterations is the scan count the retention measurement averages over.
	memoryStabilityIterations = 50
	// maxRetainedBytesPerScan bounds what one scan of a three-line file may still hold
	// after a collection. The scan's own working set is kilobytes, so a bound in the tens
	// of kilobytes is loose enough for allocator noise and tight enough to see a report,
	// an AST or a file buffer being retained per call. The previous bound was 100 MB over
	// the whole loop, four orders of magnitude away from the workload, which no realistic
	// leak could reach.
	maxRetainedBytesPerScan = 64 * 1024
)

// retainedBytesPerIteration runs work the given number of times and returns the heap it
// still holds afterwards, per iteration. Both measurements follow a collection, so what is
// counted is retention rather than allocation. A workload that retains nothing measures at
// or below zero, which is reported as zero.
func retainedBytesPerIteration(t *testing.T, iterations int, work func(i int)) float64 {
	t.Helper()
	if iterations <= 0 {
		t.Fatalf("iterations must be positive, got %d", iterations)
	}
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < iterations; i++ {
		work(i)
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc <= before.HeapAlloc {
		return 0
	}
	return float64(after.HeapAlloc-before.HeapAlloc) / float64(iterations)
}

func TestStress_MemoryStabilityUnderLoad(t *testing.T) {
	tmpDir := t.TempDir()
	goCode := "package memcheck\n\nfunc Alpha() string { return \"clean\" }\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "check.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	retained := retainedBytesPerIteration(t, memoryStabilityIterations, func(i int) {
		if _, err := hiss.Scan(ctx, tmpDir, hiss.ScanOptions{MaxFuncLOC: 60}); err != nil {
			t.Fatalf("scan %d failed: %v", i, err)
		}
	})
	if retained > maxRetainedBytesPerScan {
		t.Fatalf("suspected retention: %.0f bytes still held per scan, bound is %d",
			retained, maxRetainedBytesPerScan)
	}
}

// TestStress_MemoryStabilityDetectorSeesARetainedAllocation pins the detector itself. The
// bound above is only evidence if it can fail, so the same measurement is run against a
// workload that deliberately retains a megabyte per iteration and must be caught.
func TestStress_MemoryStabilityDetectorSeesARetainedAllocation(t *testing.T) {
	var sink [][]byte
	retained := retainedBytesPerIteration(t, memoryStabilityIterations, func(int) {
		sink = append(sink, make([]byte, 1<<20))
	})
	if retained <= maxRetainedBytesPerScan {
		t.Fatalf("a 1 MiB per-iteration retention must exceed the %d byte bound, measured %.0f bytes",
			maxRetainedBytesPerScan, retained)
	}
	runtime.KeepAlive(sink)
}
