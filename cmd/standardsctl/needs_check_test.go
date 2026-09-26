package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/needs"
)

// Positive: a manifest freshly written by scan --write passes scan --check, and this
// repository's own committed .needs.yaml is current.
func TestNeedsScanCheckPassesAfterWrite(t *testing.T) {
	repo := newNeedsRepo(t)
	if _, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--write", "--path=" + repo}) }); err != nil {
		t.Fatalf("needs scan --write: %v", err)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--check", "--path=" + repo}) })
	if err != nil || !strings.Contains(out, "matches a fresh scan") {
		t.Fatalf("needs scan --check after write: %v\n%s", err, out)
	}
	engine := filepath.Join("..", "..")
	if _, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--check", "--path=" + engine}) }); err != nil {
		t.Fatalf("the committed .needs.yaml is stale; run 'praetorctl needs scan --write': %v", err)
	}
}

// Negative: a committed manifest the generator no longer writes fails with the drift, and
// a repository with no manifest fails with the missing-manifest error.
func TestNeedsScanCheckRejectsStaleOrMissingManifest(t *testing.T) {
	repo := newNeedsRepo(t)
	_, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--check", "--path=" + repo}) })
	if !errors.Is(err, needs.ErrNeedsManifestMissing) {
		t.Fatalf("missing manifest: %v", err)
	}
	if _, err := captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--write", "--path=" + repo}) }); err != nil {
		t.Fatalf("needs scan --write: %v", err)
	}
	manifest := filepath.Join(repo, needs.NeedsManifestName)
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	stale := strings.Replace(string(data), "    basis: catalog-declared\n", "", 1)
	if stale == string(data) {
		t.Fatalf("fixture manifest carries no readiness.basis to remove:\n%s", data)
	}
	if err := os.WriteFile(manifest, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--check", "--path=" + repo}) })
	if err == nil || !strings.Contains(err.Error(), "is stale") || !strings.Contains(err.Error(), "+ generated:") || !strings.Contains(err.Error(), "basis: catalog-declared") {
		t.Fatalf("stale manifest: %v", err)
	}
}

// Boundary: --write and --check together are refused before anything is scanned or
// written.
func TestNeedsScanCheckExcludesWrite(t *testing.T) {
	repo := newNeedsRepo(t)
	_, err := captureStdout(t, func() error {
		return dispatchCommand("needs", []string{"scan", "--write", "--check", "--path=" + repo})
	})
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("--write --check: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, needs.NeedsManifestName)); !os.IsNotExist(statErr) {
		t.Fatalf("a manifest was written despite the refusal: %v", statErr)
	}
}
