package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// runVerifyAll executes the final reproducibility check for the verify-all gate.
// It unconditionally synchronizes the state ledger (repairing DEV-05 drift).
// If running in CI, it asserts the working tree remains clean.
func runVerifyAll(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	// Unconditionally sync state to repair DEV-05
	if err := runStateSync([]string{"--dir=" + dir}); err != nil {
		return fmt.Errorf("verify-all state sync failed: %w", err)
	}

	if os.Getenv("CI") == "true" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}

		// Wait for index to settle
		if _, err := util.RunGit(ctx, abs, "update-index", "-q", "--refresh"); err != nil {
			// update-index --refresh intentionally fails with code 1 if the tree is dirty.
			// The subsequent git diff --quiet check evaluates the actual drift.
			_ = err
		}

		out, err := util.RunGit(ctx, abs, "diff", "--quiet")
		if err != nil {
			if strings.Contains(err.Error(), "exit status 1") || strings.Contains(err.Error(), "exit code 1") {
				return fmt.Errorf("verify-all modified working tree: %s", out)
			}
			return fmt.Errorf("git diff failed: %w", err)
		}
	}
	return nil
}
