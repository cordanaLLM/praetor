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

// goldenPackageDir makes a fresh temporary directory the test's working directory, standing
// in for the package directory AssertGolden resolves golden paths against.
func goldenPackageDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Chdir(dir)
	return dir
}

// Positive: equal text passes, and the update switch rewrites the golden with got.
func TestAssertGolden_Positive(t *testing.T) {
	dir := goldenPackageDir(t)
	if err := os.WriteFile(filepath.Join(dir, "out.golden"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, "out.golden", "one\ntwo\n")
	if recorder.fatal != "" {
		t.Fatalf("equal text failed: %s", recorder.fatal)
	}
	t.Setenv(GoldenUpdateEnv, "1")
	target := filepath.Join("nested", "new.golden")
	AssertGolden(recorder, target, "fresh\n")
	data, err := os.ReadFile(filepath.Join(dir, target))
	if err != nil || string(data) != "fresh\n" || recorder.fatal != "" {
		t.Fatalf("update wrote %q, err=%v, failure %q", data, err, recorder.fatal)
	}
}

// Negative: a differing line fails and names the line; a missing golden fails and names the
// update switch; a path outside the package directory fails in both modes and nothing is
// written outside it.
func TestAssertGolden_Negative(t *testing.T) {
	dir := goldenPackageDir(t)
	if err := os.WriteFile(filepath.Join(dir, "out.golden"), []byte("one\ntwo\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, "out.golden", "one\nTWO\n")
	if !strings.Contains(recorder.fatal, "at line 2") || !strings.Contains(recorder.fatal, `"TWO"`) {
		t.Fatalf("difference not located: %q", recorder.fatal)
	}
	recorder = &recordingTB{TB: t}
	AssertGolden(recorder, "absent.golden", "x")
	if !strings.Contains(recorder.fatal, GoldenUpdateEnv) {
		t.Fatalf("missing golden did not name the update switch: %q", recorder.fatal)
	}
	outside := filepath.Join(t.TempDir(), "outside.golden")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaping := []string{outside, filepath.Join("..", filepath.Base(filepath.Dir(outside)), "outside.golden")}
	// Compare mode passes the outside file's own text, update mode different text, so an
	// unconfined read would pass silently and an unconfined write would change the file.
	for _, mode := range []struct{ update, got string }{{"", "x"}, {"1", "y"}} {
		t.Setenv(GoldenUpdateEnv, mode.update)
		for _, path := range escaping {
			recorder = &recordingTB{TB: t}
			AssertGolden(recorder, path, mode.got)
			if !strings.Contains(recorder.fatal, "golden") || !strings.Contains(recorder.fatal, path) {
				t.Errorf("update=%q: golden %s outside the package directory accepted: %q", mode.update, path, recorder.fatal)
			}
		}
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "x" {
		t.Fatalf("golden outside the package directory was rewritten: %q, %v", data, err)
	}
}

// AssertGoldenIn compares and rewrites a golden below another directory (positive), refuses a
// directory that does not exist and a path escaping it (negative), and with dir "." is
// AssertGolden (boundary).
func TestAssertGoldenIn(t *testing.T) {
	dir := goldenPackageDir(t)
	if err := os.MkdirAll(filepath.Join(dir, "top", "docs"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "top", "docs", "page.md"), []byte("page\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGoldenIn(recorder, "top", "docs/page.md", "page\n")
	if recorder.fatal != "" {
		t.Fatalf("equal text below another directory failed: %s", recorder.fatal)
	}
	AssertGoldenIn(recorder, "absent", "page.md", "page\n")
	if !strings.Contains(recorder.fatal, "open directory absent") {
		t.Fatalf("a missing directory was accepted: %q", recorder.fatal)
	}
	recorder = &recordingTB{TB: t}
	t.Setenv(GoldenUpdateEnv, "1")
	AssertGoldenIn(recorder, "top", "../escape.md", "x\n")
	if recorder.fatal == "" {
		t.Fatal("a golden path escaping the directory was written")
	}
	recorder = &recordingTB{TB: t}
	AssertGoldenIn(recorder, "top", "docs/page.md", "new\n")
	AssertGoldenIn(recorder, ".", "local.golden", "local\n")
	page, pageErr := os.ReadFile(filepath.Join(dir, "top", "docs", "page.md"))
	local, localErr := os.ReadFile(filepath.Join(dir, "local.golden"))
	if pageErr != nil || localErr != nil || string(page) != "new\n" || string(local) != "local\n" || recorder.fatal != "" {
		t.Fatalf("update wrote %q and %q (%v, %v), failure %q", page, local, pageErr, localErr, recorder.fatal)
	}
}

// Boundary: a CRLF checkout of the golden compares equal, while a missing final newline is a
// difference on the line past the last one.
func TestAssertGolden_Boundary(t *testing.T) {
	dir := goldenPackageDir(t)
	if err := os.WriteFile(filepath.Join(dir, "out.golden"), []byte("one\r\ntwo\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recorder := &recordingTB{TB: t}
	AssertGolden(recorder, "out.golden", "one\ntwo\n")
	if recorder.fatal != "" {
		t.Fatalf("CRLF checkout of the golden failed: %s", recorder.fatal)
	}
	AssertGolden(recorder, "out.golden", "one\ntwo")
	if !strings.Contains(recorder.fatal, "at line 3") || !strings.Contains(recorder.fatal, "<end of text>") {
		t.Fatalf("missing final newline not located: %q", recorder.fatal)
	}
}
