package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// TestAdoptForceKeepsEditedHarnessAuditGreen (#502) Positive, end to end: the audit fixture's
// Paperclip harness carries an operator-edited operating contract. adopt --force keeps it byte
// for byte, so the contract bound to it holds and the full audit still passes, its Paperclip
// gate included. Before #502, --force replaced it with a fresh synthesis.
func TestAdoptForceKeepsEditedHarnessAuditGreen(t *testing.T) {
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	stubDir := t.TempDir()
	stub := writeFixtureFile(t, stubDir, "lefthook", "#!/bin/sh\nexit 1\n")
	if err := os.Chmod(stub, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+filepath.Dir(gitPath))
	f := newAuditFixture(t)
	// The fixture's catalog is config-only, so it cannot bootstrap a dev container; the
	// manifest declines that step, which audit honours, instead of pinning a full source root.
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+
		"adoption:\n  decline:\n    - dev-container\n")
	if _, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, Profile: "framework",
		Force: true, LockSourceRoot: f.dir}); err != nil {
		t.Fatalf("adopt --force: %v", err)
	}
	harness, err := os.ReadFile(filepath.Join(f.dir, ".paperclip", "harness.json"))
	if err != nil || string(harness) != fixtureHarnessJSON {
		t.Fatalf("adopt --force rewrote the operator-owned harness (%v):\n%s", err, harness)
	}
	out, err := f.audit(t)
	if err != nil {
		t.Fatalf("audit after adopt --force: %v\n%s", err, out)
	}
	mustContain(t, out, "[PASS] Paperclip agent runtime harness verified (acme/widgets, 1 rules).")
}
