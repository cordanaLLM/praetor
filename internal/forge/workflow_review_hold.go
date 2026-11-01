// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"regexp"
	"strings"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

const reviewDraftVerdict = "echo \"clear=false\" >> \"$GITHUB_OUTPUT\"\n" +
	"echo \"reason=draft pull request\" >> \"$GITHUB_OUTPUT\""

// reviewPlannerIdleOnDraft proves an advisory planner only emits the held draft verdict.
// Every later step must exclude drafts, so checkout and review API reads cannot run there.
// Other scripts or conditions are refused rather than interpreted as shell or expressions.
func reviewPlannerIdleOnDraft(job *workflowJob) bool {
	if job.Uses != "" || job.ContinueOnError != "true" || len(job.Steps) == 0 || len(job.Steps) > maxStepsPerJob {
		return false
	}
	if !reviewPlannerDraftStep(&job.Steps[0]) {
		return false
	}
	for i := 1; i < len(job.Steps) && i < maxStepsPerJob; i++ {
		if job.Steps[i].If != "github.event_name == 'pull_request' && github.event.pull_request.draft != true" {
			return false
		}
	}
	return true
}

// reviewPlannerDraftStep accepts only the inert draft verdict script and its explicit shell.
func reviewPlannerDraftStep(step *workflowStep) bool {
	return step.ID == "draft" && step.If == "github.event.pull_request.draft == true" &&
		step.Uses == "" && step.Shell == "bash" && step.WorkingDirectory == "" &&
		len(step.Env.Content) == 0 && strings.TrimSpace(step.Run) == reviewDraftVerdict
}

// Only the pilot's repository/opt-in selector may accompany !cancelled(): no needs output or
// status condition can make the job disappear when its gate fails. This is an allow-list,
// independent of the exact inert hold and draft-step proof in ghworkflow.
var reviewHoldRepositoryCondition = regexp.MustCompile(`^\(github\.repository == \(vars\.` +
	canonicalRepositoryVariable + ` \|\| '[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+'\) \|\| vars\.PRAETOR_FORK_PORTABILITY == 'enabled'\) && !cancelled\(\)$`)

func reviewHoldRunsAfterFailure(job *workflowJob) bool {
	if ghworkflow.DraftStepPosition(job) != 1 {
		return false
	}
	condition := withoutRenovateBranchSkip(job.If)
	return condition == "!cancelled()" || reviewHoldRepositoryCondition.MatchString(condition)
}
