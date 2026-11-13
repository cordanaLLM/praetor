// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package replay

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// snapshotCutoff creates an independent shallow repository containing only sha and its tree.
func snapshotCutoff(ctx context.Context, source, sha string) (string, func() error, error) {
	dir, err := os.MkdirTemp("", "praetor-replay-tree-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() error { return os.RemoveAll(dir) }
	if err := populateSnapshot(ctx, source, sha, dir); err != nil {
		return "", nil, errors.Join(err, cleanup())
	}
	return dir, cleanup, nil
}

// populateSnapshot fetches a single local commit without source refs, alternates or FETCH_HEAD.
func populateSnapshot(ctx context.Context, source, sha, dir string) error {
	runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	source, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	format, err := util.RunGitProbe(runCtx, source, 128, "rev-parse", "--show-object-format")
	if err != nil {
		return err
	}
	if _, err := util.RunGit(runCtx, dir, "init", "--quiet", "--template=", "--object-format="+strings.TrimSpace(string(format.Stdout))); err != nil {
		return err
	}
	localURL := url.URL{Scheme: "file", Path: filepath.ToSlash(source)}
	if _, err := util.RunGit(runCtx, dir, "fetch", "--quiet", "--no-tags", "--no-recurse-submodules", "--no-write-fetch-head", "--depth=1", localURL.String(), sha); err != nil {
		return fmt.Errorf("fetch cutoff snapshot: %w", err)
	}
	_, err = util.RunGit(runCtx, dir, "checkout", "--quiet", "--detach", sha)
	return err
}
