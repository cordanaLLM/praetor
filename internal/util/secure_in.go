// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

// OpenDirectoryIn pins one direct child of parent, refusing symbolic links and replacement
// of the directory expected names. No file data is accessed before identity is verified.
// os.Root confines the open itself; a raced escaping link cannot be followed outside parent.
// Name-based roots on js/plan9 cannot preserve this guarantee across rename and are refused.
func OpenDirectoryIn(parent *os.Root, name string, expected os.FileInfo) (_ *os.Root, err error) {
	if err := checkDirectoryRequest(parent, name, expected); err != nil {
		return nil, err
	}
	if err := CheckDirectoryIdentity(parent, name, expected); err != nil {
		return nil, err
	}
	child, err := parent.OpenRoot(name)
	if err != nil {
		return nil, fmt.Errorf("directory %q replaced or unavailable while opening: %w", name, err)
	}
	opened, err := child.Stat(".")
	if err == nil && (!opened.IsDir() || !os.SameFile(expected, opened)) {
		err = fmt.Errorf("directory %q replaced while opening", name)
	}
	if err == nil {
		err = CheckDirectoryIdentity(parent, name, expected)
	}
	if err != nil {
		return nil, errors.Join(err, child.Close())
	}
	return child, nil
}

// checkDirectoryRequest restricts pinning to platforms with persistent handles and one child name.
func checkDirectoryRequest(parent *os.Root, name string, expected os.FileInfo) error {
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return fmt.Errorf("pinned directory identity unsupported on %s", runtime.GOOS)
	}
	if parent == nil || expected == nil || !expected.IsDir() || filepath.Base(name) != name || name == "." || name == ".." {
		return fmt.Errorf("open directory: parent, direct child and directory identity required")
	}
	return nil
}

// CheckDirectoryIdentity rechecks a name through its trusted parent; links and a different
// device/inode (Windows volume/file index) are replacements even when both are directories.
func CheckDirectoryIdentity(parent *os.Root, name string, expected os.FileInfo) error {
	current, err := parent.Lstat(name)
	if err != nil {
		return fmt.Errorf("directory %q replaced or unavailable: %w", name, err)
	}
	if !current.IsDir() || !os.SameFile(expected, current) {
		return fmt.Errorf("directory %q replaced (symlinks refused)", name)
	}
	return nil
}

// MkdirIn is MkdirConfined using a caller-owned root, kept across multiple operations.
// The root is never reopened by name; relative links remain confined by os.Root.
func MkdirIn(base *os.Root, rel string, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureDirPerm)
	if err != nil {
		return err
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return nil
	}
	if err := base.MkdirAll(rel, perm); err != nil {
		return fmt.Errorf("util: create directory %q below %q: %w", rel, base.Name(), err)
	}
	info, err := base.Stat(rel)
	if err != nil {
		return fmt.Errorf("util: stat directory %q below %q: %w", rel, base.Name(), err)
	}
	if !info.IsDir() {
		return fmt.Errorf("util: %q below %q is not a directory", rel, base.Name())
	}
	if err := tightenMode(filepath.Join(base.Name(), rel), info, perm, func(mode os.FileMode) error {
		return base.Chmod(rel, mode)
	}); err != nil {
		return err
	}
	return restrictOpened(func() (*os.File, error) { return base.Open(rel) }, perm)
}

// WriteFileExclusiveIn is WriteFileConfinedExclusive using a caller-owned root; staging,
// private permissions, sync and publication reuse the same implementation as path callers.
func WriteFileExclusiveIn(base *os.Root, rel string, data []byte, perm os.FileMode) error {
	perm, err := effectivePerm(perm, SecureFilePerm)
	if err != nil {
		return err
	}
	rel = filepath.Clean(rel)
	if rel == "." {
		return fmt.Errorf("%w: %q", ErrRootItself, base.Name())
	}
	return inRootDirectory(base, filepath.Dir(rel), func(dir *os.Root) error {
		return createExclusively(dir, filepath.Base(rel), data, filePermission{mode: perm, exact: true})
	})
}

// inRootDirectory lends a pinned child without reopening base's name; only handles it
// creates are closed, so the caller retains ownership of base across directory operations.
func inRootDirectory(base *os.Root, sub string, fn func(*os.Root) error) (err error) {
	if sub == "." {
		return fn(base)
	}
	child, err := base.OpenRoot(sub)
	if err != nil {
		return fmt.Errorf("util: open directory %q below %q: %w", sub, base.Name(), err)
	}
	defer closePinned(child, &err)
	return fn(child)
}
