// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build windows

package util

import "os"

// UnverifiablePrivacy is the reason ArtefactPrivacy returns on a platform whose file
// protection os.FileInfo.Mode() does not represent. Callers print it rather than
// discarding it: an unexamined artefact reported as private is the state HISS-21
// forbids, and saying so is what separates a skip from a silent pass.
const UnverifiablePrivacy = "file protection on this platform is an ACL that os.FileInfo.Mode() does not expose; " +
	"privacy is inherited from the containing directory and is not verified here"

// ArtefactPrivacy reports whether an artefact is readable only by its owner, and
// names what prevents the answer being verified where it cannot be.
//
// Windows synthesises the mode from the read-only attribute alone, so an ordinary
// file always reports 0666 and a read-only one 0444. Neither can satisfy
// Perm()&0o077 == 0. Applying the POSIX comparison here therefore refused every
// artefact while proving nothing about any of them, and the refusal named a mode
// that cannot be changed to satisfy it.
//
// The honest answer is that this platform cannot decide with the information the
// standard library exposes. Reading the real answer needs the ACL via
// golang.org/x/sys/windows, which this module deliberately does not depend on --
// go.mod carries exactly one requirement. Until that trade is made, the artefact is
// treated as private and the caller is handed the reason to print, so the gap is
// visible in output rather than hidden in a pass.
func ArtefactPrivacy(info os.FileInfo) (private bool, unverifiable string) {
	if info == nil {
		return false, ""
	}
	return true, UnverifiablePrivacy
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
