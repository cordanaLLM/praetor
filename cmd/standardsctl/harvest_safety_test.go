package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/harvester"
)

func TestOnboardLockLineStatesTheOutcome(t *testing.T) {
	// Positive: a verified lock says its content digests were hashed.
	if line := onboardLockLine(config.LockStatusVerified); !strings.Contains(line, "[OK]") {
		t.Fatalf("verified line: %q", line)
	}
	// Negative: an unverifiable lock never reads as verified.
	line := onboardLockLine(config.LockStatusUnverifiable)
	if !strings.Contains(line, "[UNVERIFIED]") || !strings.Contains(line, config.ErrLockUnverifiable.Error()) || strings.Contains(line, "[OK]") {
		t.Fatalf("unverifiable line: %q", line)
	}
	// Boundary: a dry run carries no status and prints nothing.
	if line := onboardLockLine(""); line != "" {
		t.Fatalf("dry run printed a lock outcome: %q", line)
	}
}

func TestHarvestOnboardReportsPartialFailure(t *testing.T) {
	repo := t.TempDir()
	err := runHarvestOnboard(context.Background(), []string{"--repo=" + repo, "--dry-run=false"})
	if !errors.Is(err, harvester.ErrOnboardingIncomplete) {
		t.Fatalf("CLI reported success despite missing dependency pins: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, "AGENTS.md")); err != nil {
		t.Fatalf("expected useful partial scaffold: %v", err)
	}
	if _, err := os.Stat(filepath.Join(repo, ".standards.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("CLI fabricated a lock: %v", err)
	}
}
