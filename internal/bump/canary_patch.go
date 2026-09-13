package bump

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// ErrInvalidPatch identifies a requested patch rejected before dependency updates.
var ErrInvalidPatch = errors.New("invalid requested adaptation patch")

func prepareBumpPatch(ctx context.Context, repoPath, patchPath string) (snapshot string, resultErr error) {
	data, err := contextopt.ReadSnapshot(ctx, patchPath)
	if err != nil {
		return "", fmt.Errorf("%w: read patch: %w", ErrInvalidPatch, err)
	}
	snapshot, err = writeBumpPatch(data)
	if err != nil {
		return "", fmt.Errorf("%w: retain patch bytes: %w", ErrInvalidPatch, err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = errors.Join(resultErr, removeBumpPatch(snapshot))
		}
	}()
	// Syntax-only preflight preserves patches targeting the post-update manifest.
	if _, err := util.RunCommandBytes(ctx, repoPath, "git", maxCanaryOutput, "apply", "--numstat", "--", snapshot); err != nil {
		return snapshot, fmt.Errorf("%w: patch syntax: %w", ErrInvalidPatch, err)
	}
	return snapshot, nil
}

func writeBumpPatch(data []byte) (string, error) {
	file, err := os.CreateTemp("", "praetor-bump-*.patch")
	if err != nil {
		return "", err
	}
	_, writeErr := file.Write(data)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err != nil {
		return "", errors.Join(err, removeBumpPatch(file.Name()))
	}
	return file.Name(), nil
}

func removeBumpPatch(path string) error {
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("remove retained adaptation patch: %w", err)
	}
	return nil
}
