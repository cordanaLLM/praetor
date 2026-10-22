// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/config"
)

func manifestRepoWithTimeout(t *testing.T, timeout string) string {
	t.Helper()
	return commitDeclaredTimeout(t, committedAdmitRepo(t, "pyproject.toml"), timeout)
}

// commitDeclaredTimeout commits a manifest declaring timeout as the declared-verification bound.
func commitDeclaredTimeout(t *testing.T, dir, timeout string) string {
	t.Helper()
	content := fmt.Sprintf("version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - pages-site\ngate:\n  declared_verify_timeout: %s\n", timeout)
	writeFile(t, filepath.Join(dir, ".standards.yaml"), content)
	treeGit(t, dir, "add", "--", ".standards.yaml")
	treeGit(t, dir, "commit", "-q", "-m", "declare verify timeout")
	return dir
}

// Positive: declared timeout 20m sets the stage bound to 20m and the run deadline covers it.
func TestResolveRepoRunBudget_Positive_DeclaredVerifyTimeout(t *testing.T) {
	dir := manifestRepoWithTimeout(t, "20m")
	budget, err := ResolveRepoRunBudget("", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}

	wantBound := 20 * time.Minute
	if budget.StageBound != wantBound {
		t.Fatalf("budget.StageBound = %s, want %s", budget.StageBound, wantBound)
	}
	if budget.BoundOrigin != ".standards.yaml" {
		t.Errorf("budget.BoundOrigin = %q, want %q", budget.BoundOrigin, ".standards.yaml")
	}
	if budget.Timeout() < budget.StageBound {
		t.Fatalf("run deadline %s does not cover stage bound %s", budget.Timeout(), budget.StageBound)
	}
	wantTimeout := 20*time.Minute + OtherStagesAllowance
	if budget.Timeout() != wantTimeout {
		t.Errorf("budget.Timeout() = %s, want %s", budget.Timeout(), wantTimeout)
	}
	if !strings.Contains(budget.Note, ".standards.yaml") || !strings.Contains(budget.Note, "20m0s") {
		t.Errorf("budget.Note = %q, want mention of .standards.yaml and 20m0s", budget.Note)
	}
}

// Negative: 31m and 0s are refused naming the ceiling.
func TestResolveRepoRunBudget_Negative_RefusesExceededCeilingAndNonPositive(t *testing.T) {
	cases := []struct {
		name    string
		timeout string
		want    []string
	}{
		{"over ceiling 31m", "31m", []string{"declared_verify_timeout", "31m", config.MaxDeclaredVerifyTimeout.String()}},
		{"zero duration", "0s", []string{"declared_verify_timeout", "0s", "not positive", config.MaxDeclaredVerifyTimeout.String()}},
		{"negative duration", "-1m", []string{"declared_verify_timeout", "-1m", "not positive", config.MaxDeclaredVerifyTimeout.String()}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := manifestRepoWithTimeout(t, tc.timeout)
			_, err := ResolveRepoRunBudget("", dir)
			if err == nil {
				t.Fatalf("ResolveRepoRunBudget with %s expected error, got nil", tc.timeout)
			}
			for _, w := range tc.want {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q does not contain %q", err.Error(), w)
				}
			}
		})
	}
}

// Boundary: the environment variable overrides the declared value.
func TestResolveRepoRunBudget_Boundary_EnvironmentOverridesDeclaredValue(t *testing.T) {
	dir := manifestRepoWithTimeout(t, "20m")
	budget, err := ResolveRepoRunBudget("10m", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}

	wantBound := 10 * time.Minute
	if budget.StageBound != wantBound {
		t.Fatalf("budget.StageBound = %s, want %s (environment should override declared)", budget.StageBound, wantBound)
	}
	if budget.BoundOrigin != "environment" {
		t.Errorf("budget.BoundOrigin = %q, want %q", budget.BoundOrigin, "environment")
	}
}

// Negative: an environment override cannot hide an invalid repository declaration or run make.
func TestRunDeclaredVerifyStage_Negative_CeilingRefusedBeforeCommand(t *testing.T) {
	requireMake(t)
	t.Setenv(TestStageTimeoutEnv, "10m")
	dir := commitDeclaredTimeout(t, declaredVerifyRepo(t, "@exit 0"), "30m1ns")
	cfg, recorded := newTestConfig(t, dir, false)
	_, err := runDeclaredVerifyStage(t.Context(), cfg)
	if err == nil {
		t.Fatal("declared verification accepted a timeout above its hard ceiling")
	}
	for _, want := range []string{"gate.declared_verify_timeout", "30m1ns", "30m0s", "ceiling"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if cfg.declaredVerified || len(*recorded) != 0 {
		t.Fatalf("invalid budget ran commands or verified the stage: verified=%v commands=%v", cfg.declaredVerified, *recorded)
	}
}

// slowDeclaredVerifyRepo is a committed Python root whose verify-all sleeps for three seconds and
// whose manifest declares timeout as the declared-verification bound.
func slowDeclaredVerifyRepo(t *testing.T, timeout string) string {
	t.Helper()
	return commitDeclaredTimeout(t, declaredVerifyRepo(t, "@sleep 3"), timeout)
}

// Negative (order engine-gate-declared-verification-deadline-20261018): a verification slower
// than the bound the repository declares is cut off, and the reason names the bound and where it
// came from.
func TestRunGatedPipeline_Negative_SlowVerifyOverDeclaredBoundFails(t *testing.T) {
	requireMake(t)
	t.Setenv(TestStageTimeoutEnv, "")
	rep := gatedRun(t, slowDeclaredVerifyRepo(t, "1s"), RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusRejected || got.Status != StageFailed {
		t.Fatalf("status %s, stage %s %q; want rejected at the declared verification", rep.Status, got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "did not finish within 1s (.standards.yaml)") {
		t.Errorf("reason %q does not name the declared bound", got.Message)
	}
}

// Positive: the same verification under a longer declared bound passes.
func TestRunGatedPipeline_Positive_SlowVerifyUnderDeclaredBoundPasses(t *testing.T) {
	requireMake(t)
	t.Setenv(TestStageTimeoutEnv, "")
	rep := gatedRun(t, slowDeclaredVerifyRepo(t, "30s"), RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if got.Status != StagePassed || got.Message != "make verify-all exited 0" {
		t.Fatalf("stage %s %q; want passed under the declared 30s bound", got.Status, got.Message)
	}
}
