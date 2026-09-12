package paperclip

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
	"os"
	"path/filepath"
)

// ReadDisposition loads and parses a disposition JSON file.
func ReadDisposition(path string) (*Disposition, error) {
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	return ReadDispositionContext(ctx, path)
}

func ReadDispositionContext(ctx context.Context, path string) (*Disposition, error) {
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read disposition file: %w", err)
	}

	var d Disposition
	if err := json.Unmarshal(data, &d); err != nil {
		return nil, fmt.Errorf("parse disposition json: %w", err)
	}
	return &d, nil
}

// VerifyRun asserts that a Paperclip agent run satisfies Rule 0 and contract invariants.
func VerifyRun(ctx context.Context, repoPath string, d *Disposition) error {
	if ctx == nil {
		return fmt.Errorf("verify: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("verify cancelled: %w", err)
	}

	if err := d.Validate(ctx); err != nil {
		return fmt.Errorf("disposition invalid: %w", err)
	}

	// Invariant: Pushing is NOT shipping.
	// If in_review, working directory in repoPath must have committed changes.
	if d.Status == StatusInReview {
		uncommitted, err := util.RunGit(ctx, repoPath, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("verify working tree git status: %w", err)
		}
		if uncommitted != "" {
			return fmt.Errorf("contract violation: uncommitted changes exist in working tree; push branch before disposition")
		}
	}

	// Verify harness exists in repoPath
	harnessPath := filepath.Join(repoPath, ".paperclip", "harness.json")
	if _, err := os.Stat(harnessPath); os.IsNotExist(err) {
		return fmt.Errorf("missing .paperclip/harness.json in %s; run 'praetorctl paperclip harness'", repoPath)
	}

	return nil
}
