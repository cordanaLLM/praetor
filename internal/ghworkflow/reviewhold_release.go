// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"strings"
)

const (
	// reviewHoldReleaseReviewEvent selects cancellable, per-PR review runs.
	reviewHoldReleaseReviewEvent = "github.event_name == 'pull_request_review' || github.event_name == 'pull_request_review_comment'"
	// reviewHoldReleaseScript locates the newest PR or review run on the current head.
	// Current-attempt jobs supply their check-run annotation IDs; older attempts
	// must not cause an unrelated failure to be rerun indefinitely.
	reviewHoldReleaseScript = reviewHoldLibrary + `RELEASE_TITLE='Review hold release read failed'
trap 'HOLD_TITLE="$RELEASE_TITLE"; review_error' ERR
# Keep errexit active inside each scan; a conditional function call disables it.
review_release_pr() (
  set -Ee
  # Only the parent owns cleanup of the directory shared by these serial scans.
  trap - EXIT
  trap 'HOLD_TITLE="$RELEASE_TITLE"; HOLD_CAUSE="pull request $PR_NUMBER: $HOLD_CAUSE"; review_error' ERR
  HOLD_CAUSE="pull request metadata read failed: index $pr_index"
  PR_NUMBER=$(jq --binary -er --argjson i "$pr_index" '.[$i].number' "$prs")
  HEAD_SHA=$(jq --binary -er --argjson i "$pr_index" '.[$i].head.sha' "$prs")
  HOLD_CAUSE="workflow run read failed: pull request $PR_NUMBER"
  runs=$(review_pages "repos/$GITHUB_REPOSITORY/actions/workflows/portability.yml/runs?head_sha=$HEAD_SHA" workflow_runs)
  run="$REVIEW_TMP/run.json"
  # A shared fork SHA cannot identify a PR; require an explicit run association.
  jq --binary -c --arg head "$HEAD_SHA" --argjson number "$PR_NUMBER" '
    [.[] | select(.head_sha == $head and (.event == "pull_request" or .event == "pull_request_review")) |
      select(any(.pull_requests[]; .number == $number))] |
    max_by(.run_number) // {}' "$runs" > "$run"
  # Busy runs keep their hold annotations. Every scheduled pass rescans open PRs,
  # so defer them until completion without cancelling another active leg.
  if [[ $(jq --binary -r '.status' "$run") != completed || $(jq --binary -r '.conclusion' "$run") != failure ]]; then exit 0; fi
  run_id=$(jq --binary -er '.id' "$run")
  attempt=$(jq --binary -er '.run_attempt' "$run")
  HOLD_CAUSE="job read failed: run $run_id attempt $attempt"
  jobs=$(review_pages "repos/$GITHUB_REPOSITORY/actions/runs/$run_id/attempts/$attempt/jobs" jobs)
  failed="$REVIEW_TMP/failed.json"
  jq --binary -c '[.[] | select(.conclusion == "failure")]' "$jobs" > "$failed"
  failed_count=$(jq --binary 'length' "$failed")
  held=false
  for ((job_index=0; job_index<failed_count && job_index<2000; job_index++)); do
    check_url=$(jq --binary -er --argjson i "$job_index" '.[$i].check_run_url' "$failed")
    check_id="${check_url##*/}"
    [[ "$check_id" =~ ^[0-9]+$ ]]
    HOLD_CAUSE="annotation read failed: check $check_id"
    annotations=$(review_pages "repos/$GITHUB_REPOSITORY/check-runs/$check_id/annotations" '')
    if [[ $(jq --binary --arg title "$HOLD_TITLE" 'any(.[]; .title == $title and .annotation_level == "failure")' "$annotations") == true ]]; then
      held=true
    fi
  done
  if [[ "$held" != true ]]; then exit 0; fi
  review_count
  printf 'PR %s: %s\n' "$PR_NUMBER" "$HOLD_CAUSE" >> "$GITHUB_STEP_SUMMARY"
  if ((REVIEW_THREADS > 0 || REVIEW_PENDING > 0)); then exit 0; fi
  # Revalidate immediately before mutation: another release/manual rerun or a
  # head update during the recount must not rerun an obsolete attempt.
  HOLD_CAUSE="run revalidation failed: $run_id"
  current="$REVIEW_TMP/current-run.json"
  gh api "repos/$GITHUB_REPOSITORY/actions/runs/$run_id" > "$current"
  if [[ $(jq --binary -r '.status' "$current") != completed || $(jq --binary -r '.run_attempt' "$current") != "$attempt" ]]; then exit 0; fi
  HOLD_CAUSE="head revalidation failed: $PR_NUMBER"
  current_pr="$REVIEW_TMP/current-pr.json"
  gh api "repos/$GITHUB_REPOSITORY/pulls/$PR_NUMBER" > "$current_pr"
  if [[ $(jq --binary -r '.head.sha' "$current_pr") != "$HEAD_SHA" || $(jq --binary -r '.state' "$current_pr") != open ]]; then exit 0; fi
  # Recount after the run/head reads: a new thread or bot hold must defer release.
  review_count
  if ((REVIEW_THREADS > 0 || REVIEW_PENDING > 0)); then exit 0; fi
  HOLD_CAUSE="rerun failed: $run_id"
  gh run rerun "$run_id" --repo "$GITHUB_REPOSITORY"
  printf 'Released PR %s: run %s attempt %s\n' "$PR_NUMBER" "$run_id" "$attempt" >> "$GITHUB_STEP_SUMMARY"
)
HOLD_CAUSE='open pull request read failed'
if [[ -n "${REVIEW_PR_NUMBER:-}" ]]; then
  HOLD_CAUSE='event pull request number invalid'
  [[ "$REVIEW_PR_NUMBER" =~ ^[1-9][0-9]*$ ]]
  HOLD_CAUSE="event pull request read failed: $REVIEW_PR_NUMBER"
  event_pr="$REVIEW_TMP/event-pr.json"
  gh api "repos/$GITHUB_REPOSITORY/pulls/$REVIEW_PR_NUMBER" > "$event_pr"
  prs="$REVIEW_TMP/prs.json"
  jq --binary -ce '[select(.state == "open")]' "$event_pr" > "$prs"
else
  prs=$(review_pages "repos/$GITHUB_REPOSITORY/pulls?state=open&sort=created&direction=asc" '')
fi
pr_count=$(jq --binary 'length' "$prs")
read_failed=0
for ((pr_index=0; pr_index<pr_count && pr_index<2000; pr_index++)); do
  # The child owns its fatal trap; the parent records failure and keeps scanning.
  trap - ERR
  set +e
  review_release_pr
  pr_status=$?
  set -e
  trap 'HOLD_TITLE="$RELEASE_TITLE"; review_error' ERR
  if ((pr_status != 0)); then read_failed=1; fi
done
exit "$read_failed"
`
)

