// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"strings"
	"testing"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
	"github.com/cordanaLLM/praetor/internal/util"
)

func reviewTierProblem(spec ghworkflow.Spec, id string) string {
	job := spec.Jobs[id]
	if len(job.NeedIDs()) != 1 || job.NeedIDs()[0] != "review-gate" {
		return "heavy job must need review-gate"
	}
	if !strings.Contains(job.If, "!cancelled()") {
		return "heavy job must report failure even when review-gate fails"
	}
	if !strings.Contains(job.RunsOn.Value, "needs.review-gate.outputs.clear == 'true'") ||
		!strings.Contains(job.RunsOn.Value, "'ubuntu-26.04'") {
		return "held job must use Ubuntu"
	}
	if ghworkflow.DraftStepPosition(&job) != 1 {
		return "first step must fail visibly when held"
	}
	for _, step := range job.Steps[1:] {
		if strings.Contains(step.If, "!cancelled()") &&
			!strings.Contains(step.If, "needs.review-gate.outputs.clear == 'true'") {
			return "failure-continuing step must require review clearance"
		}
	}
	return ""
}

func TestWorkflowHeavyReviewGate(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	for file, heavy := range map[string][]string{
		"ci.yml": {"verify", "docs-presets"}, "portability.yml": {"harness"},
	} {
		for _, problem := range reviewGateFileProblems(file, workflows[file], heavy) {
			t.Errorf("%s: %s", file, problem)
		}
	}
}

// reviewGateFileProblems lists what is missing from one workflow's review gate: a complete gate job,
// review clearance on every heavy job, the non-PR clear default, and a fast configuration job that
// does not wait for review.
func reviewGateFileProblems(file string, data []byte, heavy []string) []string {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	gate, exists := spec.Jobs["review-gate"]
	if !exists || len(gate.Steps) < 3 {
		problems = append(problems, "no complete review-gate")
	}
	for _, id := range heavy {
		if problem := reviewTierProblem(spec, id); problem != "" {
			problems = append(problems, id+": "+problem)
		}
	}
	if len(gate.Steps) > 0 && !strings.Contains(string(data), "github.event_name != 'pull_request' && 'true'") {
		problems = append(problems, "non-PR events must clear the gate")
	}
	config := spec.Jobs["config-validation"]
	if file == "ci.yml" && len(config.NeedIDs()) != 0 {
		problems = append(problems, "fast configuration validation waits for review")
	}
	return problems
}

func reviewReleaseProblem(data []byte) string {
	spec, err := ghworkflow.Parse(data)
	if err != nil {
		return err.Error()
	}
	if problem := reviewReleaseTriggersProblem(spec); problem != "" {
		return problem
	}
	var scopes map[string]string
	if err := spec.Permissions.Decode(&scopes); err != nil || len(scopes) != 3 ||
		scopes["actions"] != "write" || scopes["contents"] != "read" || scopes["pull-requests"] != "read" {
		return "release permissions differ"
	}
	return reviewReleaseStepsProblem(spec.Jobs["release"])
}

func reviewReleaseTriggersProblem(spec ghworkflow.Spec) string {
	for _, event := range []string{"schedule", "pull_request_review", "workflow_dispatch"} {
		if !hasTrigger(&spec.On, event) {
			return "missing release trigger: " + event
		}
	}
	schedule := util.YAMLMappingValue(&spec.On, "schedule")
	if len(schedule.Content) != 1 {
		return "release schedule differs"
	}
	cron := util.YAMLMappingValue(schedule.Content[0], "cron")
	if cron == nil || cron.Value != "*/10 * * * *" {
		return "release cadence differs"
	}
	return ""
}

func reviewReleaseStepsProblem(job ghworkflow.Job) string {
	if len(job.Steps) != 2 || job.Steps[0].With["ref"] != "${{ github.event.repository.default_branch }}" ||
		job.Steps[0].With["persist-credentials"] != false {
		return "release helper must come from trusted default branch"
	}
	if job.Steps[1].Run != "python3 -B scripts/ci_review_gate.py release" {
		return "release must use tested helper"
	}
	return ""
}

func TestWorkflowReviewReleaseContractAndPlantedDefects(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	original := string(workflows["release-heavy-tier.yml"])
	if problem := reviewReleaseProblem([]byte(original)); problem != "" {
		t.Fatal(problem)
	}
	for _, pair := range [][2]string{
		{"pull_request_review:", "pull_request_review_comment:"},
		{"*/10 * * * *", "*/30 * * * *"},
		{"actions: write", "actions: read"},
		{"github.event.repository.default_branch", "github.event.pull_request.head.sha"},
		{"ci_review_gate.py release", "ci_review_gate.py gate"},
	} {
		planted := strings.Replace(original, pair[0], pair[1], 1)
		if planted == original || reviewReleaseProblem([]byte(planted)) == "" {
			t.Errorf("release contract accepted mutation %q", pair[0])
		}
	}
}

func TestWorkflowReviewHoldPlantedDefects(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	original := string(workflows["portability.yml"])
	for name, text := range map[string]string{
		"missing need":   strings.Replace(original, "    needs: review-gate\n", "", 1),
		"spent platform": strings.Replace(original, "needs.review-gate.outputs.clear == 'true' && matrix.os || 'ubuntu-26.04'", "matrix.os", 1),
		"skipped hold":   strings.Replace(original, "if: needs.review-gate.outputs.clear != 'true'", "if: false", 1),
		"passing hold":   strings.Replace(original, "          exit 1\n\n      # The hosted gate's draft step", "          exit 0\n\n      # The hosted gate's draft step", 1),
		"continued work": strings.ReplaceAll(original, "needs.review-gate.outputs.clear == 'true' && github.event.pull_request.draft", "github.event.pull_request.draft"),
	} {
		if text == original {
			t.Fatalf("%s: mutation did not apply", name)
		}
		spec, err := ghworkflow.Parse([]byte(text))
		if err != nil || reviewTierProblem(spec, "harness") == "" {
			t.Errorf("%s: mutation accepted or unparsable: %v", name, err)
		}
	}
}

func TestWorkflowReviewHoldDraftProof(t *testing.T) {
	workflows, _ := engineWorkflows(t)
	spec, err := ghworkflow.Parse(workflows["ci.yml"])
	if err != nil {
		t.Fatal(err)
	}
	for _, condition := range []string{"!cancelled()", renovateBranchSkip + " && !cancelled()"} {
		job := spec.Jobs["verify"]
		job.If = condition
		if !reviewHoldRunsAfterFailure(&job) {
			t.Fatalf("proven review/draft barrier refused: %s", condition)
		}
		for _, suffix := range []string{" && !failure()", " && needs.review-gate.result == 'success'", " || false"} {
			job.If = condition + suffix
			if reviewHoldRunsAfterFailure(&job) {
				t.Errorf("conditional failure reporting accepted: %s", job.If)
			}
		}
	}
	job := spec.Jobs["verify"]
	for _, script := range []string{"exit 0", "make verify-all\nexit 1", job.Steps[0].Run + "\necho work"} {
		job.Steps[0].Run = script
		if reviewHoldRunsAfterFailure(&job) {
			t.Errorf("non-inert hold accepted: %s", script)
		}
	}
}
