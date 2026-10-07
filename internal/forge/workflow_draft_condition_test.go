// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"slices"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// draftWorkflow is a one-job pull request workflow in the hosted gate trigger shape whose job
// carries jobIf (empty for none) and runs steps.
func draftWorkflow(jobIf, steps string) []byte {
	job := "    name: Gate\n"
	if jobIf != "" {
		job += "    if: " + jobIf + "\n"
	}
	return []byte(ghworkflow.HostedGateOn + "jobs:\n  gate:\n" + job + "    runs-on: ubuntu-latest\n    steps:\n" + steps)
}

// gateRunStep is a gate step carrying the not-a-draft condition.
const gateRunStep = "      - name: Run the gate" + ghworkflow.HostedGateStepIf + "\n        run: make gate\n"

// Negative: a job-level draft skip makes the job conditional, bare or as one expression, even in
// a workflow that runs again on ready_for_review: GitHub reports the skipped draft as successful
// and a required check accepts it, so requiring the job would let a draft marked ready merge
// before its gate ran. Positive: the hosted gate shape, which refuses a draft in a step and keeps
// the job unconditional, stays required. Boundary: step conditions alone never make a job
// conditional.
func TestDraftConditionsAndRequiredContexts(t *testing.T) {
	cases := []struct {
		name     string
		doc      []byte
		required bool
	}{
		{"negative job draft skip", draftWorkflow(ghworkflow.HostedGateNotDraft, "      - run: make gate\n"), false},
		{"negative wrapped job draft skip", draftWorkflow("${{ "+ghworkflow.HostedGateNotDraft+" }}", "      - run: make gate\n"), false},
		{"positive hosted gate shape", draftWorkflow("", ghworkflow.HostedGateDraftStep+gateRunStep), true},
		{"boundary every step conditional", draftWorkflow("", gateRunStep), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !strings.Contains(string(tc.doc), "ready_for_review") {
				t.Fatal("the fixture does not run again on ready_for_review")
			}
			contexts, err := workflowContextsIn(tc.doc, "")
			if err != nil {
				t.Fatalf("workflowContextsIn: %v", err)
			}
			if got := slices.Equal(contexts, []string{"Gate"}); got != tc.required {
				t.Fatalf("required contexts = %v, want Gate required = %v", contexts, tc.required)
			}
		})
	}
}
