// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// statFile writes an artefact through WriteFileSecure, which makes it private on every
// platform (on Windows by its DACL, #101), then sets mode and returns its path and metadata.
func statFile(t *testing.T, mode os.FileMode) (string, os.FileInfo) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artefact")
	if err := WriteFileSecure(path, []byte("x"), SecureFilePerm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, info
}

// Positive: an owner-only artefact is reported private on every platform: on POSIX from the
// mode, on Windows from the DACL WriteFileSecure gave it.
func TestArtefactPrivacyAcceptsAnOwnerOnlyArtefact(t *testing.T) {
	private, _ := ArtefactPrivacy(statFile(t, 0o600))
	if !private {
		t.Fatal("an owner-only artefact was reported as not private")
	}
}

// Negative: nil is not an artefact and is never private. A helper that answered
// "private" for a missing file would let a caller skip the check by failing to stat.
func TestArtefactPrivacyRefusesANilInfo(t *testing.T) {
	private, unverifiable := ArtefactPrivacy("", nil)
	if private {
		t.Fatal("a nil FileInfo was reported private")
	}
	if unverifiable != "" {
		t.Fatalf("a nil FileInfo produced a platform reason: %q", unverifiable)
	}
}

// Boundary: both platforms measure the answer and return no reason. On POSIX a 0666 file is
// readable by others; on Windows the mode says nothing and the owner-only DACL decides (#101).
func TestArtefactPrivacyAnswersOrStatesWhyItCannot(t *testing.T) {
	private, unverifiable := ArtefactPrivacy(statFile(t, 0o666))
	if unverifiable != "" {
		t.Fatalf("a readable artefact produced a reason instead of an answer: %q", unverifiable)
	}
	if want := runtime.GOOS == "windows"; private != want {
		t.Fatalf("0666 artefact with an owner-only DACL: private=%v, want %v", private, want)
	}
}

// Positive: the refusal names the path and the command that makes the artefact private.
func TestPrivacyRefusal_Positive_NamesPathAndCommand(t *testing.T) {
	path, info := statFile(t, 0o600)
	err := PrivacyRefusal(path, info)
	if err == nil || !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), TightenCommand(path, false)) {
		t.Fatalf("refusal = %v, want it to name %s and %s", err, path, TightenCommand(path, false))
	}
}

// Boundary: the notice is bounded to one emission and ignores an empty reason, so a
// caller may pass the second return unconditionally without flooding its output.
func TestNotePrivacyLimitationIgnoresAnEmptyReason(t *testing.T) {
	NotePrivacyLimitation("")
	NotePrivacyLimitation("")
}
