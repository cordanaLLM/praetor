// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package topology

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/cordanaLLM/praetor/internal/util"
)

// Workstation dev-root environment names and containers. PRAETOR_DEV_ROOT is the canonical
// name used by AGENTS.md and scripts/adopt_repos.sh; PRAETOR_DEV_DIR is the earlier name,
// kept as a fallback so an operator environment that still sets it keeps selecting the same
// tree. WorktreesContainer is the built-in organization container holding linked worktrees.
const (
	DevRootEnv         = "PRAETOR_DEV_ROOT"
	LegacyDevRootEnv   = "PRAETOR_DEV_DIR"
	WorktreesContainer = "worktrees"
)

// ConfiguredDevRoot returns the dev root the environment names: $PRAETOR_DEV_ROOT, else
// $PRAETOR_DEV_DIR, else "" when neither is set.
func ConfiguredDevRoot() string {
	for _, name := range [...]string{DevRootEnv, LegacyDevRootEnv} {
		if dir := os.Getenv(name); dir != "" {
			return dir
		}
	}
	return ""
}

// ResolveHomeSubdir returns explicit when it is set, and otherwise joins segs onto the
// user's home directory. It never degrades to a working-directory-relative path: when the
// home directory cannot be resolved the caller gets an error naming the flag to pass
// instead, rather than silently retargeting the command at the current directory.
func ResolveHomeSubdir(explicit, flagName string, segs ...string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory (pass %s explicitly): %w", flagName, err)
	}
	if home == "" {
		return "", fmt.Errorf("home directory is empty: pass %s explicitly", flagName)
	}
	return filepath.Join(append([]string{home}, segs...)...), nil
}

// ResolveDevRootDir is the one resolver for the workstation dev root: explicit when it is
// set, else $PRAETOR_DEV_ROOT, else $PRAETOR_DEV_DIR, else <home>/dev. Without a usable
// home directory it returns an error naming flagName instead of a path relative to the
// working directory.
func ResolveDevRootDir(explicit, flagName string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if dir := ConfiguredDevRoot(); dir != "" {
		return dir, nil
	}
	root, err := ResolveHomeSubdir("", flagName, "dev")
	if err != nil {
		return "", fmt.Errorf("%s and %s are unset: %w", DevRootEnv, LegacyDevRootEnv, err)
	}
	return root, nil
}

// RepositoryRoot returns the canonical directory of the main repository checkout that owns
// dir (the parent of its GitCommonDir). dir may be the checkout root, a subdirectory inside
// it, or a linked worktree; no git subprocess is started.
func RepositoryRoot(ctx context.Context, dir string) (string, error) {
	checkout, err := enclosingCheckout(ctx, dir)
	if err != nil {
		return "", err
	}
	commonDir, err := GitCommonDir(ctx, checkout)
	if err != nil {
		return "", err
	}
	return filepath.Dir(commonDir), nil
}

func enclosingCheckout(ctx context.Context, dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("%w: empty path", ErrNotCheckout)
	}
	resolved, err := util.ResolveExistingPath(ctx, dir)
	if err != nil {
		return "", fmt.Errorf("resolve checkout path %s: %w", dir, err)
	}
	cur := filepath.Clean(resolved)
	for range maxGitDirSegments {
		if err := scanInterrupted(ctx, "checkout lookup"); err != nil {
			return "", err
		}
		if HasValidGitRepo(cur) {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return "", fmt.Errorf("%w: %s", ErrNotCheckout, dir)
}
