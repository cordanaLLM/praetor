// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import "strings"

// ReviewHoldStepName identifies a deliberately failed expensive job to the release helper.
const ReviewHoldStepName = "Hold heavy tier for review"

const reviewHoldScript = "echo \"::error title=heavy tier held::$REASON\"\n" +
	"echo \"heavy tier held: $REASON\" >> \"$GITHUB_STEP_SUMMARY\"\nexit 1"

// DraftStepPosition accepts the canonical draft step first, or after a strictly inert review
// hold. The hold either fails before work or is skipped; it cannot perform work on a draft.
// Every other prefix is refused. -1 means the job has no proven draft barrier.
func DraftStepPosition(job *Job) int {
	index := 0
	if len(job.Steps) > 1 && safeReviewHold(&job.Steps[0]) {
		index = 1
	}
	if len(job.Steps) > index && DraftStepFault(&job.Steps[index]) == nil {
		return index
	}
	return -1
}

func safeReviewHold(step *Step) bool {
	return step.Name == ReviewHoldStepName && step.Uses == "" && step.ContinueOnError == "" &&
		step.Shell == "bash" && step.WorkingDirectory == "" &&
		step.If == "needs.review-gate.outputs.clear != 'true'" &&
		strings.TrimSpace(step.Run) == reviewHoldScript && len(step.Env.Content) == 2 &&
		scalarValue(&step.Env, "REASON") == "${{ needs.review-gate.outputs.reason || 'gate unreadable: review-gate produced no verdict' }}"
}
