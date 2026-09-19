package bump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/lockdown"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/internal/worktree"
)

// CanaryStatus describes planning or execution, not certification.
type CanaryStatus string

const (
	CanaryPlanned   CanaryStatus = "planned"
	CanaryPassed    CanaryStatus = "passed"
	CanaryFailed    CanaryStatus = "failed"
	maxCanaryOutput              = 64 << 10
)

// ErrCanaryFailed identifies an unsuccessful update or configured test attempt.
var ErrCanaryFailed = errors.New("canary execution failed")

// CanaryOptions specifies operational parameters for speculative bump testing.
type CanaryOptions struct {
	RepoPath  string           `json:"repo_path"`
	Candidate UpgradeCandidate `json:"candidate"`
	TestCmd   string           `json:"test_cmd"`
	Retention bool             `json:"retention"`
	DryRun    bool             `json:"dry_run"`
}

// CanaryResult details speculative test execution outcome and diagnosed errors.
// Success means only that the configured test command exited zero. The current
// runner issues no certification; CanaryCertified remains false. Diagnostics are
// not adaptation patches, so StagedPatchPath remains empty.
type CanaryResult struct {
	Candidate       UpgradeCandidate `json:"candidate"`
	Success         bool             `json:"success"`
	WorktreePath    string           `json:"worktree_path"`
	ExecutionLog    string           `json:"execution_log"`
	DistilledErrors string           `json:"distilled_errors,omitempty"`
	StagedPatchPath string           `json:"staged_patch_path,omitempty"`
	CanaryCertified bool             `json:"canary_certified"`
	Status          CanaryStatus     `json:"status"`
	DiagnosticPath  string           `json:"diagnostic_path,omitempty"`
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
		Status:    CanaryFailed,
	}

	if opts.DryRun {
		res.Status = CanaryPlanned
		res.ExecutionLog = fmt.Sprintf("[DRY-RUN] Canary planned for %s -> %s; update and tests were not executed or certified", opts.Candidate.Package, opts.Candidate.TargetVersion)
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
		return res, fmt.Errorf("%w: update manifest: %w", ErrCanaryFailed, err)
	}

	return res, executeCanaryTest(ctx, wt.Path, opts.TestCmd, opts.RepoPath, res)
}

func executeCanaryTest(ctx context.Context, wtPath, testCmdStr, repoPath string, res *CanaryResult) error {
	if testCmdStr == "" {
		testCmdStr = "go test -v ./..."
	}

	parts := strings.Fields(testCmdStr)
	if len(parts) == 0 {
		res.ExecutionLog = "canary test command is empty"
		return fmt.Errorf("%w: %s", ErrCanaryFailed, res.ExecutionLog)
	}
	// The executable may be a path, and on Windows a path is written with backslashes, which
	// ValidateExecArg refuses as a metacharacter: no absolute canary command could run there.
	if err := util.ValidateExecPathArg(parts[0]); err != nil {
		res.ExecutionLog = fmt.Sprintf("invalid canary executable: %v", err)
		return fmt.Errorf("%w: executable: %w", ErrCanaryFailed, err)
	}
	out, err := util.RunCommandBytes(ctx, wtPath, parts[0], maxCanaryOutput, parts[1:]...)
	res.ExecutionLog = string(out.Stdout) + string(out.Stderr)

	if err == nil {
		res.Success = true
		res.Status = CanaryPassed
		return nil
	}
	res.Status = CanaryFailed
	res.ExecutionLog += fmt.Sprintf("\nConfigured test command failed: %v", err)
	diagnosticErr := distillBreakage(ctx, repoPath, res.ExecutionLog, res)
	return errors.Join(fmt.Errorf("%w: test command: %w", ErrCanaryFailed, err), diagnosticErr)
}

func distillBreakage(ctx context.Context, repoPath, output string, res *CanaryResult) error {
	log := lockdown.SarifLog{Version: "2.1.0", Runs: []lockdown.SarifRun{{
		Tool:    lockdown.SarifTool{Driver: lockdown.SarifDriver{Name: "bump-canary"}},
		Results: []lockdown.SarifResult{{RuleID: "CANARY-BREAK", Level: "error", Message: lockdown.SarifMessage{Text: output}}},
	}}}
	sarifJSON, err := json.Marshal(log)
	if err != nil {
		return fmt.Errorf("encode canary diagnostics: %w", err)
	}

	ephemeralDir := filepath.Join(repoPath, ".workingdir", "evidence", "canary")
	if err := contextopt.EnsureDirectory(ctx, ephemeralDir, 0700); err != nil {
		return fmt.Errorf("prepare canary diagnostics: %w", err)
	}
	dRes, err := lockdown.DistillSARIF(ctx, sarifJSON, repoPath, ephemeralDir)
	if err != nil {
		return fmt.Errorf("distill canary diagnostics: %w", err)
	}
	res.DistilledErrors = dRes.Summary
	res.DiagnosticPath = dRes.FullReportPath
	return nil
}

// ApplyBump updates a requested candidate and applies an optional patch snapshot.
// It does not verify certification. Patch syntax is checked before the update;
// applicability is checked afterward, and an application failure does not roll back.
func ApplyBump(ctx context.Context, repoPath string, c UpgradeCandidate, patchPath string) (resultErr error) {
	if ctx == nil {
		return fmt.Errorf("apply: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("apply cancelled: %w", err)
	}
	if patchPath != "" {
		snapshot, err := prepareBumpPatch(ctx, repoPath, patchPath)
		if err != nil {
			return err
		}
		defer func() { resultErr = errors.Join(resultErr, removeBumpPatch(snapshot)) }()
		patchPath = snapshot
	}

	if err := ApplyUpdate(ctx, repoPath, c); err != nil {
		return fmt.Errorf("failed applying bump: %w", err)
	}

	if patchPath != "" {
		// -c core.autocrlf=false pins what "git apply" writes to the patch's own bytes,
		// regardless of the operator's ambient git config. Without it, a machine with the
		// common Windows default core.autocrlf=true converts the LF line endings this
		// snapshot's patch carries into CRLF on write -- not a byte-for-byte application of
		// the adaptation patch, and a surprise for whatever reads the result afterward
		// (gofmt, a lockfile digest, the next diff). Caught by this package's own
		// TestApplyBumpAllowsPostUpdateManifestPatch and TestApplyBumpUsesRetainedPatchBytes
		// on the Windows leg of the portability matrix (#135).
		if _, applyErr := util.RunGit(ctx, repoPath, "-c", "core.autocrlf=false", "apply", "--ignore-whitespace", "--", patchPath); applyErr != nil {
			return fmt.Errorf("dependency update applied but adaptation patch failed; no rollback performed: %w", applyErr)
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
