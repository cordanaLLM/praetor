// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package forge

import (
	"regexp"

	"github.com/cordanaLLM/praetor/internal/ghworkflow"
)

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
