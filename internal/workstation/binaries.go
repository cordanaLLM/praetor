// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package workstation

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// goosWindows names the only operating system that needs the rename-aside step in
// swapInto. It is a plain string constant, not a build-tagged file, so the branch runs
// (and is provably covered) on every CI leg regardless of the host it executes on.
const goosWindows = "windows"

// targetKind classifies an existing installation target before anything is written.
type targetKind int

const (
	targetAbsent targetKind = iota
	targetFile
	targetSymlink
)

// targetState is one target's classification: enough to back it up and, on failure,
// restore it exactly.
type targetState struct {
	kind   targetKind
	target string // symlink target, when kind == targetSymlink
	mode   os.FileMode
}

// inspectTargets classifies every binary and alias path in binDir, refusing before
// anything is touched if any of them is a foreign target (ErrForeignTarget).
func inspectTargets(binDir string) (map[string]targetState, error) {
	states := make(map[string]targetState, len(allTargetNames()))
	for _, name := range allTargetNames() {
		expected := ""
		if primary, ok := primaryFor(name); ok {
			expected = primary
		}
		state, err := inspectTarget(filepath.Join(binDir, name), expected)
		if err != nil {
			return nil, err
		}
		states[name] = state
	}
	return states, nil
}

// inspectTarget classifies path without following a symlink. expectedSymlinkTarget is the
// only symlink content this package ever creates at path: the primary binary name for an
// alias, or "" for a primary binary (which must never legitimately be a symlink here).
func inspectTarget(path, expectedSymlinkTarget string) (targetState, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return targetState{kind: targetAbsent}, nil
	}
	if err != nil {
		return targetState{}, fmt.Errorf("workstation: inspect installation target %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return inspectSymlink(path, expectedSymlinkTarget)
	}
	if !info.Mode().IsRegular() {
		return targetState{}, fmt.Errorf("%w: %s is not a regular file", ErrForeignTarget, path)
	}
	return targetState{kind: targetFile, mode: info.Mode().Perm()}, nil
}

func inspectSymlink(path, expected string) (targetState, error) {
	target, err := os.Readlink(path)
	if err != nil {
		return targetState{}, fmt.Errorf("workstation: read installation symlink %s: %w", path, err)
	}
	if expected == "" || target != expected {
		return targetState{}, fmt.Errorf("%w: %s -> %s", ErrForeignTarget, path, target)
	}
	return targetState{kind: targetSymlink, target: target}, nil
}

// backupTargets copies every existing target into a fresh directory under binDir, so a
// failed swap can be restored. It copies rather than moves, matching
// scripts/dev_install.py's prepare_backup: a failure during backup itself leaves every
// target untouched. Call it only when at least one target is not targetAbsent.
func backupTargets(binDir string, states map[string]targetState) (backupDir string, err error) {
	backupDir, err = os.MkdirTemp(binDir, ".praetor-workstation-backup-")
	if err != nil {
		return "", fmt.Errorf("workstation: create backup directory: %w", err)
	}
	for _, name := range allTargetNames() {
		state := states[name]
		if state.kind == targetAbsent {
			continue
		}
		if err := copyTarget(filepath.Join(binDir, name), filepath.Join(backupDir, name), state); err != nil {
			return backupDir, err
		}
	}
	return backupDir, nil
}

// copyTarget recreates state at destination: a symlink with the same target, or a copy of
// source's bytes and mode.
func copyTarget(source, destination string, state targetState) error {
	if state.kind == targetSymlink {
		return os.Symlink(state.target, destination)
	}
	return copyFileMode(source, destination, state.mode)
}

// copyFileMode copies source's bytes to a new file at destination with the given mode.
func copyFileMode(source, destination string, mode os.FileMode) (err error) {
	// #nosec G304 -- source is always a path this package itself resolved: an installation
	// target under an operator-configured bin directory, or this package's own build output.
	in, err := os.Open(source)
	if err != nil {
		return fmt.Errorf("workstation: open %s: %w", source, err)
	}
	defer func() {
		if cerr := in.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("workstation: close %s: %w", source, cerr)
		}
	}()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return fmt.Errorf("workstation: create %s: %w", destination, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return fmt.Errorf("workstation: copy %s to %s: %w", source, destination, err)
	}
	return out.Close()
}

// stageAndSwap writes into a same-directory temporary entry (via write) and then swaps it
// into destinationName under binDir. Staging beside the destination keeps the final rename
// on one filesystem, so it is atomic.
func stageAndSwap(goos, binDir, destinationName string, write func(path string) error) error {
	stageDir, err := os.MkdirTemp(binDir, ".praetor-workstation-place-")
	if err != nil {
		return fmt.Errorf("workstation: create staging directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(stageDir) }()
	staged := filepath.Join(stageDir, destinationName)
	if err := write(staged); err != nil {
		return err
	}
	return swapInto(goos, staged, filepath.Join(binDir, destinationName))
}

// swapInto places staged at destination atomically.
//
// On every OS but Windows, os.Rename replaces an existing file in one atomic step, even
// while another process holds it open. Windows refuses to rename onto a file that is
// memory-mapped for execution -- the running praetorctl itself -- so goos == "windows"
// renames the previous file aside first, freeing the destination name, before renaming the
// staged file into place. goos is a parameter rather than a runtime.GOOS read so this
// branch, and both its outcomes, run on every CI leg rather than only a Windows one.
func swapInto(goos, staged, destination string) error {
	if goos == goosWindows {
		if err := renameAside(destination); err != nil {
			return err
		}
	}
	if err := os.Rename(staged, destination); err != nil {
		return fmt.Errorf("workstation: place %s: %w", destination, err)
	}
	return nil
}

// renameAside moves an existing destination out of the way, freeing its name for the
// rename that follows. A destination that does not exist yet (first install) needs no
// aside step. The aside file is scratch, not the durable backup (backupTargets already
// copied it), so a best-effort removal is enough; a leftover self-heals on the next swap.
func renameAside(destination string) error {
	if _, err := os.Lstat(destination); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return fmt.Errorf("workstation: inspect %s before rename-aside: %w", destination, err)
	}
	aside := destination + ".praetor-previous"
	if err := os.Rename(destination, aside); err != nil {
		return fmt.Errorf("workstation: rename %s aside: %w", destination, err)
	}
	_ = os.Remove(aside)
	return nil
}

// restoreFromBackup undoes swaps already performed (done, in the order they were applied),
// most recent first, best effort: it collects every restore failure rather than stopping
// at the first, so the caller's error names exactly what is still wrong.
func restoreFromBackup(goos, binDir, backupDir string, states map[string]targetState, done []string) []string {
	var failures []string
	for i := len(done) - 1; i >= 0; i-- {
		name := done[i]
		if err := restoreOne(goos, binDir, backupDir, name, states[name]); err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", name, err))
		}
	}
	return failures
}

func restoreOne(goos, binDir, backupDir, name string, state targetState) error {
	destination := filepath.Join(binDir, name)
	if state.kind == targetAbsent {
		if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("workstation: remove %s: %w", destination, err)
		}
		return nil
	}
	return stageAndSwap(goos, binDir, name, func(path string) error {
		return copyTarget(filepath.Join(backupDir, name), path, state)
	})
}

// allAbsent reports whether every inspected target was absent, the first-install case
// where there is nothing to back up or roll back.
func allAbsent(states map[string]targetState) bool {
	for _, state := range states {
		if state.kind != targetAbsent {
			return false
		}
	}
	return true
}
