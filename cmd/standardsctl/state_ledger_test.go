package main

import (
	"strings"
	"testing"
)

const cliLegacyState = "# Session State\n\n### [2026-09-11 17:22:14 UTC] Commit `173c565` on `main`\n" +
	"- **Activity**: Automated state synchronization\n- **Open Bugs**: 0 | **Pending Questions**: 0\n" +
	"\n<!-- praetor-state:v1 sha256:" + "08ef0524fb3a5a02b855b2934d5f4092792fe5c723dd7332dffb69edfe41e52c" + " -->\n"

func TestStateCompactCommand(t *testing.T) {
	dir := t.TempDir()
	if err := dispatchCommand("state", []string{"init", dir}); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, dir, ".workingdir/STATE.md", cliLegacyState)
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", dir}) }); err == nil {
		t.Fatal("compact accepted a ledger whose last marker does not verify")
	}
	if got := readFixtureFile(t, dir, ".workingdir/STATE.md"); got != cliLegacyState {
		t.Fatalf("refused compaction changed STATE.md:\n%s", got)
	}
	// Sync supersedes the trailing legacy marker, so compaction finds none left to drop.
	if err := dispatchCommand("state", []string{"sync", dir}); err != nil {
		t.Fatal(err)
	}
	if got := readFixtureFile(t, dir, ".workingdir/STATE.md"); strings.Count(got, "<!-- praetor-state") != 1 {
		t.Fatalf("sync kept a superseded marker:\n%s", got)
	}
	out, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", "--dir=" + dir}) })
	if err != nil || !strings.Contains(out, "compact: 1 entries rewritten, 1 kept, 0 markers dropped") {
		t.Fatalf("compact output %q, %v", out, err)
	}
	out, err = captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", dir}) })
	if err != nil || !strings.HasPrefix(out, "compact: already compact, no change") {
		t.Fatalf("second compact not a no-op: %q, %v", out, err)
	}
	if err := dispatchCommand("state", []string{"sync", "--verify", dir}); err != nil {
		t.Fatalf("compacted ledger does not verify: %v", err)
	}
	if _, err := captureStdout(t, func() error { return dispatchCommand("state", []string{"compact", "--bogus"}) }); err == nil {
		t.Fatal("unknown flag accepted")
	}
}
