package bump

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/util"
)

// readManifest reads a manifest under exactly the rule writeManifest enforces: confined
// to root, a regular file rather than a symlink, and at most contextopt.MaxSourceBytes.
// The reader used to accept 16 MiB and to follow in-root symlinks, both of which the
// write path refuses, so an oversized or linked manifest was scanned, planned and edited
// and only then failed to be written.
//
// The scan entry points on this path carry no context of their own, so the read is bounded
// by contextopt.MaxDuration here, as internal/config/hierarchy.go does at the same kind of
// boundary.
func readManifest(root, name string) ([]byte, error) {
	path, err := util.ConfinePath(root, name)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), contextopt.MaxDuration)
	defer cancel()
	data, err := contextopt.ReadSnapshot(ctx, path)
	if err != nil {
		return nil, fmt.Errorf("read manifest %s: %w", name, err)
	}
	return data, nil
}

func writeManifest(ctx context.Context, dir, name string, before, data []byte) error {
	return contextopt.ReplaceSnapshot(ctx, filepath.Join(dir, name), data, contextopt.ReplaceOptions{Expected: before, Exists: true, Mode: 0644})
}
