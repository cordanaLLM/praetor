// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

const echoArgs = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() {
	fmt.Print(strings.Join(os.Args[1:], "|"))
	os.Exit(3)
}
`

// TestBuildExecutable_Positive_ResolvableByBareName is the property every caller relies on: after
// putting dir on PATH, the tool is found and run by its bare name, as production code runs it.
func TestBuildExecutable_Positive_ResolvableByBareName(t *testing.T) {
	dir := t.TempDir()
	path := BuildExecutable(t, dir, "fake-tool", echoArgs)
	if filepath.Dir(path) != dir || filepath.Base(path) != ExecutableName("fake-tool") {
		t.Fatalf("shim built at an unexpected path: %s", path)
	}
	t.Setenv("PATH", dir)
	resolved, err := exec.LookPath("fake-tool")
	if err != nil {
		t.Fatalf("shim not resolvable by bare name: %v", err)
	}
	output, err := exec.CommandContext(t.Context(), resolved, "a b", "c").Output()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 3 || string(output) != "a b|c" {
		t.Fatalf("shim did not run its program: output=%q err=%v", output, err)
	}
}

// fatalRecorder stands in for the test so a Fatalf inside Build is observed rather than
// failing this test. Fatalf ends the goroutine, as testing.T does.
type fatalRecorder struct {
	testing.TB
	mu      sync.Mutex
	message string
}

func (r *fatalRecorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.message = fmt.Sprintf(format, args...)
	r.mu.Unlock()
	runtime.Goexit()
}

func runRecorded(t *testing.T, build func(testing.TB)) string {
	t.Helper()
	recorder := &fatalRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		build(recorder)
	}()
	<-done
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.message
}

func TestBuildExecutable_Negative_InvalidSourceAndMissingGo(t *testing.T) {
	dir := t.TempDir()
	message := runRecorded(t, func(tb testing.TB) { BuildExecutable(tb, dir, "broken", "package main\n\nfunc main() {") })
	if !strings.Contains(message, "testsupport: build broken") {
		t.Fatalf("a compile failure was not reported: %q", message)
	}
	if _, err := os.Stat(filepath.Join(dir, ExecutableName("broken"))); err == nil {
		t.Fatal("a failed build left an executable behind")
	}
	t.Setenv("PATH", t.TempDir())
	message = runRecorded(t, func(tb testing.TB) { BuildExecutable(tb, dir, "orphan", echoArgs) })
	if !strings.Contains(message, "needs the go command on PATH") {
		t.Fatalf("a missing go command was not reported: %q", message)
	}
}

func TestExecutableName_Boundary_PlatformSuffix(t *testing.T) {
	want := "git"
	if runtime.GOOS == "windows" {
		want = "git.exe"
	}
	if got := ExecutableName("git"); got != want {
		t.Fatalf("ExecutableName(git) = %q, want %q", got, want)
	}
	if got := ExecutableName(""); got != strings.TrimPrefix(want, "git") {
		t.Fatalf("Name of the empty name = %q", got)
	}
}
