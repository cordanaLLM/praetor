// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
	"github.com/cordanaLLM/praetor/internal/util"
)

// The tools a hermetic fixture puts on PATH (hermeticPath) are built from Go source, so each
// test runs the same stand-in on every platform (#166). They were "#!/bin/sh" scripts: Windows
// does not start a script by its shebang and exec.LookPath does not find an extensionless file
// there, so a failing lefthook stub was simply missing, and a stub meant to install a hook
// never ran. Each source is built once per test binary and linked into the test's own stub
// directory, where it keeps what it records next to itself.

// lefthookFailsSource exits 1 on every call, as newTestRepo's lefthook did.
const lefthookFailsSource = `package main

import "os"

func main() {
	os.Exit(1)
}
`

// lefthookInstallRan is the file lefthookRunsSource writes next to itself on `install`.
const lefthookInstallRan = "lefthook-install-ran"

// lefthookRunsSource answers `version` and, on `install`, records that it ran and writes a
// pre-commit hook carrying lefthookStubMarker where git says hooks live, as real lefthook does.
var lefthookRunsSource = `package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		os.Exit(1)
	}
	switch os.Args[1] {
	case "version":
		os.Stdout.WriteString("2.1.14\n")
	case "install":
		self, err := os.Executable()
		if err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(filepath.Dir(self), "` + lefthookInstallRan + `"), nil, 0o600); err != nil {
			os.Exit(1)
		}
		out, err := exec.Command("git", "rev-parse", "--git-path", "hooks").Output()
		if err != nil {
			os.Exit(1)
		}
		hooks := strings.TrimSpace(string(out))
		if err := os.MkdirAll(hooks, 0o755); err != nil {
			os.Exit(1)
		}
		if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\n` + lefthookStubMarker + `\n"), 0o755); err != nil {
			os.Exit(1)
		}
	default:
		os.Exit(1)
	}
}
`

// callFailOn is the file next to a callRecorderSource binary naming the first argument it fails on.
const callFailOn = "fail-on"

// callRecorderSource appends its arguments, joined by blanks, as one line to "calls" in its
// working directory, as printf '%s\n' "$*" >> calls did, and exits 1 when its first argument
// is the word in callFailOn next to it. Without that file it exits 2, so a fixture that
// forgot it cannot pass.
const callRecorderSource = `package main

import (
	"os"
	"path/filepath"
	"strings"
)

func main() {
	calls, err := os.OpenFile("calls", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		os.Exit(2)
	}
	_, err = calls.WriteString(strings.Join(os.Args[1:], " ") + "\n")
	if closeErr := calls.Close(); err != nil || closeErr != nil {
		os.Exit(2)
	}
	self, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	failOn, err := os.ReadFile(filepath.Join(filepath.Dir(self), "` + callFailOn + `"))
	if err != nil {
		os.Exit(2)
	}
	if len(os.Args) > 1 && os.Args[1] == strings.TrimSpace(string(failOn)) {
		os.Exit(1)
	}
}
`

// stubCache holds each stand-in built in this test binary, by source, and the PATH the binary
// started with, under which it builds them.
var stubCache = struct {
	sync.Mutex
	dir   string
	path  string
	built map[string]string
}{built: map[string]string{}}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "praetor-adopt-stubs-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stub cache: %v\n", err)
		os.Exit(1)
	}
	stubCache.dir = dir
	stubCache.path = os.Getenv("PATH")
	code := m.Run()
	if err := os.RemoveAll(dir); err != nil {
		fmt.Fprintf(os.Stderr, "stub cache: %v\n", err)
	}
	os.Exit(code)
}

// installStub puts the stand-in built from source into dir as the executable called name and
// returns its path. It may run after hermeticPath narrowed PATH (useLefthook does).
func installStub(t *testing.T, dir, name, source string) string {
	t.Helper()
	built := builtStub(t, source)
	target := filepath.Join(dir, testsupport.ExecutableName(name))
	if err := os.Link(built, target); err == nil {
		return target
	}
	if err := copyExecutable(built, target); err != nil {
		t.Fatalf("install stub %s: %v", name, err)
	}
	return target
}

