// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Positive: equal text passes, and the update switch rewrites the golden with got.
func TestAssertGolden_Positive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.golden")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, path, "one\ntwo\n")
	if recorder.fatal != "" {
		t.Fatalf("equal text failed: %s", recorder.fatal)
	}
	t.Setenv(GoldenUpdateEnv, "1")
	target := filepath.Join(t.TempDir(), "nested", "new.golden")
	AssertGolden(recorder, target, "fresh\n")
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "fresh\n" || recorder.fatal != "" {
		t.Fatalf("update wrote %q, err=%v, failure %q", data, err, recorder.fatal)
	}
}

// Negative: a differing line fails and names the line; a missing golden fails and names the
// update switch.
func TestAssertGolden_Negative(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.golden")
	if err := os.WriteFile(path, []byte("one\ntwo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, path, "one\nTWO\n")
	if !strings.Contains(recorder.fatal, "at line 2") || !strings.Contains(recorder.fatal, `"TWO"`) {
		t.Fatalf("difference not located: %q", recorder.fatal)
	}
	recorder = &recordingTB{TB: t}
	AssertGolden(recorder, filepath.Join(t.TempDir(), "absent.golden"), "x")
	if !strings.Contains(recorder.fatal, GoldenUpdateEnv) {
		t.Fatalf("missing golden did not name the update switch: %q", recorder.fatal)
	}
}

// Boundary: a CRLF checkout of the golden compares equal, while a missing final newline is a
// difference on the line past the last one.
func TestAssertGolden_Boundary(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.golden")
	if err := os.WriteFile(path, []byte("one\r\ntwo\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, path, "one\ntwo\n")
	if recorder.fatal != "" {
		t.Fatalf("CRLF checkout of the golden failed: %s", recorder.fatal)
	}
	AssertGolden(recorder, path, "one\ntwo")
	if !strings.Contains(recorder.fatal, "at line 3") || !strings.Contains(recorder.fatal, "<end of text>") {
		t.Fatalf("missing final newline not located: %q", recorder.fatal)
	}
}
