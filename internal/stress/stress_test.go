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

func setupTestGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.name", "Test"},
		{"git", "config", "user.email", "test@example.com"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, c := range cmds {
		cmd := exec.Command(c[0], c[1:]...)
		cmd.Dir = dir
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed setup cmd %v: %v", c, err)
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

func TestStress_ConcurrentCompilerAndScanner(t *testing.T) {
	tmpDir := t.TempDir()
	goCode := "package stress\n\nfunc Worker() int { return 42 }\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "worker.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	tr := compiler.NewTranspiler()
	var wg sync.WaitGroup
	errCh := make(chan error, 20)

	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			for iter := 0; iter < 10; iter++ {
				rep, err := hiss.Scan(ctx, tmpDir, hiss.ScanOptions{MaxFuncLOC: 60})
				if err != nil || rep == nil {
					errCh <- fmt.Errorf("worker %d scan failed: %v", workerID, err)
					return
				}
				content := fmt.Sprintf("# Agent Worker %d-%d\nmake verify-all\n", workerID, iter)
				if _, err := tr.CompileContent(content); err != nil {
					errCh <- fmt.Errorf("worker %d compile failed: %v", workerID, err)
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

func TestStress_MemoryStabilityUnderLoad(t *testing.T) {
	tmpDir := t.TempDir()
	goCode := "package memcheck\n\nfunc Alpha() string { return \"clean\" }\n"
	if err := os.WriteFile(filepath.Join(tmpDir, "check.go"), []byte(goCode), 0644); err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var mBefore runtime.MemStats
	runtime.ReadMemStats(&mBefore)

	ctx := context.Background()
	for i := 0; i < 50; i++ {
		_, err := hiss.Scan(ctx, tmpDir, hiss.ScanOptions{MaxFuncLOC: 60})
		if err != nil {
			t.Fatalf("scan failed: %v", err)
		}
	}

	runtime.GC()
	var mAfter runtime.MemStats
	runtime.ReadMemStats(&mAfter)

	if mAfter.HeapAlloc > mBefore.HeapAlloc {
		diffMB := float64(mAfter.HeapAlloc-mBefore.HeapAlloc) / (1024 * 1024)
		if diffMB > 100.0 {
			t.Fatalf("suspected memory leak: heap diff %.2f MB", diffMB)
		}
	}
}
