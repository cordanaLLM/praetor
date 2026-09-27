// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cordanaLLM/praetor/internal/util"
)

// buildSource is the tree Install compiles and the commit the install manifest records for it.
type buildSource struct {
	dir     string
	commit  string
	cleanup func()
}

// sourceSnapshotTimeout bounds the clone and checkout of one commit (HISS-02).
const sourceSnapshotTimeout = 2 * time.Minute

// prepareBuildSource resolves checkout's HEAD commit and the tree Install compiles for it.
//
// When no tracked file differs from HEAD, the tree is a fresh clone of that commit. Go stamps
// vcs.modified from `git status --porcelain`, which lists untracked files too, so a build of
// the working tree came out -dirty whenever anything untracked sat in the checkout; the gate
// receipt .standards-receipt.json always does. CheckBuildCurrent then refused every such
// install for context writes, because a -dirty build outside the checkout carries edits its
// stamp does not name. A clone's build carries a clean stamp naming exactly the commit the
// manifest records, and no untracked file enters the binaries.
//
// A checkout with modified tracked files is built as it stands and stamped -dirty, so a
// work-in-progress install still carries its edits.
func prepareBuildSource(ctx context.Context, checkout string) (buildSource, error) {
	commit, err := engineCommit(ctx, checkout)
	if err != nil {
		return buildSource{}, err
	}
	if !objectNamePattern.MatchString(commit) {
		return buildSource{}, fmt.Errorf("workstation: checkout HEAD %q in %s is not a commit id", commit, checkout)
	}
	modified, err := trackedChanges(ctx, checkout)
	if err != nil {
		return buildSource{}, err
	}
	if modified {
		return buildSource{dir: checkout, commit: commit, cleanup: func() {}}, nil
	}
	dir, cleanup, err := cloneCommit(ctx, checkout, commit)
	if err != nil {
		return buildSource{}, err
	}
	return buildSource{dir: dir, commit: commit, cleanup: cleanup}, nil
}

// cloneCommit checks commit out, detached, into a fresh clone under a new temporary directory
// and returns the clone and a cleanup that removes it. The clone borrows checkout's object
// store (--shared), so nothing is copied but the checked-out files.
func cloneCommit(ctx context.Context, checkout, commit string) (string, func(), error) {
	parent, err := os.MkdirTemp("", "praetor-workstation-source-")
	if err != nil {
		return "", nil, fmt.Errorf("workstation: create source directory: %w", err)
	}
	cleanup := func() {
		os.RemoveAll(parent) //nolint:errcheck // best-effort scratch cleanup; the build already read the clone
	}
	ctx, cancel := context.WithTimeout(ctx, sourceSnapshotTimeout)
	defer cancel()
	dir := filepath.Join(parent, "checkout")
	err = snapshotGit(ctx, parent, "clone", "--quiet", "--template=", "--shared", "--no-checkout", "--", checkout, dir)
	if err == nil {
		err = snapshotGit(ctx, dir, "checkout", "--quiet", "--detach", commit)
	}
	if err != nil {
		cleanup()
		return "", nil, fmt.Errorf("workstation: snapshot %s from %s: %w", shortCommit(commit), checkout, err)
	}
	return dir, cleanup, nil
}

// snapshotGit runs one git command for cloneCommit with hooks and filesystem monitors off. The
// clone also copies no template (--template=), so neither a globally configured hook directory
// nor a template hook runs against the scratch clone.
func snapshotGit(ctx context.Context, dir string, args ...string) error {
	argv := append([]string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false"}, args...)
	_, err := util.RunGit(ctx, dir, argv...)
	return err
}
