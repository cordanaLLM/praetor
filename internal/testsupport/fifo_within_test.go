// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package testsupport

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// recordingTB records a fatal failure instead of stopping the goroutine, so a helper's
// failure path can itself be asserted.
type recordingTB struct {
	testing.TB
	fatal string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) { r.fatal = fmt.Sprintf(format, args...) }

func (r *recordingTB) Skipf(format string, args ...any) {
	r.fatal = "skip: " + fmt.Sprintf(format, args...)
}

// Positive: RunWithin returns the error fn returned, and MakeFIFO leaves a named pipe.
func TestRunWithinAndMakeFIFO_Positive(t *testing.T) {
	want := errors.New("fixture")
	if got := RunWithin(t, time.Minute, func() error { return want }); !errors.Is(got, want) {
		t.Fatalf("RunWithin returned %v, want %v", got, want)
	}
	path := filepath.Join(t.TempDir(), "pipe")
	MakeFIFO(t, path)
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("MakeFIFO left mode %v, want a named pipe", info.Mode())
	}
}

// Negative: a call that never returns fails the test at the limit, and a FIFO cannot be
// planted over an existing file.
func TestRunWithinAndMakeFIFO_Negative(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	recorder := &recordingTB{TB: t}
	if err := RunWithin(recorder, 10*time.Millisecond, func() error { <-release; return nil }); err != nil {
		t.Fatalf("a timed-out call returns no error of its own, got %v", err)
	}
	if recorder.fatal == "" {
		t.Fatal("a call that never returned did not fail the test")
	}

	path := filepath.Join(t.TempDir(), "taken")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	recorder = &recordingTB{TB: t}
	MakeFIFO(recorder, path)
	if recorder.fatal == "" {
		t.Fatal("MakeFIFO over an existing file did not fail")
	}
}

// Boundary: a call that returns nil well inside the limit yields nil and no failure.
func TestRunWithin_Boundary_NilWithinLimit(t *testing.T) {
	recorder := &recordingTB{TB: t}
	if err := RunWithin(recorder, time.Minute, func() error { return nil }); err != nil || recorder.fatal != "" {
		t.Fatalf("RunWithin = %v, failure %q; want nil and no failure", err, recorder.fatal)
	}
}
