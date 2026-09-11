package gating

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/hiss"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// GatingStatus represents the disposition of a gated check.
type GatingStatus string

const (
	StatusAdmitted GatingStatus = "ADMITTED"
	StatusRejected GatingStatus = "REJECTED"
)

// StageResult captures the outcome of a single gating pipeline stage.
type StageResult struct {
	Name     string        `json:"name"`
	Passed   bool          `json:"passed"`
	Duration time.Duration `json:"duration"`
	Message  string        `json:"message,omitempty"`
}

// PipelineReport aggregates the entire gated pre-merge verification.
type PipelineReport struct {
	Status           GatingStatus  `json:"status"`
	RepoDir          string        `json:"repo_dir"`
	ReceiptSignature string        `json:"receipt_signature,omitempty"`
	Stages           []StageResult `json:"stages"`
	TotalElapsed     time.Duration `json:"total_elapsed"`
}

// RunGatedPipeline executes the 4-stage anti-direct-merge gating pipeline.
func RunGatedPipeline(ctx context.Context, repoDir string, dryRun bool) (*PipelineReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("pipeline: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("pipeline cancelled: %w", err)
	}

	start := time.Now()
	rep := &PipelineReport{
		Status:  StatusAdmitted,
		RepoDir: repoDir,
		Stages:  make([]StageResult, 0, 4),
	}

	if err := executeStages(ctx, repoDir, dryRun, rep); err != nil {
		rep.Status = StatusRejected
		return rep, nil
	}

	rep.TotalElapsed = time.Since(start)
	return rep, nil
}

func executeStages(ctx context.Context, repoDir string, dryRun bool, rep *PipelineReport) error {
	stages := []struct {
		name string
		fn   func(context.Context) error
	}{
		{"Prefetch & Lockfiles", func(c context.Context) error { return runPrefetchStage(c, repoDir) }},
		{"HISS Invariant Scan", func(c context.Context) error { return runHissStage(c, repoDir) }},
		{"Race-Detector Tests", func(c context.Context) error { return runTestStage(c, repoDir, dryRun) }},
		{"Ed25519 Exit-0 Receipt", func(c context.Context) error { return runReceiptStage(c, repoDir, rep) }},
	}

	for _, s := range stages {
		if err := executeStage(ctx, s.name, s.fn, rep); err != nil {
			return err
		}
	}
	return nil
}

func executeStage(ctx context.Context, name string, fn func(context.Context) error, rep *PipelineReport) error {
	sStart := time.Now()
	err := fn(ctx)
	dur := time.Since(sStart)

	res := StageResult{
		Name:     name,
		Passed:   err == nil,
		Duration: dur,
	}
	if err != nil {
		res.Message = err.Error()
	}
	rep.Stages = append(rep.Stages, res)
	return err
}

func runPrefetchStage(ctx context.Context, repoDir string) error {
	if err := VerifyLockfiles(repoDir); err != nil {
		return err
	}
	_, err := PrefetchDependencies(ctx, repoDir)
	return err
}

func runHissStage(ctx context.Context, repoDir string) error {
	opts := hiss.ScanOptions{
		Cap:        1000,
		MaxFuncLOC: 60,
	}
	scanRep, err := hiss.Scan(ctx, repoDir, opts)
	if err != nil {
		return fmt.Errorf("hiss scan error: %w", err)
	}
	if scanRep.TotalInfractions > 0 {
		return fmt.Errorf("hiss violations detected: %d infractions", scanRep.TotalInfractions)
	}
	return nil
}

func runTestStage(ctx context.Context, repoDir string, dryRun bool) error {
	if dryRun {
		return nil
	}
	tCtx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	testDir := repoDir
	wtMgr := worktree.NewManager(repoDir)
	if wtMgr != nil {
		taskID := fmt.Sprintf("gate-%d", time.Now().UnixNano()%1000000)
		wt, createErr := wtMgr.Create(tCtx, taskID, "HEAD")
		if createErr == nil {
			testDir = wt.Path
			defer func() {
				if cleanErr := wtMgr.Remove(context.Background(), taskID, true); cleanErr != nil {
					return
				}
			}()
		}
	}

	cmd := exec.CommandContext(tCtx, "go", "test", "-race", "./...")
	cmd.Dir = testDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tests failed in %s: %s (%w)", testDir, string(out), err)
	}
	return nil
}

func runReceiptStage(ctx context.Context, repoDir string, rep *PipelineReport) error {
	_, priv, err := lockdown.GenerateKeyPair()
	if err != nil {
		return fmt.Errorf("generate Ed25519 keypair: %w", err)
	}

	commit := getGitCommitSHA(ctx, repoDir)
	payload := []byte("ALL_GATED_CHECKS_PASSED")
	receipt, err := lockdown.CreateReceipt("standardsctl gate", 0, payload, commit, filepath.Base(repoDir), priv)
	if err != nil {
		return fmt.Errorf("create exit-0 receipt: %w", err)
	}

	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal receipt: %w", err)
	}

	receiptPath := filepath.Join(repoDir, ".standards-receipt.json")
	if err := os.WriteFile(receiptPath, data, 0644); err != nil {
		return fmt.Errorf("write receipt: %w", err)
	}

	rep.ReceiptSignature = receipt.Signature
	return nil
}

func getGitCommitSHA(ctx context.Context, repoDir string) string {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
	cmd.Dir = repoDir
	out, err := cmd.Output()
	if err != nil {
		return "uncommitted"
	}
	return strings.TrimSpace(string(out))
}
