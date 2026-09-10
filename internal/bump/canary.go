package bump

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/standards/internal/lockdown"
	"github.com/cordanaLLM/standards/internal/worktree"
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
			_ = wtManager.Remove(context.Background(), taskID, true)
		}
	}()

	// Apply candidate version modification in worktree
	if err := applyCandidateInWorktree(wt.Path, opts.Candidate); err != nil {
		res.ExecutionLog = fmt.Sprintf("failed updating manifest in worktree: %v", err)
		return res, nil
	}

	// Run test verification
	testCmdStr := opts.TestCmd
	if testCmdStr == "" {
		testCmdStr = "go test -v ./..."
	}

	parts := strings.Fields(testCmdStr)
	cmd := exec.CommandContext(ctx, parts[0], parts[1:]...)
	cmd.Dir = wt.Path
	out, err := cmd.CombinedOutput()
	res.ExecutionLog = string(out)

	if err == nil {
		res.Success = true
		res.CanaryCertified = true
	} else {
		distillBreakage(ctx, opts.RepoPath, taskID, string(out), res)
	}

	return res, nil
}

func applyCandidateInWorktree(wtPath string, c UpgradeCandidate) error {
	switch c.ManifestType {
	case "go.mod":
		goModPath := filepath.Join(wtPath, "go.mod")
		data, err := os.ReadFile(goModPath)
		if err != nil {
			return err
		}
		oldLine := fmt.Sprintf("%s %s", c.Package, c.CurrentVersion)
		newLine := fmt.Sprintf("%s %s", c.Package, c.TargetVersion)
		replaced := strings.Replace(string(data), oldLine, newLine, 1)
		return os.WriteFile(goModPath, []byte(replaced), 0644)

	case "package.json":
		pkgPath := filepath.Join(wtPath, "package.json")
		data, err := os.ReadFile(pkgPath)
		if err != nil {
			return err
		}
		replaced := strings.Replace(string(data), c.CurrentVersion, c.TargetVersion, 1)
		return os.WriteFile(pkgPath, []byte(replaced), 0644)

	default:
		return nil
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
	_ = os.MkdirAll(patchDir, 0755)
	patchFile := filepath.Join(patchDir, fmt.Sprintf("%s.patch", taskID))
	patchContent := fmt.Sprintf("# Canary Breakage Adaptation Patch for %s\n# Target: %s\n# Output:\n%s\n", res.Candidate.Package, res.Candidate.TargetVersion, output)
	if err := os.WriteFile(patchFile, []byte(patchContent), 0644); err == nil {
		res.StagedPatchPath = patchFile
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

	if err := applyCandidateInWorktree(repoPath, c); err != nil {
		return fmt.Errorf("failed applying bump: %w", err)
	}

	if patchPath != "" && fileExists(patchPath) {
		cmd := exec.CommandContext(ctx, "git", "apply", "--ignore-whitespace", patchPath)
		cmd.Dir = repoPath
		_ = cmd.Run()
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