// ReviewHoldReleaseCondition renders the release job if: condition restricting execution
// to canonical repositories and relevant review-bot check_run and status events.
// GitHub documents contains(): "This function is not case sensitive."
// https://docs.github.com/en/actions/reference/workflows-and-actions/expressions#contains
func ReviewHoldReleaseCondition() string {
	bots := strings.Split(ReviewHoldBots, "|")
	var checkParts, statusParts []string
	for _, bot := range bots {
		checkParts = append(checkParts,
			"contains(github.event.check_run.name, '"+bot+"') || contains(github.event.check_run.app.slug, '"+bot+"')",
		)
		statusParts = append(statusParts,
			"contains(github.event.context, '"+bot+"') || contains(github.event.sender.login, '"+bot+"')",
		)
	}
	return "github.repository == (vars.PRAETOR_CANONICAL_REPOSITORY || 'cordanaLLM/praetor') && " +
		"(github.event_name != 'check_run' || (" + strings.Join(checkParts, " || ") + ")) && " +
		"(github.event_name != 'status' || (github.event.state != 'pending' && (" + strings.Join(statusParts, " || ") + ")))"
}

// reviewHoldReleaseHeader coalesces reviews per PR and serializes all release jobs.
// The token stays in the trusted inline script; no checkout or PR-controlled code runs.
var reviewHoldReleaseHeader = `# SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
#
# SPDX-License-Identifier: EUPL-1.2
# Rendered by internal/ghworkflow.RenderReviewHoldReleaseWorkflow.
# Review events process only their PR; newer reviews supersede that PR's reviews.
# Scan events process all open PRs; the scan continues after a per-PR read failure.
# All release jobs share a non-cancelling concurrency group, including full scans.
name: Review hold release
on:
  pull_request_review:
    types: [submitted, dismissed]
  pull_request_review_comment:
    types: [created, edited, deleted]
  check_run:
    types: [completed]
  status:
  schedule:
    - cron: '*/15 * * * *'
  workflow_dispatch:
permissions:
  actions: write
  pull-requests: read
  checks: read
  statuses: read
concurrency:
  group: ${{ (` + reviewHoldReleaseReviewEvent + `) && format('review-hold-release-pr-{0}', github.event.pull_request.number) || 'review-hold-release-scan' }}
  cancel-in-progress: ${{ ` + reviewHoldReleaseReviewEvent + ` }}
jobs:
  release:
    if: ` + ReviewHoldReleaseCondition() + `
    concurrency:
      group: review-hold-release-mutation
      cancel-in-progress: false
    runs-on: ubuntu-26.04
    timeout-minutes: 15
    steps:
      - name: Release answered review holds
        shell: bash
        env:
          GH_TOKEN: ${{ github.token }}
          REVIEW_PR_NUMBER: ${{ github.event.pull_request.number || '' }}
        run: |
`

// RenderReviewHoldReleaseWorkflow renders the trusted scheduler with the barrier's counter.
func RenderReviewHoldReleaseWorkflow() string {
	return reviewHoldReleaseHeader + indentReviewScript(reviewHoldReleaseScript)
}
