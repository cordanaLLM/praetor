// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package adopt

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestLefthookInstallFailureReported checks the fallback cannot silently earn HISS-14 credit.
func TestLefthookInstallFailureReported(t *testing.T) {
	repo := newTestRepo(t, "install-failure")
	stubs := t.TempDir()
	writeStub(t, stubs, "lefthook", "case \"$1\" in version) exit 0 ;; install) exit 1 ;; esac\n")
	hermeticPath(t, stubs)
	rep, err := Adopt(context.Background(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t)})
	if err != nil && rep == nil {
		t.Fatalf("adopt without report: %v", err)
	}
	if installedHook(t, repo) != "fallback" {
		t.Fatal("fallback hook missing")
	}
	text := strings.Join(rep.Errors, " ")
	if !strings.Contains(text, "lefthook install") || !strings.Contains(text, "commit-msg") || !strings.Contains(text, "pre-push") || !strings.Contains(text, "rerun adopt") {
		t.Fatalf("missing activation failure report: %s", text)
	}
}

// TestLefthookInstallSuccessReported retains the active hook claim on success.
func TestLefthookInstallSuccessReported(t *testing.T) {
	repo := newTestRepo(t, "install-success")
	useLefthook(t, lefthookRuns)
	rep, err := Adopt(context.Background(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t)})
	if err != nil || len(rep.Errors) != 0 {
		t.Fatalf("adopt: %v %+v", err, rep.Errors)
	}
	if installedHook(t, repo) != "lefthook" || !strings.Contains(mustRead(t, filepath.Join(repo, agentsFile)), "lefthook commit-msg/pre-push") {
		t.Fatal("active stages not credited")
	}
}

// TestLefthookPolicyPriorMigrates verifies adoption upgrades the previous generated script.
func TestLefthookPolicyPriorMigrates(t *testing.T) {
	repo := newTestRepo(t, "policy-prior")
	old := strings.Replace(buildCommitPolicyScript(), " --remote=\"$1\"", "", 1)
	path := filepath.Join(repo, filepath.FromSlash(commitPolicyScriptFile))
	mustWrite(t, path, old)
	rep, err := Adopt(context.Background(), AdoptOptions{Path: repo, LockSourceRoot: newAdoptLockSource(t)})
	if err != nil || len(rep.Errors) != 0 {
		t.Fatalf("adopt: %v %+v", err, rep.Errors)
	}
	if got := mustRead(t, path); got != buildCommitPolicyScript() {
		t.Fatal("prior script not upgraded")
	}
}
