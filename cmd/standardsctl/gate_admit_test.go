// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package main

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/gating"
)

// admitLine is the `gate run` line the pre-push hook adoption renders runs (#648), built from the
// same constants the renderer uses.
const admitLine = gating.RepoRunCommand + " --" + gating.AdmitUnsupportedFlag

// Positive: the flag the generated pre-push hook passes is defined on the real `gate run` flag set
// and reaches the pipeline as RunOptions.AdmitUnsupported, beside --dry-run where both are given.
func TestGateRun_Positive_AdmitUnsupportedReachesPipeline(t *testing.T) {
	for _, line := range []string{admitLine, admitLine + " --dry-run"} {
		call := recordPipeline(t)
		ok, err := dispatchGateCommand(t, line)
		if !ok || err != nil {
			t.Fatalf("%q: dispatched=%v err=%v", line, ok, err)
		}
		if !call.called || !call.admitUnsupported || call.repoDir != "." || call.dryRun != strings.HasSuffix(line, "--dry-run") {
			t.Fatalf("%q: pipeline call %+v", line, *call)
		}
	}
}

// Negative: without the flag the pipeline is asked to refuse, as CI and the gatekeeper need, and a
// value that is no boolean is rejected before the pipeline starts.
func TestGateRun_Negative_AdmitUnsupportedIsOptIn(t *testing.T) {
	call := recordPipeline(t)
	if ok, err := dispatchGateCommand(t, gating.RepoRunCommand); !ok || err != nil || call.admitUnsupported {
		t.Fatalf("plain gate run: dispatched=%v err=%v call=%+v", ok, err, *call)
	}
	call = recordPipeline(t)
	_, err := dispatchGateCommand(t, admitLine+"=maybe")
	if err == nil || call.called {
		t.Fatalf("--%s=maybe: err=%v, pipeline called=%v", gating.AdmitUnsupportedFlag, err, call.called)
	}
}

// Boundary: the report ends with the admission note only for a run the pipeline admitted
// unverified, never for a signed or a dry run, so an admitted push never reads as verified.
func TestPrintGatingReport_Boundary_AdmittedUnverifiedNote(t *testing.T) {
	note := "Admitted without an Exit-0 receipt (--" + gating.AdmitUnsupportedFlag + ")"
	cases := map[string]struct {
		rep  *gating.PipelineReport
		want bool
	}{
		"admitted unverified": {rep: &gating.PipelineReport{Status: gating.StatusAdmitted, AdmittedUnverified: true}, want: true},
		"dry run":             {rep: &gating.PipelineReport{Status: gating.StatusAdmitted, DryRun: true}},
		"rejected":            {rep: &gating.PipelineReport{Status: gating.StatusRejected}},
	}
	for name, tc := range cases {
		out, err := captureStdout(t, func() error { printGatingReport(tc.rep); return nil })
		if err != nil {
			t.Fatalf("%s: printGatingReport: %v", name, err)
		}
		if strings.Contains(out, note) != tc.want {
			t.Errorf("%s: admission note printed = %v, want %v:\n%s", name, !tc.want, tc.want, out)
		}
	}
}
