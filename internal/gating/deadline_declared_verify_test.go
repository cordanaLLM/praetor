// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package gating

import (
	"context"
	"errors"
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
	// The declared bound covers worktree setup too, so on a slow runner setup can be what hits it
	// (#1566): either reason must name the bound and where it came from.
	if !strings.Contains(got.Message, "did not finish within 1s (.standards.yaml)") &&
		!strings.Contains(got.Message, "hit the 1s stage bound (.standards.yaml)") {
		t.Errorf("reason %q does not name the declared bound and origin", got.Message)
	}
}

// Negative (#1566): when worktree creation itself hits the declared bound, the reason names
// the bound and origin.
func TestRunGatedPipeline_Negative_SetupCutOverDeclaredBoundFails(t *testing.T) {
	requireMake(t)
	t.Setenv(TestStageTimeoutEnv, "")
	rep := gatedRun(t, commitDeclaredTimeout(t, declaredVerifyRepo(t, "@exit 0"), "1ns"), RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	if rep.Status != StatusRejected || got.Status != StageFailed {
		t.Fatalf("status %s, stage %s %q; want rejected at the declared verification", rep.Status, got.Status, got.Message)
	}
	if !strings.Contains(got.Message, "hit the 1ns stage bound (.standards.yaml)") {
		t.Errorf("reason %q does not name the declared bound and origin on setup cut", got.Message)
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

// commitDeclaredTimeoutWithEvidence writes an evidence file and a manifest declaring timeout and
// evidenceRel in dir, committing the evidence file when commitEvidence is true.
func commitDeclaredTimeoutWithEvidence(t *testing.T, dir, timeout, evidenceRel, measured string, commitEvidence bool) string {
	t.Helper()
	if evidenceRel != "" && measured != "" {
		evContent := fmt.Sprintf("command: make verify-all\nmeasured_duration: %s\ndate: \"2026-11-18\"\ncommit: 0123456789abcdef0123456789abcdef01234567\n", measured)
		writeFile(t, filepath.Join(dir, filepath.FromSlash(evidenceRel)), evContent)
		if commitEvidence {
			treeGit(t, dir, "add", "--", evidenceRel)
		}
	}
	manifest := fmt.Sprintf("version: 1\nrepository:\n  owner: fixture\n  name: fixture\nprofiles:\n  - pages-site\ngate:\n  declared_verify_timeout: %s\n", timeout)
	if evidenceRel != "" {
		manifest += fmt.Sprintf("  declared_verify_evidence: %q\n", evidenceRel)
	}
	writeFile(t, filepath.Join(dir, ".standards.yaml"), manifest)
	treeGit(t, dir, "add", "--", ".standards.yaml")
	treeGit(t, dir, "commit", "-q", "-m", "declare verify timeout with evidence")
	return dir
}

// TestResolveRepoRunBudget_Positive_RaisedBoundWithEvidence verifies that a 45m or 60m bound with
// committed evidence raises StageBound for a declared-verification repository only.
func TestResolveRepoRunBudget_Positive_RaisedBoundWithEvidence(t *testing.T) {
	dir := commitDeclaredTimeoutWithEvidence(t, committedAdmitRepo(t, "pyproject.toml"), "45m", "docs/verify-evidence.yaml", "38m", true)
	budget, err := ResolveRepoRunBudget("", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}
	if budget.StageBound != 45*time.Minute || budget.EvidencePath != "docs/verify-evidence.yaml" || budget.MeasuredDuration != 38*time.Minute {
		t.Fatalf("budget = %+v; want 45m stage bound with docs/verify-evidence.yaml (38m)", budget)
	}
	if budget.Timeout() != 45*time.Minute+OtherStagesAllowance {
		t.Errorf("budget.Timeout() = %s, want %s", budget.Timeout(), 45*time.Minute+OtherStagesAllowance)
	}
	for _, want := range []string{"45m0s", "docs/verify-evidence.yaml", "38m0s"} {
		if !strings.Contains(budget.Note, want) {
			t.Errorf("budget.Note %q does not contain %q", budget.Note, want)
		}
	}
}

// TestResolveRepoRunBudget_Boundary_ToolchainRepoKeeps30mCeiling verifies that a Go repository
// does not receive the raised declared-verification bound.
func TestResolveRepoRunBudget_Boundary_ToolchainRepoKeeps30mCeiling(t *testing.T) {
	dir := deadlineMarkerFixture(t, []string{"go.mod"})
	commitDeclaredTimeoutWithEvidence(t, dir, "45m", "docs/verify-evidence.yaml", "38m", true)
	budget, err := ResolveRepoRunBudget("", dir)
	if err != nil {
		t.Fatalf("ResolveRepoRunBudget returned error: %v", err)
	}
	if budget.StageBound != TestStageTimeout || budget.EvidencePath != "" {
		t.Fatalf("Go repository budget = %+v; want default %s race stage bound", budget, TestStageTimeout)
	}
}

// blockedMakeVerify simulates a hung `make verify-all` command killed once its stage bound expires.
func blockedMakeVerify(ran *bool, arm func()) commandRunner {
	return func(runCtx context.Context, dir, name string, args ...string) (string, error) {
		if name == "make" && len(args) > 0 && args[0] == declaredVerifyTarget {
			*ran = true
			if arm != nil {
				arm()
			}
			<-runCtx.Done()
			return "", errors.New("signal: killed")
		}
		return fakeRunner(&[]recordedCommand{}, "", nil)(runCtx, dir, name, args...)
	}
}

// TestRunDeclaredVerifyStage_Positive_RaisedBoundOutputNamesEvidence verifies that when a raised
// bound is in force, the passing stage message names the declared bound, evidence path, and measured duration.
func TestRunDeclaredVerifyStage_Positive_RaisedBoundOutputNamesEvidence(t *testing.T) {
	requireMake(t)
	t.Setenv(TestStageTimeoutEnv, "")
	dir := commitDeclaredTimeoutWithEvidence(t, declaredVerifyRepo(t, "@exit 0"), "45m", "docs/verify-evidence.yaml", "38m", true)
	rep := gatedRun(t, dir, RunOptions{AdmitUnsupported: true})
	got := stageNamed(t, rep, stageDeclaredVerify)
	want := "make verify-all exited 0 (declared bound 45m0s, evidence docs/verify-evidence.yaml, measured 38m0s)"
	if rep.Status != StatusAdmitted || got.Status != StagePassed || got.Message != want {
		t.Fatalf("status=%s stage=%s msg=%q; want %q", rep.Status, got.Status, got.Message, want)
	}
}

// TestRunDeclaredVerifyStage_Negative_RaisedBoundCutOutputNamesEvidence verifies that when the
// stage hits a raised bound, the failure message names the declared bound, evidence path, and measured duration.
func TestRunDeclaredVerifyStage_Negative_RaisedBoundCutOutputNamesEvidence(t *testing.T) {
	t.Setenv(TestStageTimeoutEnv, "")
	dir := commitDeclaredTimeoutWithEvidence(t, declaredVerifyRepo(t, "@exit 0"), "45m", "docs/verify-evidence.yaml", "38m", true)
	cfg, _ := newTestConfig(t, dir, false)
	cfg.lookPath = func(string) (string, error) { return "make", nil }
	var seen time.Duration
	ran := false
	cfg.run = blockedMakeVerify(&ran, holdStageBoundAt(cfg, time.Millisecond, &seen))
	_, err := runDeclaredVerifyStage(context.Background(), cfg)
	if err == nil || !ran {
		t.Fatalf("expected hung declared verify to fail at bound: ran=%v err=%v", ran, err)
	}
	if seen != 45*time.Minute {
		t.Errorf("stage requested bound %s, want 45m0s", seen)
	}
	wantPrefix := "declared verification make verify-all did not finish within 45m0s (.standards.yaml: evidence docs/verify-evidence.yaml, measured 38m0s):"
	if !strings.HasPrefix(err.Error(), wantPrefix) {
		t.Errorf("error = %q, want prefix %q", err.Error(), wantPrefix)
	}
}

// TestRunDeclaredVerifyStage_Boundary_UndeclaredRepoOutputByteIdentical verifies that a repository
// without a raised bound keeps the 30m ceiling and prints byte-identical pass and timeout messages.
func TestRunDeclaredVerifyStage_Boundary_UndeclaredRepoOutputByteIdentical(t *testing.T) {
	t.Setenv(TestStageTimeoutEnv, "")
	dir := declaredVerifyRepo(t, "@exit 0")
	budget, err := ResolveRepoRunBudget("", dir)
	if err != nil || budget.StageBound != TestStageTimeout || MaxTestStageTimeout != 30*time.Minute {
		t.Fatalf("undeclared budget = %+v, err = %v; want %s default under 30m ceiling", budget, err, TestStageTimeout)
	}
	if got := declaredVerifyPassMessage(budget); got != "make verify-all exited 0" {
		t.Errorf("undeclared pass message = %q, want exact %q", got, "make verify-all exited 0")
	}
	cfg, _ := newTestConfig(t, dir, false)
	cfg.lookPath = func(string) (string, error) { return "make", nil }
	var seen time.Duration
	ran := false
	cfg.run = blockedMakeVerify(&ran, holdStageBoundAt(cfg, time.Millisecond, &seen))
	_, cutErr := runDeclaredVerifyStage(context.Background(), cfg)
	if cutErr == nil || !ran || seen != TestStageTimeout {
		t.Fatalf("undeclared cut: ran=%v seen=%s err=%v", ran, seen, cutErr)
	}
	wantPrefix := "declared verification make verify-all did not finish within 20m0s (default):"
	if !strings.HasPrefix(cutErr.Error(), wantPrefix) {
		t.Errorf("undeclared timeout error = %q, want exact prefix %q", cutErr.Error(), wantPrefix)
	}
}
