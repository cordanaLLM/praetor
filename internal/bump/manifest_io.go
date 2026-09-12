package bump

import (
	"context"
	"errors"
	"path/filepath"

	"fmt"
	"github.com/cordanaLLM/praetor/internal/contextopt"
	"io"
	"os"
)

// readManifest confines manifest access, including symlinks, to its explicit root.
func readManifest(root, name string) (data []byte, resultErr error) {
	file, err := os.OpenInRoot(root, name)
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, file.Close()) }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	const maxManifestBytes = 16 << 20
	if !info.Mode().IsRegular() || info.Size() > maxManifestBytes {
		return nil, fmt.Errorf("manifest must be regular and at most %d bytes", maxManifestBytes)
	}
	data, err = io.ReadAll(io.LimitReader(file, maxManifestBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxManifestBytes {
		return nil, fmt.Errorf("manifest exceeds %d bytes", maxManifestBytes)
	}
	return data, nil
}

func writeManifest(ctx context.Context, dir, name string, before, data []byte) error {
	return contextopt.ReplaceSnapshot(ctx, filepath.Join(dir, name), data, contextopt.ReplaceOptions{Expected: before, Exists: true, Mode: 0644})
}
