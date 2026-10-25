// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package secretscan_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
	"github.com/cordanaLLM/praetor/internal/secretscan"
	"github.com/cordanaLLM/praetor/internal/util"
	"github.com/cordanaLLM/praetor/tools/secrets"
	"go.yaml.in/yaml/v3"
)

var testDay = time.Date(2026, time.October, 18, 0, 0, 0, 0, time.UTC)

func writeManifest(t *testing.T, dir string, exceptions []config.Exception) {
	t.Helper()
	manifest := config.Manifest{
		Version:    1,
		Exceptions: exceptions,
	}
	data, err := yaml.Marshal(&manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".standards.yaml"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func defaultOptions(dir string) secretscan.Options {
	return secretscan.Options{
		Dir:     dir,
		Scanner: []string{"gitleaks"},
		Run:     util.RunCommand,
		Now:     func() time.Time { return testDay },
	}
}

func checkPlantedMergeWithoutM(t *testing.T, dir string) {
	t.Helper()
	oldOpts := defaultOptions(dir)
	var oldArgs []string
	oldOpts.Run = func(ctx context.Context, d, name string, args ...string) (string, error) {
		filtered := make([]string, 0, len(args))
		for _, a := range args {
			if strings.HasPrefix(a, "--log-opts=") {
				filtered = append(filtered, "--log-opts=--full-history --all")
			} else {
				filtered = append(filtered, a)
			}
		}
		oldArgs = filtered
		return util.RunCommand(ctx, d, name, filtered...)
	}
	oldReport, err := secretscan.Check(t.Context(), oldOpts)
	if err != nil {
		t.Fatalf("old scan failed: %v", err)
	}
	if oldReport.Failed() || len(oldReport.Blocking) != 0 {
		t.Fatalf("expected old scan without -m to pass, got: %d findings with args %v", len(oldReport.Blocking), oldArgs)
	}
}

func checkPlantedMergeWithM(t *testing.T, dir, mergeHash string) {
	t.Helper()
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan failed: %v", err)
	}
	if !report.Failed() {
		t.Fatal("expected secretscan.Check to fail on merge commit leak")
	}
	if len(report.Blocking) == 0 {
		t.Fatal("expected blocking findings for merge commit leak")
	}
	for _, finding := range report.Blocking {
		if finding.Commit != mergeHash {
			t.Errorf("finding commit = %q, want merge commit %q", finding.Commit, mergeHash)
		}
		if finding.File != "creds.py" {
			t.Errorf("finding file = %q, want creds.py", finding.File)
		}
	}
}

func TestCheck_PlantedMergeOnly_PositiveAndNegative(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, mergeHash := secrets.BuildMergeLeakRepo(t)
	checkPlantedMergeWithoutM(t, dir)
	checkPlantedMergeWithM(t, dir, mergeHash)
}

func checkOrdinaryLeak(t *testing.T, exceptions []config.Exception) *secretscan.Report {
	t.Helper()
	dir, _ := secrets.BuildOrdinaryLeakRepo(t)
	if exceptions != nil {
		writeManifest(t, dir, exceptions)
	}
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	return report
}

func testExceptionsLifecycle_Unexcused(t *testing.T) {
	report := checkOrdinaryLeak(t, nil)
	if !report.Failed() || len(report.Blocking) != 1 {
		t.Fatalf("expected 1 blocking finding, got %d", len(report.Blocking))
	}
}

func testExceptionsLifecycle_Unexpired(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "creds.py",
			Tool:    "generic-api-key",
			Expires: "2026-11-01",
			Reason:  "temporary test waiver",
		},
	})
	if report.Failed() {
		t.Fatalf("expected pass with unexpired exception, got failed: %s", report.Summary())
	}
	if len(report.Allowed) != 1 || len(report.Blocking) != 0 {
		t.Fatalf("expected 1 allowed and 0 blocking, got allowed=%d, blocking=%d", len(report.Allowed), len(report.Blocking))
	}
}

func testExceptionsLifecycle_Expired(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "creds.py",
			Tool:    "generic-api-key",
			Expires: "2026-10-01",
			Reason:  "expired waiver",
		},
	})
	if !report.Failed() || len(report.Blocking) != 1 {
		t.Fatalf("expected expired exception to refuse finding, got blocking=%d", len(report.Blocking))
	}
}

