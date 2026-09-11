package gating

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/cordanaLLM/standards/internal/hiss"
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
	Status       GatingStatus  `json:"status"`
	RepoDir      string        `json:"repo_dir"`
	Stages       []StageResult `json:"stages"`
	TotalElapsed time.Duration `json:"total_elapsed"`
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

	if err := executeStage(ctx, "Prefetch & Lockfiles", func(sCtx context.Context) error {
		return runPrefetchStage(sCtx, repoDir)
	}, rep); err != nil {
		rep.Status = StatusRejected
		return rep, nil
	}

	if err := executeStage(ctx, "HISS Invariant Scan", func(sCtx context.Context) error {
		return runHissStage(sCtx, repoDir)
	}, rep); err != nil {
		rep.Status = StatusRejected
		return rep, nil
	}

	if err := executeStage(ctx, "Race-Detector Tests", func(sCtx context.Context) error {
		return runTestStage(sCtx, repoDir, dryRun)
	}, rep); err != nil {
		rep.Status = StatusRejected
		return rep, nil
	}

	rep.TotalElapsed = time.Since(start)
	return rep, nil
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
	tCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(tCtx, "go", "test", "-race", "./internal/gating/...")
	cmd.Dir = repoDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("tests failed: %s (%w)", string(out), err)
	}
	return nil
}
