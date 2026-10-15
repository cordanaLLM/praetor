// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package util

import (
	"fmt"
	"os"
)

// UnverifiablePrivacy is the reason ModeIsProtection prints on this platform: a test that
// asserts a mode asserts something os.FileInfo.Mode() does not represent here.
const UnverifiablePrivacy = "file protection on this platform is an ACL that os.FileInfo.Mode() does not expose; " +
	"a mode assertion is not a privacy assertion here"

// ArtefactPrivacy reports whether the artefact at path, whose metadata is info, is readable
// only by its owner, and names what prevented the answer when the DACL could not be read.
//
// Windows synthesises the mode from the read-only attribute alone, so an ordinary file
// always reports 0666; the file's protection is its DACL. This reads it (OwnerOnlyAccess):
// an artefact is private when no one but this account, SYSTEM and Administrators may open
// it. Before #101 the answer was asserted, never verified.
func ArtefactPrivacy(path string, info os.FileInfo) (private bool, unverifiable string) {
	if info == nil {
		return false, ""
	}
	private, _, err := OwnerOnlyAccess(path)
	if err != nil {
		return false, fmt.Sprintf("the DACL of %s could not be read: %v", path, err)
	}
	return private, ""
}

// TightenCommand is the command that makes the artefact at path private: an icacls call
// that cuts inheritance and grants this account, SYSTEM and Administrators full access.
func TightenCommand(path string, _ bool) string {
	sid, err := currentUserSID()
	if err != nil {
		sid = "<your account SID>"
	}
	return fmt.Sprintf(`icacls "%s" /inheritance:r /grant:r *%s:F *S-1-5-18:F *S-1-5-32-544:F`, path, sid)
}

// ModeIsProtection reports whether a file's mode is what protects it on this platform.
//
// On Windows it is not -- the ACL is -- so a test asserting a mode such as 0600 is asserting
// something the platform cannot express, and it fails for a reason unrelated to the code
// under test. Callers use this as a precondition on exactly that assertion, so the rest of
// the case still runs. The reason is printed once per process rather than skipped silently.
func ModeIsProtection() bool {
	NotePrivacyLimitation(UnverifiablePrivacy)
	return false
}
