// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// pipelineCall is what a stubbed gating pipeline was handed.
type pipelineCall struct {
	called   bool
	deadline time.Time
	repoDir  string
	dryRun   bool
}

// recordPipeline replaces the gating pipeline for one test and records the call it receives.
func recordPipeline(t *testing.T) *pipelineCall {
	t.Helper()
	call := &pipelineCall{}
	original := gatedPipeline
	gatedPipeline = func(ctx context.Context, repoDir string, dryRun bool) (*gating.PipelineReport, error) {
		deadline, ok := ctx.Deadline()
		if !ok {
			t.Error("the pipeline must be handed a bounded context (HISS-02)")
		}
		*call = pipelineCall{called: true, deadline: deadline, repoDir: repoDir, dryRun: dryRun}
		return &gating.PipelineReport{Status: gating.StatusAdmitted, RepoDir: repoDir, DryRun: dryRun}, nil
	}
	t.Cleanup(func() { gatedPipeline = original })
	return call
}

// stubPipeline replaces the gating pipeline for one test and records the deadline it was handed.
func stubPipeline(t *testing.T) *time.Time {
	t.Helper()
	return &recordPipeline(t).deadline
}

// mustLeave fails unless deadline lies within a minute below want from now.
func mustLeave(t *testing.T, deadline time.Time, want time.Duration) {
	t.Helper()
	if left := time.Until(deadline); left <= want-time.Minute || left > want {
		t.Fatalf("deadline %s away, want about %s", left, want)
	}
}

// Positive: a 12m race-stage bound reaches the pipeline as a 12m + allowance run deadline. The
// fixed five-minute run deadline made every bound above it unreachable (#314).
func TestGateRun_Positive_RunDeadlineFollowsTheStageBound(t *testing.T) {
	seen := stubPipeline(t)
	t.Setenv(gating.TestStageTimeoutEnv, "12m")

	out, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--path=" + t.TempDir()})
	})
	if err != nil {
		t.Fatalf("gate run: %v", err)
	}
	want := 12*time.Minute + gating.OtherStagesAllowance
	mustLeave(t, *seen, want)
	mustContain(t, out, "Run Deadline: "+want.String(), "12m0s race stage bound")
}

// Negative: an unusable value neither removes the run deadline nor shrinks it; it falls back to
// the default bound plus the allowance, like the stage itself.
func TestGateRun_Negative_InvalidStageBoundKeepsTheDefaultRunDeadline(t *testing.T) {
	seen := stubPipeline(t)
	t.Setenv(gating.TestStageTimeoutEnv, "soon")

	if _, err := captureStdout(t, func() error {
		return dispatchCommand("gate", []string{"run", "--path=" + t.TempDir()})
	}); err != nil {
		t.Fatalf("gate run: %v", err)
	}
	mustLeave(t, *seen, gating.TestStageTimeout+gating.OtherStagesAllowance)
}

// The gatekeeper agent runs the same pipeline, and had the same fixed five minutes.
func TestAgentContext_3D(t *testing.T) {
	// Positive: the gatekeeper takes the gate run's derived deadline.
	seen := stubPipeline(t)
	t.Setenv(gating.TestStageTimeoutEnv, "12m")
	if _, err := captureStdout(t, func() error { return dispatchAgentTask("praetor-gatekeeper", nil) }); err != nil {
		t.Fatalf("gatekeeper: %v", err)
	}
	mustLeave(t, *seen, 12*time.Minute+gating.OtherStagesAllowance)

	// Boundary: the ceiling is the longest run the gatekeeper can be given.
	t.Setenv(gating.TestStageTimeoutEnv, "31m")
	ctx, cancel := agentContext("praetor_gatekeeper")
	defer cancel()
	deadline, _ := ctx.Deadline()
	mustLeave(t, deadline, gating.MaxTestStageTimeout+gating.OtherStagesAllowance)

	// Negative: every other helper keeps its own bound; the variable is the race stage's.
	other, cancelOther := agentContext("praetor-auditor")
	defer cancelOther()
	otherDeadline, _ := other.Deadline()
	mustLeave(t, otherDeadline, agentTimeout)
}

