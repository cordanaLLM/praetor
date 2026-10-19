package bump

import (
	"context"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/contextopt"
	"github.com/cordanaLLM/praetor/internal/gomanifest"
)

// readManifest reads a manifest under exactly the rule writeManifest enforces: confined
// to root, a regular file rather than a symlink, and at most contextopt.MaxSourceBytes.
// The reader used to accept 16 MiB and to follow in-root symlinks, both of which the
// write path refuses, so an oversized or linked manifest was scanned, planned and edited
// and only then failed to be written.
//
// The read runs under the caller's context; contextopt.ReadSnapshot adds its own
// contextopt.MaxDuration bound on top, so a caller without a deadline is still bounded.
func readManifest(ctx context.Context, root, name string) ([]byte, error) {
	return gomanifest.ReadManifest(ctx, root, name)
}

func writeManifest(ctx context.Context, dir, name string, before, data []byte) error {
	return contextopt.ReplaceSnapshot(ctx, filepath.Join(dir, name), data, contextopt.ReplaceOptions{Expected: before, Exists: true, Mode: 0644})
}