// builtStub returns the cached build of source, building it on first use.
func builtStub(t *testing.T, source string) string {
	t.Helper()
	stubCache.Lock()
	defer stubCache.Unlock()
	if path, ok := stubCache.built[source]; ok {
		return path
	}
	dir, err := os.MkdirTemp(stubCache.dir, "stub-")
	if err != nil {
		t.Fatalf("stub cache: %v", err)
	}
	// The first build of a source may come after hermeticPath narrowed PATH, which holds no go
	// command. BuildExecutable finds go on PATH, so the build runs under the binary's starting
	// PATH, restored at once. Tests that change PATH do not run in parallel (t.Setenv), and the
	// lock holds out the other builds.
	narrowed := os.Getenv("PATH")
	if err := os.Setenv("PATH", stubCache.path); err != nil {
		t.Fatalf("stub cache: %v", err)
	}
	defer func() {
		if err := os.Setenv("PATH", narrowed); err != nil {
			t.Errorf("stub cache: restore PATH: %v", err)
		}
	}()
	path := testsupport.BuildExecutable(t, dir, "stub", source)
	stubCache.built[source] = path
	return path
}

func copyExecutable(from, to string) (err error) {
	source, err := os.Open(from)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, source.Close()) }()
	target, err := os.OpenFile(to, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o700)
	if err != nil {
		return err
	}
	_, err = io.Copy(target, source)
	return errors.Join(err, target.Close())
}

// TestHermeticPathRunsTheBuiltStubs (#166): under hermeticPath, lefthook is the stand-in the
// fixture installed, on every platform. A fixture whose lefthook is missing reaches adoption's
// fallback the same way a failing one does, so without this the tests that mean "lefthook
// fails" or "lefthook installs" could pass on a platform where the stub never starts.
func TestHermeticPathRunsTheBuiltStubs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("hermeticPath keeps git's directory, and no git is on PATH: %v", err)
	}
	// A skip in hermeticPath would skip every fixture that uses it, this test's subtests
	// included, and a skip is no failure: the narrowing itself must run here.
	narrowed := false
	t.Run("hermeticPath runs", func(t *testing.T) {
		hermeticPath(t, t.TempDir())
		narrowed = true
	})
	if !narrowed {
		t.Fatal("hermeticPath did not run on this platform")
	}
	t.Run("failing stub is found and fails", func(t *testing.T) {
		newTestRepo(t, "stub-fails")
		path, err := exec.LookPath("lefthook")
		if err != nil {
			t.Fatalf("lefthook is not on the hermetic PATH: %v", err)
		}
		_, err = util.RunCommand(t.Context(), t.TempDir(), "lefthook", "version")
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 {
			t.Fatalf("lefthook %s: want exit 1 from the stub, got %v", path, err)
		}
	})
	t.Run("installing stub runs and records install", func(t *testing.T) {
		repo := newTestRepo(t, "stub-runs")
		useLefthook(t, lefthookRuns)
		if !lefthookRunnable(t.Context()) {
			t.Fatal("lefthook version does not run")
		}
		path, err := exec.LookPath("lefthook")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := util.RunCommand(t.Context(), repo, "lefthook", "install"); err != nil {
			t.Fatalf("lefthook install: %v", err)
		}
		if !fileExists(filepath.Join(filepath.Dir(path), lefthookInstallRan)) || installedHook(t, repo) != "lefthook" {
			t.Fatalf("install left no record or no lefthook hook (hook %q)", installedHook(t, repo))
		}
	})
	t.Run("missing stub is not found", func(t *testing.T) {
		newTestRepo(t, "stub-missing")
		useLefthook(t, lefthookMissing)
		if path, err := exec.LookPath("lefthook"); err == nil {
			t.Fatalf("a lefthook outside the fixture is reachable: %s", path)
		}
		if lefthookRunnable(t.Context()) {
			t.Fatal("lefthook runs although none is on PATH")
		}
	})
	t.Run("recorder records and fails on its word", func(t *testing.T) {
		stubs := t.TempDir()
		mustWrite(t, filepath.Join(stubs, callFailOn), "build\n")
		recorder := installStub(t, stubs, "go", callRecorderSource)
		work := t.TempDir()
		if _, err := util.RunCommand(t.Context(), work, recorder, "test", "-v", "./..."); err != nil {
			t.Fatalf("recorder failed on a word it does not fail on: %v", err)
		}
		if _, err := util.RunCommand(t.Context(), work, recorder, "build"); err == nil {
			t.Fatal("recorder passed on its failure word")
		}
		if got := mustRead(t, filepath.Join(work, "calls")); got != "test -v ./...\nbuild\n" {
			t.Fatalf("calls = %q", got)
		}
		if err := os.Remove(filepath.Join(stubs, callFailOn)); err != nil {
			t.Fatal(err)
		}
		if _, err := util.RunCommand(t.Context(), work, recorder, "test"); err == nil {
			t.Fatal("recorder without its failure word passed")
		}
	})
}
