// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package util

import (
	"context"
	"errors"
	"os/exec"
	"testing"
	"time"
)

func TestGitAnsweredUnset_3D(t *testing.T) {
	// Positive: a process exiting with status 1 under a live context reports true.
	liveCtx := context.Background()
	cmd1 := exec.Command("sh", "-c", "exit 1")
	err1 := cmd1.Run()
	if err1 == nil {
		t.Fatal("expected exit status 1")
	}
	if !GitAnsweredUnset(liveCtx, err1) {
		t.Errorf("GitAnsweredUnset with exit status 1 under live context must be true")
	}

	// Boundary: nil context with exit status 1 reports true.
	var nilCtx context.Context
	if !GitAnsweredUnset(nilCtx, err1) {
		t.Errorf("GitAnsweredUnset with exit status 1 under nil context must be true")
	}

	// Boundary: cancelled context with exit status 1 must report false (a context that ended
	// during the call is never an answer).
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if GitAnsweredUnset(cancelledCtx, err1) {
		t.Errorf("GitAnsweredUnset under cancelled context must be false")
	}

	// Boundary: timed-out context with exit status 1 must report false.
	timedOutCtx, cancelTimeout := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancelTimeout()
	if GitAnsweredUnset(timedOutCtx, err1) {
		t.Errorf("GitAnsweredUnset under deadline exceeded context must be false")
	}

	// Negative: nil error reports false.
	if GitAnsweredUnset(liveCtx, nil) {
		t.Errorf("GitAnsweredUnset(nil) must be false")
	}

	// Negative: non-ExitError reports false.
	if GitAnsweredUnset(liveCtx, errors.New("arbitrary error")) {
		t.Errorf("GitAnsweredUnset with arbitrary error must be false")
	}

	// Negative: process exiting with status 128 reports false.
	cmd128 := exec.Command("sh", "-c", "exit 128")
	err128 := cmd128.Run()
	if err128 == nil {
		t.Fatal("expected exit status 128")
	}
	if GitAnsweredUnset(liveCtx, err128) {
		t.Errorf("GitAnsweredUnset with exit status 128 must be false")
	}

	// Negative: process exiting with status 2 reports false.
	cmd2 := exec.Command("sh", "-c", "exit 2")
	err2 := cmd2.Run()
	if err2 == nil {
		t.Fatal("expected exit status 2")
	}
	if GitAnsweredUnset(liveCtx, err2) {
		t.Errorf("GitAnsweredUnset with exit status 2 must be false")
	}
}
