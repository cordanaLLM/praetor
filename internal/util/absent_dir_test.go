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
	"testing"
)

// readDirError returns the error the host itself produces reading dir, so every case below
// asserts against the real platform behaviour rather than a constructed error.
func readDirError(t *testing.T, dir string) error {
	t.Helper()
	_, err := os.ReadDir(dir)
	if err == nil {
		t.Fatalf("reading %s unexpectedly succeeded", dir)
	}
	return err
}

func TestDirectoryAbsent_Positive_MissingUnderExistingDirectory(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{
		filepath.Join(root, "missing"),
		filepath.Join(root, "missing", "deeper", "leaf"),
	} {
		if !DirectoryAbsent(dir, readDirError(t, dir)) {
			t.Fatalf("a directory that does not exist was not reported absent: %s", dir)
		}
	}
}

func TestDirectoryAbsent_Negative_RegularFileIsNotAbsence(t *testing.T) {
	file := filepath.Join(t.TempDir(), "brain.txt")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	// On Windows both reads fail with an error that satisfies fs.ErrNotExist; on POSIX they
	// fail with ENOTDIR. Either way the path is misconfigured, not absent.
	for _, dir := range []string{file, filepath.Join(file, "logs")} {
		if DirectoryAbsent(dir, readDirError(t, dir)) {
			t.Fatalf("a path naming or running through a regular file was reported absent: %s", dir)
		}
	}
}

func TestDirectoryAbsent_Negative_OtherErrorsAreNotAbsence(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing")
	cases := map[string]error{
		"nil error":           nil,
		"unrelated error":     errors.New("permission denied"),
		"wrapped unrelated":   fmt.Errorf("read: %w", fs.ErrPermission),
		"empty directory arg": fs.ErrNotExist,
	}
	for name, err := range cases {
		dir := missing
		if name == "empty directory arg" {
			dir = ""
		}
		if DirectoryAbsent(dir, err) {
			t.Fatalf("%s: reported absent", name)
		}
	}
}

func TestDirectoryAbsent_Boundary_ExistingDirectoryAndWrappedError(t *testing.T) {
	root := t.TempDir()
	// A directory that exists by the time absence is judged is not absent, whatever the
	// earlier read said: the caller must surface its error rather than read nothing.
	if DirectoryAbsent(root, fs.ErrNotExist) {
		t.Fatal("an existing directory was reported absent")
	}
	missing := filepath.Join(root, "missing") + string(filepath.Separator)
	wrapped := fmt.Errorf("read transcripts root: %w", readDirError(t, missing))
	if !DirectoryAbsent(missing, wrapped) {
		t.Fatal("a wrapped not-exist error for a missing directory with a trailing separator was not reported absent")
	}
}
