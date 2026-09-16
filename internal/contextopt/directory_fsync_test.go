// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package contextopt_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// directorySync matches a flush issued on a directory handle. Windows refuses
// FlushFileBuffers on one with access denied, so an unguarded call returns an error after the
// write has already landed -- a success reported as a failure (#125).
//
// The receiver name is how a directory handle is distinguished from a file handle here. A file
// flush is correct on every platform and must not be caught.
var directorySync = regexp.MustCompile(`\b(dir|directory|root|parent)\w*\.Sync\(\)`)

// syncDirectoryHome is the one place allowed to flush a directory, because it carries the
// platform split. Everything else must call it rather than repeat the decision (HISS-19).
const syncDirectoryHome = "replace.go"

// scanRoots are the trees this rule governs.
var scanRoots = []string{"internal", "cmd"}

func TestDirectoryFsyncHappensOnlyWhereThePlatformSplitLives(t *testing.T) {
	repo := filepath.Join("..", "..")
	var offenders []string
	for _, tree := range scanRoots {
		err := filepath.WalkDir(filepath.Join(repo, tree), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			for number, line := range strings.Split(string(data), "\n") {
				if !directorySync.MatchString(line) {
					continue
				}
				if filepath.Base(path) == syncDirectoryHome {
					continue
				}
				offenders = append(offenders,
					filepath.ToSlash(path)+":"+itoa(number+1)+": "+strings.TrimSpace(line))
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}
	if len(offenders) > 0 {
		t.Errorf("directory fsync is POSIX-only and must go through contextopt.SyncDirectory, "+
			"which carries the Windows split. Unguarded call(s):\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

// Negative: the rule must actually match the shape it forbids, and must spare a file flush --
// otherwise it would pass by matching nothing, or ban a call that is correct everywhere.
func TestDirectoryFsyncPatternMatchesTheRightShape(t *testing.T) {
	forbidden := []string{
		"return errors.Join(directory.Sync(), directory.Close())",
		"return errors.Join(dir.Sync(), dir.Close())",
		"if err := root.Sync(); err != nil {",
		"parentDir.Sync()",
	}
	for _, line := range forbidden {
		if !directorySync.MatchString(line) {
			t.Errorf("pattern must catch a directory flush: %q", line)
		}
	}
	allowed := []string{
		"err = errors.Join(writeErr, file.Sync(), file.Close())",
		"if err := file.Sync(); err != nil {",
		"return errors.Join(handle.Sync(), handle.Close())",
		"// directory sync is POSIX-only",
	}
	for _, line := range allowed {
		if directorySync.MatchString(line) {
			t.Errorf("pattern must spare a file flush: %q", line)
		}
	}
}

// itoa avoids pulling strconv in for one conversion.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
