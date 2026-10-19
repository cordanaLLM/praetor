package main

import (
	"context"
	"fmt"
	"time"

	"github.com/cordanaLLM/praetor/internal/adopt"
	"github.com/cordanaLLM/praetor/internal/config"
)

// auditReleaseToken runs the release-token gate: every release-please step in .github/workflows
// must take its token from an earlier create-github-app-token step in the same job.
func auditReleaseToken(ctx context.Context, manifest *config.Manifest, rootDir string) error {
	line, err := adopt.AuditReleaseToken(ctx, adopt.ReleaseTokenOptions{
		Root: rootDir, Exceptions: manifest.Exceptions, Today: time.Now(),
	})
	if err != nil {
		return err
	}
	fmt.Println(line)
	return nil
}
