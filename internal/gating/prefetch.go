package gating

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

const (
	DefaultPrefetchTimeout = 60 * time.Second
)

// ErrLockfileEmpty reports a lockfile or manifest that exists but carries no content.
var ErrLockfileEmpty = errors.New("file is empty")

// PrefetchReport details the results of dependency prefetching and checksum verification.
type PrefetchReport struct {
	VerifiedDependencies bool          `json:"verified_dependencies"`
	Skipped              bool          `json:"skipped"`
	TotalModules         int           `json:"total_modules"`
	Duration             time.Duration `json:"duration"`
}

// PrefetchDependencies runs go mod verify and pre-downloads modules with an explicit
// context timeout. Repositories without a go.mod are not Go repositories: the stage is
// reported as skipped instead of rejecting a conforming polyglot repository.
func PrefetchDependencies(ctx context.Context, repoDir string) (*PrefetchReport, error) {
	if ctx == nil {
		return nil, fmt.Errorf("prefetch: context cannot be nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("prefetch cancelled: %w", err)
	}

	start := time.Now()
	if !util.FileExists(filepath.Join(repoDir, "go.mod")) {
		return &PrefetchReport{Skipped: true, Duration: time.Since(start)}, nil
	}

	pCtx, cancel := context.WithTimeout(ctx, DefaultPrefetchTimeout)
	defer cancel()

	if out, err := util.RunCommand(pCtx, repoDir, "go", "mod", "verify"); err != nil {
		return nil, fmt.Errorf("dependency verification failed: %s (%w)", out, err)
	}
	if out, err := util.RunCommand(pCtx, repoDir, "go", "mod", "download"); err != nil {
		return nil, fmt.Errorf("dependency download failed: %s (%w)", out, err)
	}

	return &PrefetchReport{
		VerifiedDependencies: true,
		Duration:             time.Since(start),
	}, nil
}

// VerifyLockfiles confirms that the standards manifest and lockfile exist and are
// non-empty.
func VerifyLockfiles(repoDir string) error {
	lockPath := filepath.Join(repoDir, ".standards.lock")
	manifestPath := filepath.Join(repoDir, ".standards.yaml")

	if err := checkFileExistsAndNonEmpty(manifestPath); err != nil {
		return fmt.Errorf("standards manifest invalid: %w", err)
	}
	if err := checkFileExistsAndNonEmpty(lockPath); err != nil {
		return fmt.Errorf("standards lockfile invalid: %w", err)
	}
	return nil
}

// checkFileExistsAndNonEmpty stats path literally. It must never treat the path as a
// glob pattern: repository directories legitimately contain '[', '*' and '?'.
func checkFileExistsAndNonEmpty(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("file not found: %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("expected a file but found a directory: %s", path)
	}
	if info.Size() == 0 {
		return fmt.Errorf("%w: %s", ErrLockfileEmpty, path)
	}
	return nil
}
