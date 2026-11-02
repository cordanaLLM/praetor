// SPDX-FileCopyrightText: 2026 Lusoris <lusoris@pm.me>
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import "strings"

// ReviewHoldStepName identifies a deliberately failed expensive job to the release helper.
const ReviewHoldStepName = "Hold heavy tier for review"

const reviewReadyCondition = "github.event_name == 'pull_request' && github.event.pull_request.draft != true"

const reviewHoldScript = `REASON="gate unreadable: no trusted helper on the default branch"
if [ -f .review-gate/scripts/ci_review_gate.py ]; then
  PYTHON="$(command -v python3 2>/dev/null || command -v python 2>/dev/null || true)"
  verdict="$(mktemp)"
  if [ -n "$PYTHON" ] && GITHUB_OUTPUT="$verdict" "$PYTHON" -I -B .review-gate/scripts/ci_review_gate.py gate --pr "$PR_NUMBER" --head "$PR_HEAD"; then
    if [ "$PLANNED_CLEAR" = true ] && grep -qx 'clear=true' "$verdict"; then
      echo "clear=true" >> "$GITHUB_OUTPUT"
      rm -f "$verdict"
      exit 0
    fi
    REASON="$(sed -n 's/^reason=//p' "$verdict")"
    if [ "$PLANNED_CLEAR" != true ]; then
      REASON="$REASON; planner held: rerun required for platform selection"
    fi
    REASON="${REASON:-gate unreadable: trusted helper produced no verdict}"
  else
    REASON="gate unreadable: trusted helper failed"
  fi
  rm -f "$verdict"
fi
echo "::error title=heavy tier held::$REASON"
echo "heavy tier held: $REASON" >> "$GITHUB_STEP_SUMMARY"
exit 1`

// DraftStepPosition accepts the canonical draft step first, or after a trusted checkout and
// fresh review hold. Both prefix steps exclude drafts, preserving the canonical annotation.
// Every other prefix is refused. -1 means the job has no proven draft barrier.
func DraftStepPosition(job *Job) int {
	index := 0
	if len(job.Steps) > 2 && trustedReviewCheckout(&job.Steps[0]) && safeReviewHold(&job.Steps[1]) {
		index = 2
	}
	if len(job.Steps) > index && DraftStepFault(&job.Steps[index]) == nil {
		return index
	}
	return -1
}

// trustedReviewCheckout permits only a sparse default-branch helper checkout before a draft barrier.
func trustedReviewCheckout(step *Step) bool {
	return step.If == reviewReadyCondition && step.Uses == "actions/checkout@v7" &&
		step.Run == "" && step.ContinueOnError == "" && len(step.Env.Content) == 0 &&
		trustedReviewInputs(step.With)
}

// trustedReviewInputs refuses PR refs, broad checkouts and persisted authorization.
func trustedReviewInputs(inputs map[string]any) bool {
	return len(inputs) == 5 && inputs["ref"] == "${{ github.event.repository.default_branch }}" &&
		inputs["persist-credentials"] == false && inputs["sparse-checkout"] == "scripts/ci_review_gate.py" &&
		inputs["sparse-checkout-cone-mode"] == false && inputs["path"] == ".review-gate"
}

// safeReviewHold proves fresh review reads use only the trusted helper and never run on drafts.
func safeReviewHold(step *Step) bool {
	return step.Name == ReviewHoldStepName && step.ID == "review" && step.Uses == "" &&
		step.ContinueOnError == "" && step.Shell == "bash" && step.WorkingDirectory == "" &&
		step.If == reviewReadyCondition && strings.TrimSpace(step.Run) == reviewHoldScript &&
		reviewHoldEnvironment(step)
}

// reviewHoldEnvironment binds review reads to the event's PR head and the planner's runner decision.
func reviewHoldEnvironment(step *Step) bool {
	return len(step.Env.Content) == 8 &&
		scalarValue(&step.Env, "GITHUB_TOKEN") == "${{ github.token }}" &&
		scalarValue(&step.Env, "PR_NUMBER") == "${{ github.event.pull_request.number }}" &&
		scalarValue(&step.Env, "PR_HEAD") == "${{ github.event.pull_request.head.sha }}" &&
		scalarValue(&step.Env, "PLANNED_CLEAR") == "${{ needs.review-gate.outputs.clear }}"
}
