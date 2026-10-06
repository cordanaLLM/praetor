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

// GoldenUpdateEnv names the environment variable that makes AssertGolden rewrite a golden
// file instead of comparing against it. A deliberate output change is then one reviewed diff
// of the golden file: PRAETOR_UPDATE_GOLDEN=1 go test ./<package> -run <Test>.
const GoldenUpdateEnv = "PRAETOR_UPDATE_GOLDEN"

// maxGoldenLines bounds the line walk that locates the first difference (HISS-02).
const maxGoldenLines = 1 << 16

// AssertGolden fails t unless got equals the golden file at path, a path relative to the
// test's package directory. Access is confined to that directory through os.Root, so an
// absolute path or one that escapes it fails instead of reading or writing elsewhere. The
// golden is read with CRLF folded to LF, so a checkout that converted it on Windows still
// compares equal (HISS-21): golden text is printable, and a test that pins a carriage return
// writes it escaped. With GoldenUpdateEnv set to 1 the file is rewritten with got instead.
func AssertGolden(t testing.TB, path, got string) {
	t.Helper()
	AssertGoldenIn(t, ".", path, got)
}

// AssertGoldenIn is AssertGolden for a golden file below dir, a directory relative to the
// test's package directory, such as "../.." for a committed file the test renders at the top
// of the repository. Access is confined to dir through os.Root the same way.
func AssertGoldenIn(t testing.TB, dir, path, got string) {
	t.Helper()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("testsupport: open directory %s for golden %s: %v", dir, path, err)
		return
	}
	defer closeGoldenRoot(t, root)
	if os.Getenv(GoldenUpdateEnv) == "1" {
		writeGolden(t, root, path, got)
		return
	}
	data, err := root.ReadFile(path)
	if err != nil {
		t.Fatalf("testsupport: read golden %s: %v (set %s=1 to create it)", path, err, GoldenUpdateEnv)
		return
	}
	want := strings.ReplaceAll(string(data), "\r\n", "\n")
	if got == want {
		return
	}
	line, gotLine, wantLine := firstGoldenDifference(got, want)
	t.Fatalf("testsupport: output differs from golden %s at line %d:\n got: %q\nwant: %q\n(set %s=1 to accept a deliberate change)",
		path, line, gotLine, wantLine, GoldenUpdateEnv)
}

func closeGoldenRoot(t testing.TB, root *os.Root) {
	t.Helper()
	if err := root.Close(); err != nil {
		t.Errorf("testsupport: close package directory: %v", err)
	}
}

func writeGolden(t testing.TB, root *os.Root, path, got string) {
	t.Helper()
	if err := root.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatalf("testsupport: create golden directory for %s: %v", path, err)
		return
	}
	if err := root.WriteFile(path, []byte(got), 0o600); err != nil {
		t.Fatalf("testsupport: write golden %s: %v", path, err)
	}
}

// firstGoldenDifference returns the 1-based number of the first line on which got and want
// differ, with both lines; a missing line reads as "<end of text>".
func firstGoldenDifference(got, want string) (int, string, string) {
	gotLines, wantLines := strings.Split(got, "\n"), strings.Split(want, "\n")
	for index := 0; index < maxGoldenLines; index++ {
		gotLine, wantLine := goldenLine(gotLines, index), goldenLine(wantLines, index)
		if gotLine != wantLine {
			return index + 1, gotLine, wantLine
		}
	}
	return maxGoldenLines, "<beyond the compared lines>", "<beyond the compared lines>"
}

func goldenLine(lines []string, index int) string {
	if index < len(lines) {
		return lines[index]
	}
	return "<end of text>"
}