func testExceptionsLifecycle_Unused(t *testing.T) {
	report := checkOrdinaryLeak(t, []config.Exception{
		{
			Rule:    config.ExceptionRuleSecrets,
			Path:    "other.py",
			Tool:    "generic-api-key",
			Expires: "2026-11-01",
			Reason:  "waiver for other file",
		},
	})
	if !report.Failed() {
		t.Fatal("expected failure on unused exception")
	}
	if len(report.Unused) != 1 {
		t.Fatalf("expected 1 unused exception, got %d", len(report.Unused))
	}
}

func TestCheck_ExceptionsLifecycle_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}

	t.Run("unexcused token is refused", testExceptionsLifecycle_Unexcused)
	t.Run("unexpired exception excuses token", testExceptionsLifecycle_Unexpired)
	t.Run("expired exception excuses nothing", testExceptionsLifecycle_Expired)
	t.Run("unused exception fails", testExceptionsLifecycle_Unused)
}

func TestCheck_CleanRepo_Passes(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir := secrets.BuildCleanRepo(t)

	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("clean scan error: %v", err)
	}
	if report.Failed() {
		t.Fatalf("clean repo failed: %s", report.Summary())
	}
	if report.Summary() != "zero secret findings in history" {
		t.Errorf("summary = %q, want 'zero secret findings in history'", report.Summary())
	}
}

func TestCheck_ErrorHandling(t *testing.T) {
	var nilCtx context.Context
	if _, err := secretscan.Check(nilCtx, secretscan.Options{}); err == nil {
		t.Error("expected error on nil context")
	}
	ctx := t.Context()
	if _, err := secretscan.Check(ctx, secretscan.Options{}); err == nil {
		t.Error("expected error on empty options")
	}
	runnerErr := fmt.Errorf("runner failed")
	mockRunner := func(ctx context.Context, dir, name string, args ...string) (string, error) {
		return "stderr output", runnerErr
	}
	_, err := secretscan.Check(ctx, secretscan.Options{
		Dir: t.TempDir(),
		Run: mockRunner,
	})
	if err == nil {
		t.Error("expected error when runner fails")
	}
}

func testForeignRemoteExcluded(t *testing.T, dir string) {
	t.Helper()
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if report.Failed() || len(report.Blocking) != 0 {
		t.Fatalf("expected foreign remote refs to be excluded, got %d blocking findings: %v",
			len(report.Blocking), report.Blocking)
	}
}

func testRefIncluded(t *testing.T, dir, addRef, delRef, leakHash, scopeLabel string) {
	t.Helper()
	secrets.UpdateRef(t, dir, addRef, leakHash)
	secrets.DeleteRef(t, dir, delRef)
	report, err := secretscan.Check(t.Context(), defaultOptions(dir))
	if err != nil {
		t.Fatalf("scan error: %v", err)
	}
	if !report.Failed() || len(report.Blocking) == 0 {
		t.Fatalf("expected blocking finding for secret reachable from %s", scopeLabel)
	}
	if report.Blocking[0].Commit != leakHash {
		t.Errorf("commit = %q, want %q", report.Blocking[0].Commit, leakHash)
	}
}

func TestCheck_RefScoping_3D(t *testing.T) {
	if _, err := exec.LookPath("gitleaks"); err != nil {
		t.Skip("gitleaks not installed on PATH")
	}
	dir, leakHash := secrets.BuildForeignRemoteLeakRepo(t)

	t.Run("foreign remote refs excluded", func(t *testing.T) {
		testForeignRemoteExcluded(t, dir)
	})
	t.Run("origin remote ref included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/remotes/origin/feature", "refs/remotes/upstream/leak", leakHash, "refs/remotes/origin/feature")
	})
	t.Run("local branch included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/heads/feature", "refs/remotes/origin/feature", leakHash, "local branch")
	})
	t.Run("tag included", func(t *testing.T) {
		testRefIncluded(t, dir, "refs/tags/v1.0.0", "refs/heads/feature", leakHash, "tag")
	})
}