func TestGateDeadline_3D(t *testing.T) {
	cases := []struct {
		name, raw, bound, note string
		seconds                int64
	}{
		// Positive: unset reports the default bound plus the allowance, with no note.
		{"unset", "", "3m0s", "", int64((gating.TestStageTimeout + gating.OtherStagesAllowance).Seconds())},
		// Boundary: the exact ceiling, and one step past it, which clamps and says so.
		{"exact ceiling", "30m", "30m0s", "raised", int64((gating.MaxTestStageTimeout + gating.OtherStagesAllowance).Seconds())},
		{"over ceiling", "31m", "30m0s", "clamped", int64((gating.MaxTestStageTimeout + gating.OtherStagesAllowance).Seconds())},
		// Negative: an unparseable value falls back to the default and says why.
		{"invalid", "soon", "3m0s", "not a duration", int64((gating.TestStageTimeout + gating.OtherStagesAllowance).Seconds())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(gating.TestStageTimeoutEnv, tc.raw)
			out, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{"deadline", "--json"}) })
			if err != nil {
				t.Fatalf("gate deadline --json: %v", err)
			}
			var got gateDeadlineReport
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("gate deadline --json printed non-JSON %q: %v", out, err)
			}
			if got.TimeoutSeconds != tc.seconds || got.StageBound != tc.bound || got.OtherStagesAllowance != "5m0s" {
				t.Errorf("gate deadline = %+v, want %ds with bound %s", got, tc.seconds, tc.bound)
			}
			mustContain(t, got.Note, tc.note)
			if tc.note == "" && got.Note != "" {
				t.Errorf("an unset bound must be reported silently, got note %q", got.Note)
			}
		})
	}

	// The human form states the same deadline, and positional arguments are refused.
	t.Setenv(gating.TestStageTimeoutEnv, "")
	out, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{"deadline"}) })
	if err != nil {
		t.Fatalf("gate deadline: %v", err)
	}
	mustContain(t, out, "Run Deadline: 8m0s (3m0s race stage bound + 5m0s for the other stages)")
	_, err = captureStdout(t, func() error { return dispatchCommand("gate", []string{"deadline", "extra"}) })
	mustErrContain(t, err, "no positional arguments")
}

// writeMarkers creates a repository root holding the named marker files.
func writeMarkers(t *testing.T, markers ...string) string {
	t.Helper()
	dir := t.TempDir()
	for _, marker := range markers {
		writeFixtureFile(t, dir, marker, "\n")
	}
	return dir
}

// `gate deadline --path` sizes the deadline to the repository `gate run --path` gates, so the
// pre-push hook bounds a mixed repository's gate by what that gate grants itself.
func TestGateDeadline_RepositorySized_3D(t *testing.T) {
	cargoAllowance := gating.OtherStagesAllowance + gating.CargoStagesAllowance
	cases := []struct {
		name      string
		markers   []string
		suites    int
		allowance time.Duration
	}{
		// Positive: go.mod and Cargo.lock reserve two suite bounds and the Cargo stages' bounds.
		{"go and cargo", []string{"go.mod", gating.CargoLockFile}, 2, cargoAllowance},
		// Negative: a Go repository reports the budget it had before Cargo support.
		{"go only", []string{"go.mod"}, 1, gating.OtherStagesAllowance},
		// Boundary: a Cargo-only repository runs one suite but still adds the Cargo stages.
		{"cargo only", []string{gating.CargoLockFile}, 1, cargoAllowance},
	}
	t.Setenv(gating.TestStageTimeoutEnv, "")
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := writeMarkers(t, tc.markers...)
			out, err := captureStdout(t, func() error {
				return dispatchCommand("gate", []string{"deadline", "--json", "--path=" + repo})
			})
			if err != nil {
				t.Fatalf("gate deadline --json --path: %v", err)
			}
			var got gateDeadlineReport
			if err := json.Unmarshal([]byte(out), &got); err != nil {
				t.Fatalf("gate deadline --json printed non-JSON %q: %v", out, err)
			}
			want := int64((time.Duration(tc.suites)*gating.TestStageTimeout + tc.allowance).Seconds())
			if got.TimeoutSeconds != want || got.TestSuites != tc.suites || got.OtherStagesAllowance != tc.allowance.String() {
				t.Errorf("gate deadline = %+v, want %ds over %d suites + %s", got, want, tc.suites, tc.allowance)
			}
		})
	}
}

// Positive: `gate run` over a mixed repository hands the pipeline the two-suite deadline.
func TestGateRun_Positive_MixedRepositoryRunDeadline(t *testing.T) {
	seen := stubPipeline(t)
	t.Setenv(gating.TestStageTimeoutEnv, "12m")
	repo := writeMarkers(t, "go.mod", gating.CargoLockFile)
	out, err := captureStdout(t, func() error { return dispatchCommand("gate", []string{"run", "--path=" + repo}) })
	if err != nil {
		t.Fatalf("gate run: %v", err)
	}
	want := 2*12*time.Minute + gating.OtherStagesAllowance + gating.CargoStagesAllowance
	mustLeave(t, *seen, want)
	mustContain(t, out, "Run Deadline: "+want.String(), "2 test suites at a 12m0s stage bound each")
}
