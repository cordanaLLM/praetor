package paperclip

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ReadDisposition loads and parses a disposition JSON file.
func ReadDisposition(path string) (*Disposition, error) {
	data, err := os.ReadFile(path)
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
		cmd := exec.CommandContext(ctx, "git", "-C", repoPath, "status", "--porcelain")
		out, err := cmd.Output()
		if err == nil {
			uncommitted := strings.TrimSpace(string(out))
			if uncommitted != "" {
				return fmt.Errorf("contract violation: uncommitted changes exist in working tree; push branch before disposition")
			}
		}
	}

	// Verify harness exists in repoPath
	harnessPath := filepath.Join(repoPath, ".paperclip", "harness.json")
	if _, err := os.Stat(harnessPath); os.IsNotExist(err) {
		return fmt.Errorf("missing .paperclip/harness.json in %s; run 'praetorctl paperclip harness'", repoPath)
	}

	return nil
}
