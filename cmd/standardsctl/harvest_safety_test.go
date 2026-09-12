package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/harvester"
)

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
