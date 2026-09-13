package gc

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func normalizeOptions(opts Options) (Options, []pool, map[string]bool, error) {
	if opts.RootDir == "" {
		opts.RootDir = "."
	}
	absRoot, err := filepath.Abs(opts.RootDir)
	if err != nil {
		return opts, nil, nil, err
	}
	info, err := os.Lstat(absRoot)
	if err != nil {
		return opts, nil, nil, fmt.Errorf("collection root does not exist: %w", err)
	}
	if !info.IsDir() {
		return opts, nil, nil, errors.New("collection root must be a directory, not a symlink")
	}
	opts.RootDir = absRoot
	if err := normalizeRetention(&opts); err != nil {
		return opts, nil, nil, err
	}
	if opts.WorktreesDir == "" {
		opts.WorktreesDir = ".standards/worktrees"
	}
	if opts.EphemeralDir == "" {
		opts.EphemeralDir = ".standards/ephemeral"
	}
	pools, err := normalizePools(opts)
	if err != nil {
		return opts, nil, nil, err
	}
	released, err := normalizeReleases(opts, pools)
	return opts, pools, released, err
}

func normalizeRetention(opts *Options) error {
	if opts.MaxWorktreeAge < 0 || opts.MaxArtifactAge < 0 {
		return errors.New("retention ages cannot be negative")
	}
	if opts.MaxWorktreeAge == 0 {
		opts.MaxWorktreeAge = DefaultMaxWorktreeAge
	}
	if opts.MaxArtifactAge == 0 {
		opts.MaxArtifactAge = DefaultMaxArtifactAge
	}
	return nil
}

func normalizePools(opts Options) ([]pool, error) {
	pools := []pool{{path: opts.WorktreesDir, worktree: true}, {path: opts.EphemeralDir}, {path: ".standards/tmp", cache: true}}
	for i := range pools {
		path, err := confinedPath(opts.RootDir, pools[i].path)
		if err != nil {
			return nil, err
		}
		pools[i].path = path
	}
	for i, left := range pools {
		for j, right := range pools {
			if j < i && (left.path == right.path || strings.HasPrefix(left.path, right.path+string(filepath.Separator)) || strings.HasPrefix(right.path, left.path+string(filepath.Separator))) {
				return nil, errors.New("collection pools cannot overlap")
			}
		}
	}
	return pools, nil
}

func normalizeReleases(opts Options, pools []pool) (map[string]bool, error) {
	if len(opts.ReleasedPaths) > MaxEntriesLimit {
		return nil, errors.New("release count exceeds collection limit")
	}
	released := make(map[string]bool, len(opts.ReleasedPaths))
	for _, path := range opts.ReleasedPaths {
		relative, err := confinedPath(opts.RootDir, path)
		if err != nil {
			return nil, err
		}
		parent := filepath.Dir(relative)
		found := false
		for _, p := range pools {
			if parent == p.path {
				found = true
			}
		}
		if !found {
			return nil, fmt.Errorf("released path must be a direct child of a collection pool: %s", relative)
		}
		if _, duplicate := released[relative]; duplicate {
			return nil, fmt.Errorf("duplicate release: %s", relative)
		}
		released[relative] = false
	}
	return released, nil
}

func confinedPath(root, path string) (string, error) {
	if path == "" {
		return "", errors.New("empty collection path")
	}
	relative := filepath.Clean(path)
	if filepath.IsAbs(relative) {
		var err error
		relative, err = filepath.Rel(root, relative)
		if err != nil {
			return "", err
		}
	}
	if !filepath.IsLocal(relative) || relative == "." {
		return "", fmt.Errorf("collection path must be below root: %s", path)
	}
	parts := strings.Split(relative, string(filepath.Separator))
	if len(parts) > 128 {
		return "", errors.New("collection path exceeds depth limit")
	}
	for _, part := range parts {
		if part == ".git" {
			return "", errors.New("git metadata cannot be a collection path")
		}
	}
	return relative, nil
}

// checkPath rejects symlink components before operations through the pinned root.
// os.Root confines subsequent filesystem access even if a component is replaced.
func checkPath(root *os.Root, path string) error {
	parts := strings.Split(path, string(filepath.Separator))
	if len(parts) > 128 {
		return errors.New("collection path exceeds depth limit")
	}
	current := ""
	for _, part := range parts {
		current = filepath.Join(current, part)
		info, err := root.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect %s: %w", current, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink collection path refused: %s", current)
		}
	}
	return nil
}
