package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/adopt"
)

// declineDevContainer declines the step the fixture cannot run: its catalog is config-only, so
// it cannot bootstrap a dev container. Audit honours the decline.
const declineDevContainer = "adoption:\n  decline:\n    - dev-container\n"

// Rows of the operating contract adoption synthesizes, and the operator's edit of them: one row
// reworded, one added, so the recomputed pins differ in count and digest.
const (
	generatedTimeoutRow = `"Timeout != failure. Re-check open PRs before retry; prevent duplicate PRs.",`
	editedTimeoutRows   = `"Timeout != failure. Re-check open PRs and CI before retry; prevent duplicate PRs.",` +
		"\n    " + `"Stale branch -> rebase first; rerun gate.",`
)

var (
	reportedCounts = regexp.MustCompile(`expected (\d+) applicable values, extracted (\d+)`)
	reportedDigest = regexp.MustCompile(`sha256 mismatch: declared (sha256:[0-9a-f]{64}), actual (sha256:[0-9a-f]{64})`)
)

// newForceAdoptFixture is the audit fixture with lefthook stubbed to fail, so adoption takes the
// deterministic fallback hook path, and PATH narrowed to that stub plus git.
func newForceAdoptFixture(t *testing.T) *auditFixture {
	t.Helper()
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
	return newAuditFixture(t)
}

// adoptFixture adopts the fixture with its own pinned catalog as the lock source.
func adoptFixture(t *testing.T, f *auditFixture, force bool) error {
	t.Helper()
	_, err := adopt.Adopt(t.Context(), adopt.AdoptOptions{Path: f.dir, Profile: "framework",
		Force: force, LockSourceRoot: f.dir})
	return err
}

// TestAdoptForceKeepsEditedHarnessAuditGreen (#502) Positive, end to end: the audit fixture's
// Paperclip harness carries an operator-edited operating contract. adopt --force keeps it byte
// for byte, so the contract bound to it holds and the full audit still passes, its Paperclip
// gate included. Before #502, --force replaced it with a fresh synthesis.
func TestAdoptForceKeepsEditedHarnessAuditGreen(t *testing.T) {
	f := newForceAdoptFixture(t)
	writeFixtureFile(t, f.dir, ".standards.yaml", fixtureManifest("acme", "widgets", false)+declineDevContainer)
	if err := adoptFixture(t, f, true); err != nil {
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

// TestAdoptForceEditedHarnessNeedsRecomputedPins (#502 U9) Negative, then Positive, end to end:
// adopt writes the harness and binds register.sources to it; the operator edits the operating
// contract without re-binding. adopt --force stops with the remedy and leaves harness and
// manifest as they were. The operator runs the command the remedy names, sets the values it
// reports, and adopt --force then succeeds, keeps the edit, and that command passes.
func TestAdoptForceEditedHarnessNeedsRecomputedPins(t *testing.T) {
	f := newForceAdoptFixture(t)
	harnessRel := ".paperclip/harness.json"
	writeFixtureFile(t, f.dir, ".standards.yaml",
		strings.TrimSuffix(fixtureManifest("acme", "widgets", false), fixtureRegisterSources())+declineDevContainer)
	if err := os.Remove(filepath.Join(f.dir, filepath.FromSlash(harnessRel))); err != nil {
		t.Fatal(err)
	}
	if err := adoptFixture(t, f, false); err != nil {
		t.Fatalf("first adopt: %v", err)
	}
	edited := editGeneratedContract(t, readFixtureFile(t, f.dir, harnessRel))
	writeFixtureFile(t, f.dir, harnessRel, edited)
	if out, err := runFixtureGit(t, f.dir, f.gitEnv, "add", "-A"); err != nil {
		t.Fatalf("stage the adopted tree: %v (%s)", err, out)
	}
	manifest := readFixtureFile(t, f.dir, ".standards.yaml")
	err := adoptFixture(t, f, true)
	mustErrContain(t, err, "existing register.sources fails its configured gate")
	mustErrContain(t, err, "recompute the pins with `praetorctl caveman check --configured-sources --root=.`")
	if readFixtureFile(t, f.dir, harnessRel) != edited || readFixtureFile(t, f.dir, ".standards.yaml") != manifest {
		t.Fatal("the refused adopt --force wrote the harness or the manifest")
	}
	_, checkErr := runCavemanCLI(t, "", "check", "--configured-sources", "--root="+f.dir)
	writeFixtureFile(t, f.dir, ".standards.yaml", applyReportedPins(t, manifest, checkErr))
	if err := adoptFixture(t, f, true); err != nil {
		t.Fatalf("adopt --force after recomputing the pins: %v", err)
	}
	if got := readFixtureFile(t, f.dir, harnessRel); got != edited {
		t.Fatalf("adopt --force dropped the operator's edit:\n%s", got)
	}
	if out, err := runCavemanCLI(t, "", "check", "--configured-sources", "--root="+f.dir); err != nil {
		t.Fatalf("configured-sources check after the remedy: %v\n%s", err, out)
	}
}

// editGeneratedContract rewords one row of the generated operating contract and adds another.
func editGeneratedContract(t *testing.T, generated string) string {
	t.Helper()
	edited := strings.Replace(generated, generatedTimeoutRow, editedTimeoutRows, 1)
	if edited == generated || !json.Valid([]byte(edited)) {
		t.Fatalf("fixture precondition: the generated harness lacks the timeout row:\n%s", generated)
	}
	return edited
}

// applyReportedPins sets expected and sha256 in manifest to the extracted values the
// configured-sources check reported in err, as an operator copies them. The edit adds a row, so
// the one report must carry both.
func applyReportedPins(t *testing.T, manifest string, err error) string {
	t.Helper()
	report := fmt.Sprint(err)
	counts, digest := reportedCounts.FindStringSubmatch(report), reportedDigest.FindStringSubmatch(report)
	if counts == nil || digest == nil {
		t.Fatalf("configured-sources check did not report every recomputed value: %v", err)
	}
	pinned := strings.Replace(manifest, "expected: "+counts[1]+"\n", "expected: "+counts[2]+"\n", 1)
	pinned = strings.Replace(pinned, digest[1], digest[2], 1)
	if !strings.Contains(pinned, "expected: "+counts[2]+"\n") || !strings.Contains(pinned, digest[2]) {
		t.Fatalf("reported values not found in the manifest:\n%s", manifest)
	}
	return pinned
}
