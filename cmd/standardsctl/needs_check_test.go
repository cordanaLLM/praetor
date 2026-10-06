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
	writeFixtureFile(t, repo, ".standards.yaml", "repository:\n  name: platform\n")
	out, err = runNeedsCapture(t, "scan", "--path="+repo)
	if err != nil || !strings.Contains(out, "=== Framework Needs Scan: platform ===\n") || strings.Contains(out, "Repository name:") {
		t.Fatalf("needs scan of a named repository: %v\n%s", err, out)
	}
	// Boundary: the manifest written under the directory name is stale against the name
	// .standards.yaml now gives.
	_, err = runNeedsCapture(t, "scan", "--check", "--path="+repo)
	if err == nil || !strings.Contains(err.Error(), "+ generated:2: repository: platform") {
		t.Fatalf("check after naming the repository: %v", err)
	}
}

// Boundary: --write and --check together are refused before anything is scanned or
// written.
// needs scan --check and --write refuse a declared non-goal the repository still uses,
// naming the non-goal and the package, and keep one it does not use (#34).
func TestNeedsScanDeclaredNonGoals_3D(t *testing.T) {
	scan := func(repo string, flags ...string) (string, error) {
		return captureStdout(t, func() error { return dispatchCommand("needs", append([]string{"scan", "--path=" + repo}, flags...)) })
	}
	nonGoal := func(capability string) string {
		return "version: 1\nnon_goals:\n  - capability: " + capability +
			"\n    rationale: Command lines belong to the application\n    alternative: the standard flag package\n"
	}
	// Negative: the fixture imports cobra (clikit.cobra), which the manifest declares a non-goal.
	repo := newNeedsRepo(t)
	writeFixtureFile(t, repo, needs.NeedsManifestName, nonGoal("clikit.cobra"))
	for _, flag := range []string{"--check", "--write"} {
		_, err := scan(repo, flag)
		if !errors.Is(err, needs.ErrNonGoalContradicted) || !strings.Contains(err.Error(), "non_goals[0] (clikit.cobra) is used by github.com/spf13/cobra") {
			t.Fatalf("needs scan %s with a contradicted non-goal: %v", flag, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(repo, needs.NeedsManifestName)); err != nil || string(data) != nonGoal("clikit.cobra") {
		t.Fatalf("a refused --write changed the manifest: %v\n%s", err, data)
	}
	// Positive: a non-goal the code does not use is written and then matches.
	clean := newNeedsRepo(t)
	writeFixtureFile(t, clean, needs.NeedsManifestName, nonGoal("clikit.tui"))
	if _, err := scan(clean, "--write"); err != nil {
		t.Fatalf("needs scan --write: %v", err)
	}
	if out, err := scan(clean, "--check"); err != nil || !strings.Contains(out, "matches a fresh scan") {
		t.Fatalf("needs scan --check: %v\n%s", err, out)
	}
	if data, err := os.ReadFile(filepath.Join(clean, needs.NeedsManifestName)); err != nil || !strings.Contains(string(data), "capability: clikit.tui") {
		t.Fatalf("written manifest dropped the non-goal: %v\n%s", err, data)
	}
	// Boundary: a capability declared both needed and a non-goal fails the scan itself.
	both := newNeedsRepo(t)
	writeFixtureFile(t, both, needs.NeedsManifestName, strings.Replace(nonGoal("clikit.tui"), "non_goals:", "capabilities:\n  required: [clikit.tui]\nnon_goals:", 1))
	if _, err := scan(both); err == nil || !strings.Contains(err.Error(), "non_goals[0] (clikit.tui) is also declared needed under capabilities.required") {
		t.Fatalf("needed and non-goal: %v", err)
	}
}

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
