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

const (
	selftestRegressionCommand = `"$PYTHON" -u -B scripts/test_portability_selftest.py`
	selftestDriverCommand     = `"$PYTHON" -u -B scripts/portability_selftest.py`
)

// selftestProgressProblem keeps the timeout regression on every platform before real suites.
func selftestProgressProblem(workflow string) string {
	spec, err := ghworkflow.Parse([]byte(workflow))
	if err != nil {
		return err.Error()
	}
	for _, job := range spec.Jobs {
		for _, step := range job.Steps {
			if step.Name == "Harness Self-Tests" {
				regression := strings.Index(step.Run, selftestRegressionCommand)
				driver := strings.Index(step.Run, selftestDriverCommand)
				if regression < 0 || driver < regression {
					return "run timeout regressions before the driver, both unbuffered"
				}
				return ""
			}
		}
	}
	return "no Harness Self-Tests step"
}

func TestPortability_SelftestRegressionsAndDriverRunUnbuffered(t *testing.T) {
	workflow := readPortability(t)
	if problem := selftestProgressProblem(workflow); problem != "" {
		t.Fatal(problem)
	}
	for _, command := range []string{selftestRegressionCommand, selftestDriverCommand} {
		planted := strings.Replace(workflow, command, strings.Replace(command, " -u", "", 1), 1)
		if planted == workflow || selftestProgressProblem(planted) == "" {
			t.Fatalf("planted buffered or missing command accepted: %s", command)
		}
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
