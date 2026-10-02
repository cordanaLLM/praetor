// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// ancestorWalk is where a walk from a path toward the root met something that exists: that
// ancestor (the path itself when it exists), what stat said of it, and the trailing segments
// of the path below it, "" when there are none.
type ancestorWalk struct {
	existing string
	info     fs.FileInfo
	rest     string
}

// walkToExistingAncestor climbs from path until stat finds something, at most
// maxPathAncestorWalk levels (HISS-02). Only fs.ErrNotExist moves the walk up: any other stat
// error, a root that does not exist and an exhausted bound are errors, so no caller reads an
// unanswered question as an absent path. It is the one ancestor walk that keeps the missing
// segments: resolveExistingAncestor resolves symlinks in what it finds, SplitAtExistingDir
// splits the path there.
func walkToExistingAncestor(path string, stat func(string) (fs.FileInfo, error)) (ancestorWalk, error) {
	current, rest := path, ""
	for i := 0; i < maxPathAncestorWalk; i++ {
		info, err := stat(current)
		if err == nil {
			return ancestorWalk{existing: current, info: info, rest: rest}, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return ancestorWalk{}, fmt.Errorf("util: inspect %q: %w", current, err)
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ancestorWalk{}, fmt.Errorf("util: no existing ancestor for %q", path)
		}
		rest = filepath.Join(filepath.Base(current), rest)
		current = parent
	}
	return ancestorWalk{}, fmt.Errorf("util: %q exceeds the %d level ancestor walk bound", path, maxPathAncestorWalk)
}

// DirSplit is a path split at the deepest directory that exists.
type DirSplit struct {
	// Dir is the deepest existing directory on the path: the path itself when it is one.
	Dir string
	// Rest is the path below Dir, "" when the path is Dir.
	Rest string
	// Exists reports whether the path itself exists. With a non-empty Rest it is then something
	// other than a directory, and Rest is its name in Dir.
	Exists bool
}

// SplitAtExistingDir splits path at the deepest directory that exists, so a caller can start
// a command in Dir and hand it Rest: the way to ask git about a file whose directory the
// working tree no longer holds, where starting git in that directory fails before git runs.
// Symlinks are followed, so a link to a directory is a directory and a dangling link is
// missing.
//
// A segment that is not a directory ends the directory part like a missing one, on every host
// alike: POSIX answers a path that runs through a regular file with ENOTDIR and Windows with a
// missing path (see DirectoryAbsent), and both are read here as "not there". Any other stat
// error and an exhausted walk bound are errors.
func SplitAtExistingDir(path string) (DirSplit, error) {
	walk, err := walkToExistingAncestor(filepath.Clean(path), statMissingBelowFile)
	if err != nil {
		return DirSplit{}, err
	}
	exists := walk.rest == ""
	if walk.info.IsDir() {
		return DirSplit{Dir: walk.existing, Rest: walk.rest, Exists: exists}, nil
	}
	rest := filepath.Join(filepath.Base(walk.existing), walk.rest)
	return DirSplit{Dir: filepath.Dir(walk.existing), Rest: rest, Exists: exists}, nil
}

// statMissingBelowFile is os.Stat with POSIX's ENOTDIR answered as fs.ErrNotExist, the answer
// Windows gives for a path that runs through a regular file.
func statMissingBelowFile(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if errors.Is(err, syscall.ENOTDIR) {
		return nil, &fs.PathError{Op: "stat", Path: path, Err: fs.ErrNotExist}
	}
	return info, err
}
