package devcontainer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/contextopt"
)

// Legacy self-host configuration remains valid only where its inputs actually
// exist. A matching template string cannot establish an adopted checkout's tools.
func verifyLegacyBootstrapInputs(ctx context.Context, path string, dc *DevContainer) error {
	if dc.Build == nil || dc.Build.Dockerfile != DefaultDockerfilePath || dc.Build.Context != DefaultContextDir {
		return nil
	}
	dockerfile := filepath.Join(filepath.Dir(path), DefaultDockerfilePath)
	if _, err := contextopt.ReadSnapshot(ctx, dockerfile); err != nil {
		return fmt.Errorf("legacy DevContainer build input is unavailable; regenerate with an explicit source bundle: %w", err)
	}
	if !strings.Contains(dc.PostCreateCommand, "go run ./cmd/standardsctl") {
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
	if _, err := contextopt.ReadSnapshot(ctx, filepath.Join(root, "cmd", "standardsctl", "main.go")); err != nil {
		return fmt.Errorf("legacy DevContainer startup has no Praetor CLI sources: %w", err)
	}
	return nil
}
