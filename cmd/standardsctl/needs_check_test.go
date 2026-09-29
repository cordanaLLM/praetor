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
	stale := strings.Replace(string(data), "    basis: not-configured\n", "", 1)
	if stale == string(data) {
		t.Fatalf("fixture manifest carries no readiness.basis to remove:\n%s", data)
	}
	if err := os.WriteFile(manifest, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = captureStdout(t, func() error { return dispatchCommand("needs", []string{"scan", "--check", "--path=" + repo}) })
	if err == nil || !strings.Contains(err.Error(), "is stale") || !strings.Contains(err.Error(), "+ generated:") || !strings.Contains(err.Error(), "basis: not-configured") {
		t.Fatalf("stale manifest: %v", err)
	}
}

// needs scan and needs scan --check name the repository after .standards.yaml, and say so
// when they fall back to the checkout's directory name, whose verdict changes with the
// directory (#606).
func TestNeedsScanNamesRepositoryFallback_3D(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "py-checkout")
	writeFixtureFile(t, repo, ".git/HEAD", "ref: refs/heads/main\n")
	writeFixtureFile(t, repo, "requirements.txt", "click\n")
	// Negative: nothing names the repository, so the scan and the check name the fallback.
	note := "Repository name: `py-checkout` is the repository directory's name"
	out, err := runNeedsCapture(t, "scan", "--write", "--path="+repo)
	if err != nil {
		t.Fatalf("needs scan --write: %v\n%s", err, out)
	}
	mustContain(t, out, "=== Framework Needs Scan: py-checkout ===\n"+note)
	out, err = runNeedsCapture(t, "scan", "--check", "--path="+repo)
	if err != nil || !strings.Contains(out, note) || !strings.Contains(out, "matches a fresh scan") {
		t.Fatalf("needs scan --check of a fallback row: %v\n%s", err, out)
	}
	// Positive: repository.name names it, and no fallback line is printed.
	writeFixtureFile(t, repo, ".standards.yaml", "repository:\n  name: nucleus\n")
	out, err = runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil || !strings.Contains(out, "=== Framework Needs Scan: nucleus ===\n") || strings.Contains(out, "Repository name:") {
		t.Fatalf("needs scan of a named repository: %v\n%s", err, out)
	}
	// Boundary: the manifest written under the directory name is stale against the name
	// .standards.yaml now gives.
	_, err = runNeedsCapture(t, "scan", "--check", "--path="+repo)
	if err == nil || !strings.Contains(err.Error(), "+ generated:2: repository: nucleus") {
		t.Fatalf("check after naming the repository: %v", err)
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
