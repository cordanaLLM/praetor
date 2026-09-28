// SPDX-License-Identifier: EUPL-1.2
// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@proton.me>

package main

import (
	"errors"
	"flag"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// BUG-790: a help probe of `agent run praetor-gatekeeper` must print usage and exit 0 the way
// `gate --help` does, and must never reach the gating pipeline, which creates a worktree and a
// branch, runs the race stage and mints a receipt.

// runAgentProbe runs `agent <args>` against a stubbed gatekeeper pipeline and returns the
// output, how many times the pipeline ran and the error.
func runAgentProbe(t *testing.T, args ...string) (out string, calls int32, err error) {
	t.Helper()
	counter := stubGatekeeperPipeline(t, &gating.PipelineReport{Status: gating.StatusAdmitted}, nil)
	out, err = captureStdout(t, func() error { return dispatchCommand("agent", args) })
	return out, counter.Load(), err
}

// assertHelpAnswered fails unless the probe printed the agent usage, returned flag.ErrHelp
// (main exits 0 on it) and left the pipeline unstarted.
func assertHelpAnswered(t *testing.T, args []string, out string, calls int32, err error) {
	t.Helper()
	if !errors.Is(err, flag.ErrHelp) {
		t.Fatalf("agent %v: err %v, want flag.ErrHelp", args, err)
	}
	if calls != 0 {
		t.Fatalf("agent %v: a help probe ran the gating pipeline %d time(s)", args, calls)
	}
	mustContain(t, out, agentUsage)
	if strings.Contains(out, "[Agent Dispatch]") {
		t.Fatalf("agent %v: a help probe activated a helper: %s", args, out)
	}
}

// Positive: every help spelling after the gatekeeper persona prints usage, exits 0 and runs
// nothing.
func TestAgentHelp_Positive_GatekeeperHelpProbePrintsUsageAndRunsNothing(t *testing.T) {
	for _, token := range []string{"--help", "-h", "help"} {
		args := []string{"run", "praetor-gatekeeper", token}
		out, calls, err := runAgentProbe(t, args...)
		assertHelpAnswered(t, args, out, calls, err)
	}
}

// Negative: an argument that is not a help token is still refused, and still runs nothing;
// without any argument the gatekeeper runs the pipeline exactly once.
func TestAgentHelp_Negative_NonHelpArgumentIsRefusedAndBareRunStillGates(t *testing.T) {
	for _, extra := range []string{"--json", "--helpful", "--help=true"} {
		out, calls, err := runAgentProbe(t, "run", "praetor-gatekeeper", extra)
		mustErrContain(t, err, "unexpected extra argument")
		if errors.Is(err, flag.ErrHelp) || calls != 0 || strings.Contains(out, agentUsage) {
			t.Fatalf("agent run praetor-gatekeeper %s: err %v, pipeline calls %d, output %q; want a refusal and no run",
				extra, err, calls, out)
		}
	}
	if _, calls, err := runAgentProbe(t, "run", "praetor-gatekeeper"); err != nil || calls != 1 {
		t.Fatalf("agent run praetor-gatekeeper: err %v, pipeline calls %d; want nil, 1", err, calls)
	}
}

// Boundary: a help token at any position answers the probe, whether it stands in the
// subcommand slot, the persona slot, before the persona or after another argument.
func TestAgentHelp_Boundary_HelpTokenAtAnyPositionWins(t *testing.T) {
	cases := [][]string{
		{"--help"},
		{"help"},
		{"list", "-h"},
		{"run", "--help"},
		{"run", "-h", "praetor-gatekeeper"},
		{"run", "praetor_gatekeeper", "--json", "--help"},
	}
	for _, args := range cases {
		out, calls, err := runAgentProbe(t, args...)
		assertHelpAnswered(t, args, out, calls, err)
	}
}
