package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/testsupport"
)

// Positive (#955, Refs #62): init writes .standards.yaml, a zero-debt baseline and a placeholder
// .standards.lock without content digests. Because the lock carries no digests, audit --offline
// rejects the freshly initialised repository until its lock is pinned.
func TestInitLock_PlaceholderWrittenAndRefusedByAudit(t *testing.T) {
	dir, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	lock := readFixtureFile(t, dir, ".standards.lock")
	if !strings.Contains(lock, "pinned_version:") {
		t.Fatalf("lock missing pinned_version:\n%s", lock)
	}
	if strings.Contains(lock, "digest:") {
		t.Fatalf("placeholder lock unexpectedly carries a digest:\n%s", lock)
	}

	audited, err := captureStdout(t, func() error {
		return dispatchCommand("audit", []string{"--offline", "--config=" + manifest})
	})
	if err == nil {
		t.Fatalf("audit --offline must reject an unpinned placeholder lock:\n%s", audited)
	}
	mustContain(t, audited+err.Error(), "top-level digest", "lockfile digest")
}

// Boundary (#955): the closing line names the repository the manifest records and no upstream
// name; without an owner and name it names nothing.
func TestInitMessage_Boundary_NamesOnlyTheConfiguredRepository(t *testing.T) {
	_, manifest := initContextRepo(t, fixtureAgentsMD, "")
	out, err := runInitCmd(t, "--output="+manifest)
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	mustContain(t, out, "Repository successfully onboarded: acme/kit")
	if strings.Contains(out, "cordanaLLM") {
		t.Fatalf("init printed a hard-coded upstream name:\n%s", out)
	}

	bare := t.TempDir()
	testsupport.InitGitRepoWithOrigin(t, bare, "")
	writeFixtureFile(t, bare, "AGENTS.md", fixtureAgentsMD)
	out, err = runInitCmd(t, "--output="+filepath.Join(bare, ".standards.yaml"))
	if err != nil {
		t.Fatalf("init without a remote: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Repository successfully onboarded.\n") || strings.Contains(out, "cordanaLLM") {
		t.Fatalf("an unresolved identity must print the bare line:\n%s", out)
	}
}

// Negative: init refuses to overwrite an existing manifest file.
func TestInit_Negative_RefusesExistingManifest(t *testing.T) {
	_, manifest := initContextRepo(t, fixtureAgentsMD, "")
	if _, err := runInitCmd(t, "--output="+manifest); err != nil {
		t.Fatalf("initial init failed: %v", err)
	}
	out, err := runInitCmd(t, "--output="+manifest)
	if err == nil {
		t.Fatalf("expected error on rerun, got output:\n%s", out)
	}
	mustErrContain(t, err, "already exists")
}
