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
// If running in CI, it asserts the working tree remains clean, avoiding implicit drift.
func runVerifyAll(args []string) error {
	dir := "."
	if len(args) > 0 {
		dir = args[0]
	}

	if os.Getenv("CI") == "true" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		abs, err := filepath.Abs(dir)
		if err != nil {
			return fmt.Errorf("resolve working directory: %w", err)
		}

		out, err := util.RunGit(ctx, abs, "status", "--porcelain")
		if err != nil {
			return fmt.Errorf("git status failed: %w", err)
		}
		if len(strings.TrimSpace(out)) > 0 {
			return fmt.Errorf("verify-all modified working tree:\n%s", out)
		}
	}
	return nil
}
