// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package state

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cordanaLLM/praetor/internal/util"
)

// StateLocation holds the resolved state directory and repository root.
type StateLocation struct {
	WorkingDir string
	RepoDir    string
	IsLinked   bool
}

// Notice returns the human announcement when using the main worktree's .workingdir.
func (l StateLocation) Notice() string {
	if l.IsLinked {
		return fmt.Sprintf("state: using the main worktree's %s at %s", WorkingDirName, l.WorkingDir)
	}
	return ""
}

// isRealDir reports whether path exists on disk as a real directory and not a symbolic link.
func isRealDir(path string) bool {
	info, err := os.Lstat(path)
	if err != nil {
		return false
	}
	return info.IsDir() && info.Mode()&os.ModeSymlink == 0
}

// resolveLinkedWorktreeState inspects dir with git rev-parse. If dir is a linked worktree
// whose main worktree possesses a real .workingdir, its path and repository root are returned.
func resolveLinkedWorktreeState(ctx context.Context, dir string) (string, string, bool) {
	if ctx == nil {
		return "", "", false
	}
	probeCtx, cancel := context.WithTimeout(ctx, util.GitProbeTimeout)
	defer cancel()

	result, err := util.RunGitProbe(probeCtx, dir, 4096, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", "", false
	}
	lines := strings.Split(strings.TrimSpace(string(result.Stdout)), "\n")
	if len(lines) < 2 {
		return "", "", false
	}
	gitDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(lines[0])))
	commonDir := filepath.Clean(filepath.FromSlash(strings.TrimSpace(lines[1])))
	if gitDir == commonDir {
		return "", "", false
	}
	mainRepo := filepath.Dir(commonDir)
	mainWorkingDir := filepath.Join(mainRepo, WorkingDirName)
	if !isRealDir(mainWorkingDir) {
		return "", "", false
	}
	return mainWorkingDir, mainRepo, true
}

// ResolveWorkingDir resolves the state folder for dir:
//  1. dir/.workingdir when it exists as a real directory.
//  2. Else, when dir is a linked worktree, the main worktree's .workingdir when that folder exists.
//  3. Else an error stating that .workingdir does not exist.
func ResolveWorkingDir(ctx context.Context, dir string) (StateLocation, error) {
	if dir == "" {
		dir = "."
	}
	localWorkingDir := filepath.Join(dir, WorkingDirName)
	if isRealDir(localWorkingDir) {
		return StateLocation{
			WorkingDir: localWorkingDir,
			RepoDir:    dir,
			IsLinked:   false,
		}, nil
	}

	mainWorkingDir, mainRepo, isLinked := resolveLinkedWorktreeState(ctx, dir)
	if isLinked {
		return StateLocation{
			WorkingDir: mainWorkingDir,
			RepoDir:    mainRepo,
			IsLinked:   true,
		}, nil
	}

	return StateLocation{}, fmt.Errorf("%s/ does not exist in %s (run 'praetorctl state init %s' first)",
		WorkingDirName, dir, dir)
}

// ledgerRoot returns the repository root that owns the state ledger: the main worktree root
// when root is a linked worktree without its own real .workingdir and the main one exists,
// else root.
func ledgerRoot(ctx context.Context, root string) string {
	loc, err := ResolveWorkingDir(ctx, root)
	if err == nil && loc.IsLinked {
		return loc.RepoDir
	}
	return root
}
