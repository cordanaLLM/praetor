// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"testing"
)

// hookToolchain is the hook resolver whose MAKE_CANDIDATES GNUMakeCandidates mirrors.
var hookToolchain = filepath.Join("..", "..", ".config", "lefthook", "scripts", "toolchain.py")

var (
	makeCandidatesLine = regexp.MustCompile(`(?m)^MAKE_CANDIDATES = (.*)$`)
	quotedName         = regexp.MustCompile(`"([^"]+)"`)
)

// hookMakeCandidates returns the names the MAKE_CANDIDATES line of a toolchain.py text lists, in
// order, and an error when the text holds no such line or the line names no program.
func hookMakeCandidates(text string) ([]string, error) {
	line := makeCandidatesLine.FindStringSubmatch(text)
	if line == nil {
		return nil, errors.New("no MAKE_CANDIDATES line")
	}
	var names []string
	for _, match := range quotedName.FindAllStringSubmatch(line[1], len(line[1])) {
		names = append(names, match[1])
	}
	if len(names) == 0 {
		return nil, errors.New("MAKE_CANDIDATES names no program")
	}
	return names, nil
}

// The tests and the hooks resolve GNU Make from one list: a candidate added to the hooks and not
// here would let the hooks build the CLI with a make the replay tests never find.
func TestGNUMakeCandidatesMatchHookToolchain(t *testing.T) {
	data, err := os.ReadFile(hookToolchain)
	if err != nil {
		t.Fatal(err)
	}
	hooks, err := hookMakeCandidates(string(data))
	if err != nil {
		t.Fatalf("%s: %v", hookToolchain, err)
	}
	if !slices.Equal(hooks, GNUMakeCandidates) {
		t.Fatalf("GNUMakeCandidates = %q, %s MAKE_CANDIDATES = %q", GNUMakeCandidates, hookToolchain, hooks)
	}
}

// Negative and boundary: a text without the line, or whose line names nothing, is an error rather
// than an empty list that would compare equal to nothing; a single name is read as one candidate.
func TestHookMakeCandidatesRefusesWhatNamesNoProgram(t *testing.T) {
	for name, text := range map[string]string{
		"absent":   "PYTHON_CANDIDATES = ((\"python3\",),)\n",
		"empty":    "MAKE_CANDIDATES = ()\n",
		"indented": "  MAKE_CANDIDATES = ((\"make\",),)\n",
	} {
		if got, err := hookMakeCandidates(text); err == nil {
			t.Errorf("%s: read %q", name, got)
		}
	}
	got, err := hookMakeCandidates("MAKE_CANDIDATES = ((\"make\",),)\n")
	if err != nil || !slices.Equal(got, []string{"make"}) {
		t.Fatalf("one candidate read as %q, %v", got, err)
	}
}

// makeVersionStub prints what a make prints for --version.
const makeVersionStub = `package main

import "fmt"

func main() { fmt.Println(%q) }
`

// buildMakeStub builds a program called name into dir that reports version for --version.
func buildMakeStub(t *testing.T, dir, name, version string) string {
	t.Helper()
	return BuildExecutable(t, dir, name, fmt.Sprintf(makeVersionStub, version))
}

// gnuMakeSkips reports whether GNUMake skips under the current PATH, and the path it returned.
func gnuMakeSkips(t *testing.T) (bool, string) {
	t.Helper()
	skipped, path := false, ""
	t.Run("resolve", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		path = GNUMake(t)
	})
	return skipped, path
}

// Positive and boundary: the first candidate that states GNU Make 4.x wins, so a make that states
// 3.81 is passed over for a gmake that states 4.4.1 later in the list.
func TestGNUMake_Positive_FirstStatedGNUMake4Wins(t *testing.T) {
	dir := t.TempDir()
	buildMakeStub(t, dir, "make", "GNU Make 3.81")
	gmake := buildMakeStub(t, dir, "gmake", "GNU Make 4.4.1")
	t.Setenv("PATH", dir)
	if skipped, path := gnuMakeSkips(t); skipped || path != gmake {
		t.Fatalf("GNUMake skipped = %v, returned %q; want %q", skipped, path, gmake)
	}
}

// Negative: an empty PATH and a PATH holding only GNU Make 3.81 skip the test.
func TestGNUMake_Negative_SkipsWithoutGNUMake4(t *testing.T) {
	old := t.TempDir()
	buildMakeStub(t, old, "make", "GNU Make 3.81")
	for name, dir := range map[string]string{"empty": t.TempDir(), "make-3.81": old} {
		t.Setenv("PATH", dir)
		if skipped, path := gnuMakeSkips(t); !skipped {
			t.Errorf("%s: GNUMake returned %q", name, path)
		}
	}
}

// requireShellSkips reports whether RequireGNUMakeShell skips under the current PATH.
func requireShellSkips(t *testing.T, programs ...string) bool {
	t.Helper()
	skipped := false
	t.Run("require", func(t *testing.T) {
		defer func() { skipped = t.Skipped() }()
		RequireGNUMakeShell(t, programs...)
	})
	return skipped
}

// Positive, negative and boundary: with sh and cat on PATH nothing skips; with an empty PATH only
// Windows skips, where GNU Make would fall back to cmd.exe; with sh alone Windows still skips a row
// that needs cat.
func TestRequireGNUMakeShell_SkipsOnlyWhereCmdWouldRun(t *testing.T) {
	tools := t.TempDir()
	buildMakeStub(t, tools, "sh", "sh")
	buildMakeStub(t, tools, "cat", "cat")
	shOnly := t.TempDir()
	buildMakeStub(t, shOnly, "sh", "sh")
	windows := runtime.GOOS == "windows"
	for name, tc := range map[string]struct {
		path string
		want bool
	}{
		"sh-and-cat": {tools, false},
		"empty-path": {t.TempDir(), windows},
		"sh-only":    {shOnly, windows},
	} {
		t.Setenv("PATH", tc.path)
		if got := requireShellSkips(t, "cat"); got != tc.want {
			t.Errorf("%s: skipped = %v, want %v on %s", name, got, tc.want, runtime.GOOS)
		}
	}
}
