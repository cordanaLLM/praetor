// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

//go:build !linux

package dogfood

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// The transcript store judges its directory and files by POSIX mode, which decides nothing
// outside Linux, where scheduling runs (#132). Elsewhere each entry point says the feature is
// unsupported instead of refusing on a mode the platform cannot produce.

func TestOpenPrivateScheduleDirectory_Negative_UnsupportedOffLinux(t *testing.T) {
	parent, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if _, err := openPrivateScheduleDirectory(parent, scheduleTranscriptDir, "schedule transcript store"); !errors.Is(err, errScheduleTranscriptsUnsupported) {
		t.Fatalf("open the transcript store off Linux: %v, want %v", err, errScheduleTranscriptsUnsupported)
	}
}

func TestReadStoredScheduleTranscript_Negative_UnsupportedOffLinux(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "t.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	source := SuiteTranscript{ID: "t", SHA256: transcriptSHA256([]byte("{}\n"))}
	if _, _, err := readStoredScheduleTranscript(t.Context(), root, "t.jsonl", source, dir); !errors.Is(err, errScheduleTranscriptsUnsupported) {
		t.Fatalf("read a stored transcript off Linux: %v, want %v", err, errScheduleTranscriptsUnsupported)
	}
}
