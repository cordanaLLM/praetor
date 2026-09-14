package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/bump"
	"github.com/cordanaLLM/praetor/internal/util"
)

func TestBumpCanaryCLIDryRunIsOnlyPlanned(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent")
	text, err := captureStdout(t, func() error {
		return runBumpCanary(t.Context(), []string{"example.org/dependency", "--target=v1.2.3", "--dry-run", "--path", dir})
	})
	if err != nil || !strings.Contains(text, "[PLANNED]") || !strings.Contains(text, "Canary Certified: false") || strings.Contains(text, "[PASS]") {
		t.Fatalf("dry-run misrepresented: %q, %v", text, err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("dry-run created files: %v", err)
	}
}

func TestBumpApplyCLIRejectsMissingPatchBeforeUpdate(t *testing.T) {
	dir := t.TempDir()
	text, err := captureStdout(t, func() error {
		return runBumpApply(t.Context(), []string{"example.org/dependency", "--version=v1.2.3", "--path", dir, "--patch", filepath.Join(dir, "missing.patch")})
	})
	if !errors.Is(err, os.ErrNotExist) || !errors.Is(err, bump.ErrInvalidPatch) || strings.Contains(text, "[APPLIED]") || strings.Contains(text, "[PATCHED]") {
		t.Fatalf("missing patch skipped or claimed applied: %q, %v", text, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("missing patch caused side effects: %v, %v", entries, err)
	}
}

func TestBumpTrainCLIDryRunDoesNotClaimPatchOrCertification(t *testing.T) {
	useOfflineCanaryScanner(t)
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", `{"dependencies":{"fixture-dep":"2.0.0-rc.1"}}`)
	text, err := captureStdout(t, func() error {
		return runBumpTrain(t.Context(), []string{"--dry-run", "--path", dir})
	})
	if err != nil || !strings.Contains(text, "[planned]") || strings.Contains(text, "[CERTIFIED]") || strings.Contains(text, "patch staged") {
		t.Fatalf("train fabricated evidence: %q, %v", text, err)
	}
}

func TestBumpTrainCLIPropagatesFailedCanary(t *testing.T) {
	useOfflineCanaryScanner(t)
	dir := t.TempDir()
	writeFixtureFile(t, dir, "package.json", `{"dependencies":{"fixture-dep":"2.0.0-rc.1"}}`)
	writeFixtureFile(t, dir, ".gitignore", ".standards/\n.workingdir/\n")
	for _, args := range [][]string{{"init", "-q"}, {"config", "user.name", "Canary Fixture"}, {"config", "user.email", "fixture@example.test"}, {"add", "."}, {"commit", "-q", "-s", "-m", "test: initialize canary fixture"}} {
		if _, err := util.RunGit(t.Context(), dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	text, err := captureStdout(t, func() error {
		return runBumpTrain(t.Context(), []string{"--path", dir})
	})
	if !errors.Is(err, bump.ErrCanaryFailed) || !strings.Contains(text, "[ERROR]") || strings.Contains(text, "[CERTIFIED]") {
		t.Fatalf("failed train did not propagate failure: %q, %v", text, err)
	}
}

func useOfflineCanaryScanner(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "pnpm"), []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}
