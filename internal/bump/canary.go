package bump

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// CanaryOptions specifies operational parameters for speculative bump testing.
type CanaryOptions struct {
	RepoPath  string           `json:"repo_path"`
	Candidate UpgradeCandidate `json:"candidate"`
	TestCmd   string           `json:"test_cmd"`
	Retention bool             `json:"retention"`
	DryRun    bool             `json:"dry_run"`
}

// CanaryResult details speculative test execution outcome and diagnosed errors.
type CanaryResult struct {
	Candidate       UpgradeCandidate `json:"candidate"`
	Success         bool             `json:"success"`
	WorktreePath    string           `json:"worktree_path"`
	ExecutionLog    string           `json:"execution_log"`
	DistilledErrors string           `json:"distilled_errors,omitempty"`
	StagedPatchPath string           `json:"staged_patch_path,omitempty"`
	CanaryCertified bool             `json:"canary_certified"`
}

// RunCanary speculatively tests an upgrade candidate in an isolated ephemeral worktree.
func RunCanary(ctx context.Context, opts CanaryOptions) (*CanaryResult, error) {
	if ctx == nil {
		return nil, fmt.Errorf("canary: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("canary cancelled: %w", err)
	}

	res := &CanaryResult{
		Candidate: opts.Candidate,
		Success:   false,
	}

	if opts.DryRun {
		res.Success = true
		res.CanaryCertified = true
		res.ExecutionLog = fmt.Sprintf("[DRY-RUN] Speculative canary validated for %s -> %s", opts.Candidate.Package, opts.Candidate.TargetVersion)
		return res, nil
	}

	wtManager := worktree.NewManager(opts.RepoPath)
	taskID := fmt.Sprintf("bump-%s-%d", sanitizeTaskID(opts.Candidate.Package), time.Now().UnixNano()%100000)

	wt, err := wtManager.Create(ctx, taskID, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("create canary worktree: %w", err)
	}
	res.WorktreePath = wt.Path

	defer func() {
		if !opts.Retention {
			if rmErr := wtManager.Remove(context.WithoutCancel(ctx), taskID, true); rmErr != nil {
				res.ExecutionLog += fmt.Sprintf("\nwarning: failed removing worktree %s: %v", taskID, rmErr)
			}
		}
	}()

	// Apply candidate version modification in worktree
	if err := ApplyUpdate(ctx, wt.Path, opts.Candidate); err != nil {
		res.ExecutionLog = fmt.Sprintf("failed updating manifest in worktree: %v", err)
		return res, nil
	}

	executeCanaryTest(ctx, wt.Path, opts.TestCmd, opts.RepoPath, taskID, res)
	return res, nil
}

func executeCanaryTest(ctx context.Context, wtPath, testCmdStr, repoPath, taskID string, res *CanaryResult) {
	if testCmdStr == "" {
		testCmdStr = "go test -v ./..."
	}

	parts := strings.Fields(testCmdStr)
	if len(parts) == 0 {
		res.ExecutionLog = "canary test command is empty"
		return
	}
	if err := util.ValidateExecArg(parts[0]); err != nil {
		res.ExecutionLog = fmt.Sprintf("invalid canary executable: %v", err)
		return
	}
	out, err := util.RunCommand(ctx, wtPath, parts[0], parts[1:]...)
	res.ExecutionLog = out

	if err == nil {
		res.Success = true
		res.CanaryCertified = true
	} else {
		distillBreakage(ctx, repoPath, taskID, out, res)
	}
}

func distillBreakage(ctx context.Context, repoPath, taskID, output string, res *CanaryResult) {
	// Synthesize SARIF wrapper around compiler/test output for distillation
	sarifJSON := fmt.Sprintf(`{"version":"2.1.0","runs":[{"tool":{"driver":{"name":"bump-canary"}},"results":[{"ruleId":"CANARY-BREAK","level":"error","message":{"text":%q},"locations":[]}]}]}`, output)

	ephemeralDir := filepath.Join(repoPath, ".standards", "ephemeral")
	dRes, err := lockdown.DistillSARIF(ctx, []byte(sarifJSON), repoPath, ephemeralDir)
	if err == nil {
		res.DistilledErrors = dRes.Summary
	} else {
		res.DistilledErrors = output
	}

	// Stage an adaptation patch stub in .standards/patches/
	patchDir := filepath.Join(repoPath, ".standards", "patches")
	if mkErr := os.MkdirAll(patchDir, 0700); mkErr == nil {
		patchFile := filepath.Join(patchDir, fmt.Sprintf("%s.patch", taskID))
		patchContent := fmt.Sprintf("# Canary Breakage Adaptation Patch for %s\n# Target: %s\n# Output:\n%s\n", res.Candidate.Package, res.Candidate.TargetVersion, output)
		if err := os.WriteFile(patchFile, []byte(patchContent), 0600); err == nil {
			res.StagedPatchPath = patchFile
		}
	}
}

// ApplyBump updates the target repository with the verified candidate and applies patch if present.
func ApplyBump(ctx context.Context, repoPath string, c UpgradeCandidate, patchPath string) error {
	if ctx == nil {
		return fmt.Errorf("apply: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("apply cancelled: %w", err)
	}

	if err := ApplyUpdate(ctx, repoPath, c); err != nil {
		return fmt.Errorf("failed applying bump: %w", err)
	}

	if patchPath != "" && util.FileExists(patchPath) {
		if err := util.ValidateExecArg(patchPath); err != nil {
			return fmt.Errorf("invalid patch path: %w", err)
		}
		if _, applyErr := util.RunGit(ctx, repoPath, "apply", "--ignore-whitespace", "--", patchPath); applyErr != nil {
			return fmt.Errorf("apply patch %s: %w", patchPath, applyErr)
		}
	}

	return nil
}

func sanitizeTaskID(pkg string) string {
	var b strings.Builder
	for _, r := range pkg {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	res := b.String()
	if len(res) > 30 {
		res = res[:30]
	}
	return res
}
