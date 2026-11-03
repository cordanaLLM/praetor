// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"maps"
	"strconv"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

// aggregateResultStart skips a canonical leading hold or refuses an absent required barrier.
func aggregateResultStart(spec *workflowSpec, job *workflowJob) (int, bool) {
	if canonicalReviewHoldStep(&job.Steps[0]) {
		return 1, true
	}
	return 0, !aggregateNeedsReviewHold(spec, job)
}

// canonicalReviewHoldStep accepts only the shared renderer's barrier, without execution overrides.
func canonicalReviewHoldStep(step *workflowStep) bool {
	spec, err := ghworkflow.Parse([]byte("jobs:\n  hold:\n    steps:\n" + ghworkflow.RenderReviewHoldStep(false)))
	if err != nil {
		return false
	}
	want := spec.Jobs["hold"].Steps[0]
	return reviewHoldExecutionMatches(step, &want) && reviewHoldOptionsMatch(step, &want)
}

// reviewHoldExecutionMatches compares every field that selects or runs the canonical barrier.
func reviewHoldExecutionMatches(step, want *workflowStep) bool {
	return step.Name == want.Name && step.ID == want.ID && step.If == want.If &&
		step.Uses == "" && step.Run == want.Run && step.Shell == want.Shell && step.ContinueOnError == ""
}

// reviewHoldOptionsMatch refuses added overrides, enforces the required timeout and compares the renderer's environment values.
func reviewHoldOptionsMatch(step, want *workflowStep) bool {
	wantTimeout := strconv.Itoa(ghworkflow.ReviewHoldStepTimeoutMinutes)
	if step.WorkingDirectory != "" || len(step.With) != 0 ||
		step.TimeoutMinutes.Kind == 0 ||
		step.TimeoutMinutes.Value != wantTimeout ||
		want.TimeoutMinutes.Value != wantTimeout {
		return false
	}
	var gotEnv, wantEnv map[string]string
	if err := step.Env.Decode(&gotEnv); err != nil {
		return false
	}
	if err := want.Env.Decode(&wantEnv); err != nil {
		return false
	}
	return maps.Equal(gotEnv, wantEnv)
}

// aggregateNeedsReviewHold reports whether a directly needed job declares a review barrier.
func aggregateNeedsReviewHold(spec *workflowSpec, job *workflowJob) bool {
	needs := job.NeedIDs()
	for i := 0; i < len(needs) && i < maxJobsPerFile; i++ {
		need := spec.Jobs[needs[i]]
		for j := 0; j < len(need.Steps) && j < ghworkflow.MaxStepsPerJob; j++ {
			if need.Steps[j].ID == "review-hold" {
				return true
			}
		}
	}
	return false
}
