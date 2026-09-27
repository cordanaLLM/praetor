// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// docsReferencesCommand is what the portability legs run for the documentation reference
// gate (BUG-992); the binary is the one the job builds, since `make` is absent on Windows.
const docsReferencesCommand = "docs references --path=."

// docsReferencesGap names why a job does not run the documentation reference gate on every
// leg after building the binary it runs, or returns "" when it does.
func docsReferencesGap(job workflowJob) string {
	if advisoryJob(job.ContinueOnError) {
		return "job is advisory"
	}
	built := false
	for i := 0; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		step := job.Steps[i]
		if strings.Contains(step.Run, "go build -o \"bin/praetorctl") && runsOnEveryLeg(step) {
			built = true
		}
		if !strings.Contains(step.Run, docsReferencesCommand) {
			continue
		}
		switch {
		case !built:
			return "documentation references run before an unconditional praetorctl build"
		case !runsOnEveryLeg(step):
			return "documentation references step is conditional: " + step.If
		}
		return ""
	}
	return "no step runs " + docsReferencesCommand
}

// HISS-21 evidence for the documentation reference gate. Positive: the real harness job runs
// it on every leg. Negative and boundary: a missing step, one ahead of the build, one guarded
// by an OS condition, or an advisory job.
func TestPortabilityRunsDocumentationReferencesOnEveryLeg(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	var spec workflowSpec
	if err := yaml.Unmarshal(workflows["portability.yml"], &spec); err != nil {
		t.Fatalf("parse portability.yml: %v", err)
	}
	if gap := docsReferencesGap(spec.Jobs["harness"]); gap != "" {
		t.Fatalf("portability harness: %s", gap)
	}
	build := workflowStep{Run: `go build -o "bin/praetorctl$(go env GOEXE)" ./cmd/standardsctl`, If: "${{ !cancelled() }}"}
	check := workflowStep{Run: `"bin/praetorctl$(go env GOEXE)" ` + docsReferencesCommand, If: "${{ !cancelled() }}"}
	cases := []struct {
		name string
		job  workflowJob
		want string
	}{
		{"positive", workflowJob{Steps: []workflowStep{build, check}}, ""},
		{"negative missing", workflowJob{Steps: []workflowStep{build}}, "no step runs"},
		{"boundary order", workflowJob{Steps: []workflowStep{check, build}}, "before an unconditional praetorctl build"},
		{"boundary os guard", workflowJob{Steps: []workflowStep{build, {Run: check.Run, If: "runner.os != 'Windows'"}}}, "conditional"},
		{"boundary advisory", workflowJob{ContinueOnError: "true", Steps: []workflowStep{build, check}}, "advisory"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := docsReferencesGap(tc.job)
			if (tc.want == "") != (got == "") || !strings.Contains(got, tc.want) {
				t.Fatalf("gap = %q, want containing %q", got, tc.want)
			}
		})
	}
}
