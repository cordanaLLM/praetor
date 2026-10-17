// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !windows

package lockdown

import (
	"fmt"
	"os"
)

// VerifyKeyPermissions refuses a signing or secret key any other account can read.
//
// The POSIX mode is the file's protection here, so the mode is the check. See
// keyperm_windows.go for what the Windows build enforces instead, and why the
// same comparison there enforces nothing (HISS-21).
func VerifyKeyPermissions(path string, info os.FileInfo) error {
	if info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%w: %s is %#o, want 0600 (chmod 600 it)", ErrInsecureKeyPerm, path, info.Mode().Perm())
	}
	return nil
}
