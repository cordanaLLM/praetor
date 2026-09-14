package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// legacyCLIRun is the pre-rename self-host startup command. It is matched only to
// reject a configuration that would run a command directory that no longer exists;
// no legacy path is ever opened or executed.
const legacyCLIRun = "go run ./cmd/standardsctl"

// selfHostCLIRun is the current self-host startup command whose inputs are checked.
const selfHostCLIRun = "go run ./cmd/praetorctl"

// ErrLegacyPostCreate rejects a DevContainer that still starts the pre-rename CLI.
var ErrLegacyPostCreate = errors.New("DevContainer postCreateCommand runs 'go run ./cmd/standardsctl', which no longer exists; regenerate with 'praetorctl devcontainer generate --force'")

func rejectLegacyPostCreate(dc *DevContainer) error {
	if dc != nil && strings.Contains(dc.PostCreateCommand, legacyCLIRun) {
		return ErrLegacyPostCreate
	}
	return nil
}

// Legacy self-host configuration remains valid only where its inputs actually
// exist. A matching template string cannot establish an adopted checkout's tools.
// Verify rejects the pre-rename postCreateCommand before reaching this check.
func verifyLegacyBootstrapInputs(ctx context.Context, path string, dc *DevContainer) error {
	if dc.Build == nil || dc.Build.Dockerfile != DefaultDockerfilePath || dc.Build.Context != DefaultContextDir {
		return nil
	}
	dockerfile := filepath.Join(filepath.Dir(path), DefaultDockerfilePath)
	if _, err := contextopt.ReadSnapshot(ctx, dockerfile); err != nil {
		return fmt.Errorf("legacy DevContainer build input is unavailable; regenerate with an explicit source bundle: %w", err)
	}
	if !strings.Contains(dc.PostCreateCommand, selfHostCLIRun) {
		return nil
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(path), DefaultContextDir))
	ready, err := bootstrapSourceAvailable(ctx, root)
	if err != nil {
		return err
	}
	if !ready {
		return errors.New("legacy DevContainer startup requires the Praetor source checkout; regenerate with an explicit source bundle")
	}
	if _, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, filepath.FromSlash(bootstrapCLIMain))); err != nil {
		return fmt.Errorf("legacy DevContainer startup has no Praetor CLI sources: %w", err)
	}
	return nil
}
