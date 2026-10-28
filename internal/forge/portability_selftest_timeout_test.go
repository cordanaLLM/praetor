// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// selftestTimeoutProblem requires a literal step bound below the enclosing job's bound.
func selftestTimeoutProblem(workflow string) string {
	spec, err := ghworkflow.Parse([]byte(workflow))
	if err != nil {
		return err.Error()
	}
	for _, job := range spec.Jobs {
		for _, step := range job.Steps {
			if step.Name != "Harness Self-Tests" {
				continue
			}
			minutes, stepErr := strconv.Atoi(step.TimeoutMinutes.Value)
			jobMinutes, jobErr := strconv.Atoi(job.TimeoutMinutes.Value)
			if stepErr != nil || jobErr != nil || minutes < 30 || minutes > 60 || minutes >= jobMinutes {
				return "Harness Self-Tests needs timeout-minutes in [30, 60] below the job timeout"
			}
			return ""
		}
	}
	return "no Harness Self-Tests step"
}

func TestPortability_SelftestTimeout(t *testing.T) {
	if problem := selftestTimeoutProblem(readPortability(t)); problem != "" {
		t.Fatal(problem)
	}
}

func TestPortability_SelftestTimeoutBoundariesAndPlantedDefects(t *testing.T) {
	for _, tc := range []struct {
		name, step, job string
		wantOK          bool
	}{
		{"lower boundary", "30", "180", true},
		{"upper boundary", "60", "180", true},
		{"missing", "", "180", false},
		{"too short", "29", "180", false},
		{"too long", "61", "180", false},
		{"equal to job", "40", "40", false},
		{"above job", "40", "39", false},
		{"missing job bound", "40", "", false},
		{"expression", "${{ inputs.timeout }}", "180", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			text := fmt.Sprintf("jobs:\n  test:\n    timeout-minutes: %s\n    steps:\n      - name: Harness Self-Tests\n        timeout-minutes: %s\n", tc.job, tc.step)
			if problem := selftestTimeoutProblem(text); (problem == "") != tc.wantOK {
				t.Fatalf("problem = %q, want accepted = %t", problem, tc.wantOK)
			}
		})
	}
	workflow := readPortability(t)
	planted := strings.Replace(workflow, "        timeout-minutes: 40\n", "", 1)
	if planted == workflow {
		t.Fatal("planted removal did not apply")
	}
	if selftestTimeoutProblem(planted) == "" {
		t.Fatal("workflow without the step timeout passed")
	}
}
