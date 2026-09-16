// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func statFile(t *testing.T, mode os.FileMode) os.FileInfo {
	t.Helper()
	path := filepath.Join(t.TempDir(), "artefact")
	if err := os.WriteFile(path, []byte("x"), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

// Positive: an owner-only artefact is reported private on every platform. On POSIX
// that is measured from the mode; on Windows it is the asserted default, which is
// why the reason below must accompany it.
func TestArtefactPrivacyAcceptsAnOwnerOnlyArtefact(t *testing.T) {
	private, _ := ArtefactPrivacy(statFile(t, 0o600))
	if !private {
		t.Fatal("an owner-only artefact was reported as not private")
	}
}

// Negative: nil is not an artefact and is never private. A helper that answered
// "private" for a missing file would let a caller skip the check by failing to stat.
func TestArtefactPrivacyRefusesANilInfo(t *testing.T) {
	private, unverifiable := ArtefactPrivacy(nil)
	if private {
		t.Fatal("a nil FileInfo was reported private")
	}
	if unverifiable != "" {
		t.Fatalf("a nil FileInfo produced a platform reason: %q", unverifiable)
	}
}

// Boundary: the two-state contract. Either the platform decides from the mode and
// returns no reason, or it cannot and says so. A "private" answer with no reason on
// a platform that cannot measure it is the silent pass HISS-21 forbids.
func TestArtefactPrivacyAnswersOrStatesWhyItCannot(t *testing.T) {
	private, unverifiable := ArtefactPrivacy(statFile(t, 0o666))
	if runtime.GOOS == "windows" {
		if !private || unverifiable == "" {
			t.Fatalf("windows must assert privacy with a stated reason, got private=%v reason=%q",
				private, unverifiable)
		}
		return
	}
	if private {
		t.Fatal("a world-readable artefact was reported private on a platform that can tell")
	}
	if unverifiable != "" {
		t.Fatalf("a platform that measured the answer also returned a reason: %q", unverifiable)
	}
}

// Boundary: the notice is bounded to one emission and ignores an empty reason, so a
// caller may pass the second return unconditionally without flooding its output.
func TestNotePrivacyLimitationIgnoresAnEmptyReason(t *testing.T) {
	NotePrivacyLimitation("")
	NotePrivacyLimitation("")
}
