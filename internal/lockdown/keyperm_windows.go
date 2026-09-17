// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package lockdown

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// verifyKeyPermissions confines the signing key to the per-user configuration tree.
//
// The POSIX comparison used on other platforms cannot be used here, and the reason is
// not that it is merely approximate: on Windows a file's protection is its NTFS ACL,
// which os.FileInfo.Mode() does not represent at all. Go synthesises the mode from the
// read-only attribute alone, so an ordinary file always reports 0666 and a read-only
// one 0444. Neither can satisfy `Perm()&0o077 == 0`, so applying the POSIX rule here
// enforced nothing while making the key permanently unreadable -- and the error blamed
// the file's mode, which was never the cause and cannot be repaired by changing it.
//
// What is enforced instead is containment: the key must live inside this user's own
// configuration directory, whose ACL is what actually restricts it. That is a real
// refusal -- a key on a shared volume or another user's profile is rejected -- and it
// is stated rather than skipped, because a check that quietly passes would be the
// unexamined thing certified as clean that this lattice exists to prevent.
//
// The residual gap is deliberate and narrower than the one it replaces: a key whose
// ACL has been widened inside that directory is not detected. Reading the ACL needs
// golang.org/x/sys/windows, which this module does not depend on; see the discussion
// in the private-artefact permissions issue.
func verifyKeyPermissions(path string, info os.FileInfo) error {
	if info.IsDir() {
		return fmt.Errorf("%w: %s is a directory", ErrInsecureKeyPerm, path)
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("%w: the per-user configuration directory is unknown: %w",
			ErrInsecureKeyPerm, err)
	}
	resolvedKey, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("%w: %s cannot be resolved: %w", ErrInsecureKeyPerm, path, err)
	}
	resolvedDir, err := filepath.Abs(configDir)
	if err != nil {
		return fmt.Errorf("%w: %s cannot be resolved: %w", ErrInsecureKeyPerm, configDir, err)
	}
	relative, err := filepath.Rel(resolvedDir, resolvedKey)
	if err != nil || !filepath.IsLocal(relative) {
		return fmt.Errorf(
			"%w: %s is outside the per-user configuration directory %s, whose ACL is what "+
				"protects it on this platform",
			ErrInsecureKeyPerm, resolvedKey, resolvedDir)
	}
	if strings.HasPrefix(relative, "..") {
		return fmt.Errorf("%w: %s escapes %s", ErrInsecureKeyPerm, resolvedKey, resolvedDir)
	}
	return nil
}
