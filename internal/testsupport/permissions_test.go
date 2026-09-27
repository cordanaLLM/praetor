// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/cordanaLLM/praetor/internal/util"
)

// skipRecorder observes a Skip inside a probe instead of skipping this test. SkipNow ends the
// goroutine, as testing.T does.
type skipRecorder struct {
	testing.TB
	mu      sync.Mutex
	skipped bool
}

func (r *skipRecorder) Skip(args ...any) {
	r.mu.Lock()
	r.skipped = true
	r.mu.Unlock()
	runtime.Goexit()
}

func probeSkips(t *testing.T, probe func(testing.TB)) bool {
	t.Helper()
	recorder := &skipRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		probe(recorder)
	}()
	<-done
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.skipped
}

// enforced measures, independently of the probes, whether mode 0 denies access on this host.
func enforced(t *testing.T, directory bool) bool {
	t.Helper()
	path := filepath.Join(t.TempDir(), "independent")
	var err error
	if directory {
		err = os.Mkdir(path, 0o700)
	} else {
		err = os.WriteFile(path, []byte("x"), 0o600)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(path, 0o700); err != nil {
			t.Error(err)
		}
	}()
	if directory {
		_, err = os.ReadDir(path)
	} else {
		// #nosec G304 -- path is a file this test just created in its own temporary directory.
		_, err = os.ReadFile(path)
	}
	return err != nil
}

// TestSkipIfModeUnenforced_Positive_AgreesWithTheHost: each probe skips exactly when the
// denial it stands for does not happen here, so neither probe can hide a case the host could run.
func TestSkipIfModeUnenforced_Positive_AgreesWithTheHost(t *testing.T) {
	if got, want := probeSkips(t, SkipIfFileModeUnenforced), !enforced(t, false); got != want {
		t.Fatalf("file probe skipped=%v, want %v", got, want)
	}
	if got, want := probeSkips(t, SkipIfDirectoryModeUnenforced), !enforced(t, true); got != want {
		t.Fatalf("directory probe skipped=%v, want %v", got, want)
	}
}

// TestSkipIfModeUnenforced_Negative_RootAndWindowsAlwaysSkip pins the two causes the probes
// replace an inference about: neither may run a permission case.
func TestSkipIfModeUnenforced_Negative_RootAndWindowsAlwaysSkip(t *testing.T) {
	if runtime.GOOS != "windows" && os.Geteuid() != 0 {
		t.Skip("this host enforces modes; the non-skipping side is covered by the positive case")
	}
	if !probeSkips(t, SkipIfFileModeUnenforced) || !probeSkips(t, SkipIfDirectoryModeUnenforced) {
		t.Fatal("a host that ignores modes ran a permission case")
	}
}

// TestSkipIfModeUnenforced_Boundary_ProbesLeaveNothingDenied checks that the probes restore
// what they changed, so the testing package can remove their temporary directories.
func TestSkipIfModeUnenforced_Boundary_ProbesLeaveNothingDenied(t *testing.T) {
	for _, probe := range []func(testing.TB){SkipIfFileModeUnenforced, SkipIfDirectoryModeUnenforced} {
		sub := &skipRecorder{TB: t}
		done := make(chan struct{})
		go func() {
			defer close(done)
			probe(sub)
		}()
		<-done
	}
	if t.Failed() {
		t.Fatal("a probe failed to restore the mode it removed")
	}
}

// failureRecorder observes the failures RequireCreatedMode reports instead of failing this test.
// Fatalf ends the goroutine, as testing.T does.
type failureRecorder struct {
	testing.TB
	mu       sync.Mutex
	failures []string
}

func (r *failureRecorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	r.failures = append(r.failures, fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func (r *failureRecorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	runtime.Goexit()
}

func createdModeFailures(t *testing.T, path string, perm os.FileMode) []string {
	t.Helper()
	recorder := &failureRecorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RequireCreatedMode(recorder, path, perm)
	}()
	<-done
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.failures
}

func requireModeProtection(t *testing.T) {
	t.Helper()
	if !util.ModeIsProtection() {
		t.Skip("permission bits are not the file protection on this platform")
	}
}

// TestRequireCreatedMode_Positive_AcceptsWhatTheUmaskProduces: a file and a directory created
// with a mode carry that mode filtered by the umask, which is exactly what the helper expects.
func TestRequireCreatedMode_Positive_AcceptsWhatTheUmaskProduces(t *testing.T) {
	requireModeProtection(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "tracked")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for path, perm := range map[string]os.FileMode{file: 0o644, sub: 0o755} {
		if failures := createdModeFailures(t, path, perm); len(failures) != 0 {
			t.Errorf("%s: unexpected failures %q", path, failures)
		}
	}
}

// TestRequireCreatedMode_Negative_ReportsAWrongModeAndAMissingPath: an owner-only file checked
// against the owner read-write mode, and a path that does not exist, both fail.
func TestRequireCreatedMode_Negative_ReportsAWrongModeAndAMissingPath(t *testing.T) {
	requireModeProtection(t)
	file := filepath.Join(t.TempDir(), "narrow")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	if failures := createdModeFailures(t, file, 0o600); len(failures) != 1 {
		t.Errorf("a 0400 file passed as 0600: %q", failures)
	}
	if failures := createdModeFailures(t, filepath.Join(t.TempDir(), "absent"), 0o600); len(failures) != 1 {
		t.Errorf("a missing path passed: %q", failures)
	}
}

// TestRequireCreatedMode_Boundary_ZeroModeAndTheMaskAgree: a mode of zero expects zero whatever
// the umask, and the measured mask never grants a bit the widest request did not ask for.
func TestRequireCreatedMode_Boundary_ZeroModeAndTheMaskAgree(t *testing.T) {
	requireModeProtection(t)
	file := filepath.Join(t.TempDir(), "closed")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chmod(file, 0o600); err != nil {
			t.Error(err)
		}
	}()
	if failures := createdModeFailures(t, file, 0); len(failures) != 0 {
		t.Errorf("mode 0 rejected: %q", failures)
	}
	if mask := creationMask(t); mask&^0o777 != 0 {
		t.Errorf("creation mask %#o carries non-permission bits", mask)
	}
}
