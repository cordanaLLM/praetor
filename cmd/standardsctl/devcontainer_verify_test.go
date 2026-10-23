// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/devcontainer"
)

func verifyDeadlineOptions(t *testing.T) devContainerOptions {
	t.Helper()
	manifest, output := cliBootstrapPaths(t)
	return devContainerOptions{configPath: manifest, outputPath: output, verify: true}
}

func TestDevContainerVerifyProductionBound(t *testing.T) {
	if devContainerVerifyBound <= 10*time.Second || devContainerVerifyBound != devContainerFreshnessBound {
		t.Fatalf("verification bound = %s; want freshness-sized budget above 10s", devContainerVerifyBound)
	}
}

func TestDevContainerVerifySlowWithinBound(t *testing.T) {
	// Scale the production budget and a 12-second verifier by 100. The old
	// 10-second budget expires; the verification-sized budget permits completion.
	bound := devContainerVerifyBound / 100
	verify := func(ctx context.Context, path string, dc *devcontainer.DevContainer) error {
		if path == "" || dc == nil {
			t.Fatal("verifier did not receive the declared configuration and output")
		}
		timer := time.NewTimer(120 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := runDevContainerVerify(verifyDeadlineOptions(t), bound, verify); err != nil {
		t.Fatalf("scaled 12s verification exceeded budget %s: %v", bound, err)
	}
}

func TestDevContainerVerifyDeadline(t *testing.T) {
	const bound = 100 * time.Millisecond
	for _, lateSuccess := range []bool{false, true} {
		verify := func(ctx context.Context, _ string, _ *devcontainer.DevContainer) error {
			<-ctx.Done()
			if lateSuccess {
				return nil
			}
			return ctx.Err()
		}
		err := runDevContainerVerify(verifyDeadlineOptions(t), bound, verify)
		assertDevContainerDeadline(t, err, bound, "verify")
	}
}

func TestDevContainerVerifyManifestDeadline(t *testing.T) {
	verify := func(context.Context, string, *devcontainer.DevContainer) error {
		t.Fatal("verifier ran after manifest deadline")
		return nil
	}
	err := runDevContainerVerify(verifyDeadlineOptions(t), 0, verify)
	assertDevContainerDeadline(t, err, 0, "manifest read")
}

func assertDevContainerDeadline(t *testing.T, err error, bound time.Duration, step string) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want wrapped deadline, got %v", err)
	}
	mustContain(t, err.Error(), "devcontainer verification", bound.String(), "during "+step)
	if strings.Contains(err.Error(), "does not match") {
		t.Fatalf("deadline reported as configuration drift: %v", err)
	}
}

func TestDevContainerVerifyMismatchPreserved(t *testing.T) {
	mismatch := errors.New("devcontainer does not match expected configuration")
	verify := func(context.Context, string, *devcontainer.DevContainer) error { return mismatch }
	err := runDevContainerVerify(verifyDeadlineOptions(t), devContainerVerifyBound, verify)
	if !errors.Is(err, mismatch) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("configuration mismatch changed classification: %v", err)
	}
	mustContain(t, err.Error(), "devcontainer verification failed", mismatch.Error())
	if strings.Contains(err.Error(), "bound") {
		t.Fatalf("configuration mismatch reported as deadline: %v", err)
	}
}
