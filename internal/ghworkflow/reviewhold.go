// SPDX-FileCopyrightText: 2026 lusoris <lusoris@pm.me>
//
// SPDX-License-Identifier: EUPL-1.2

package ghworkflow

import (
	"strconv"
	"strings"
)

const (
	// ReviewHoldTitle identifies a refusal that the release workflow may reconsider.
	ReviewHoldTitle = "Gate held: review open"
	// ReviewHoldBots is the case-insensitive bot pattern rendered into both scripts.
	// Match check names/app slugs and status contexts/creator logins, including
	// CodeRabbit, Codacy Static Code Analysis and sourcery-ai.
	ReviewHoldBots = "coderabbit|codacy|sourcery"
	// ReviewHoldPollSeconds spaces review recounts while the barrier is held.
	ReviewHoldPollSeconds = 30
	// ReviewHoldWaitMinutes bounds the polling window before retaining a review hold.
	ReviewHoldWaitMinutes = 10
	// ReviewHoldStepTimeoutMinutes bounds the "Stop while review is open" step.
	// A wall-clock deadline limits polling to 10 minutes and at most 20 retries.
	// Each recount retains 20 pages * 3 collections = 60 requests maximum.
	// Allow the final recount 60 * 4 s = 240 s, plus 60 s for parsing/cleanup:
	// 600 + 240 + 60 = 900 s = 15 minutes. The 4 s/request is a sizing budget,
	// not a gh HTTP timeout; the runner timeout also bounds stalled requests.
	ReviewHoldStepTimeoutMinutes = 15
	// ReviewHoldQuery is the one paginated thread query used by the barrier and release.
	ReviewHoldQuery = `query($owner: String!, $name: String!, $number: Int!, $cursor: String) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      headRefOid
      reviewThreads(first: 100, after: $cursor) {
        nodes { isResolved }
        pageInfo { hasNextPage endCursor }
      }
    }
  }
}`
	// ReviewHoldCondition runs the barrier on pull_request and pull_request_review events
	// for non-draft PRs. pull_request_review lets a resolved review thread trigger a fresh
	// evaluation without waiting for the review-hold-release.yml scheduler.
	ReviewHoldCondition = "(github.event_name == 'pull_request' || github.event_name == 'pull_request_review') && " + HostedGateNotDraft
	// ReviewHoldClear guards later steps even when they deliberately run after failure.
	// Non-PR events do not count reviews; PR reads must explicitly produce clear=true.
	ReviewHoldClear = "(github.event_name != 'pull_request' && github.event_name != 'pull_request_review' || steps.review-hold.outputs.clear == 'true')"
	// HostedGateLegReady guards heavy legs after review clearance, including steps run after failure.
	HostedGateLegReady = ReviewHoldClear + " && !cancelled() && " + HostedGateNotDraft
	// ReviewCheckMessage is the annotation and summary body for the review-check step.
	ReviewCheckMessage = "Heavy tier held while review threads are unresolved or bot reviews are pending. The gate runs automatically when they clear."
	// ReviewCheckCondition fires the review-check step when the gate did not succeed on a non-draft PR.
	ReviewCheckCondition = "needs.review-gate.result != 'success' && " + HostedGateNotDraft
	// ReviewHoldGateCheck guards heavy matrix legs in the gate-job pattern where a
	// dedicated review-gate job runs the hold and the matrix legs read its result.
	// Steps with !cancelled() carry this so they do not run after the gate-check failure.
	ReviewHoldGateCheck = "steps.review-check.outcome != 'failure'"
	// HostedGateGateLegReady guards heavy legs in the gate-job pattern.
	HostedGateGateLegReady = "!cancelled() && " + HostedGateNotDraft + " && " + ReviewHoldGateCheck
	// reviewHoldLibrary is trusted inline Bash: release never checks out pull request code.
	// Every API collection has a 20-page scalar bound; a full final page fails closed.
	reviewHoldLibrary = `set -Eeuo pipefail
REVIEW_BOTS='` + ReviewHoldBots + `'
REVIEW_QUERY=$(cat <<'GRAPHQL'
` + ReviewHoldQuery + `
GRAPHQL
)
HOLD_TITLE='` + ReviewHoldTitle + `'
HOLD_CAUSE='review read failed'
review_error() {
  local message="$HOLD_CAUSE" title="$HOLD_TITLE" cr=$'\r' lf=$'\n'
  # Bash 3.2 preserves quotes inside substitution patterns and replacements.
  title=${title//%/%25}
  title=${title//:/%3A}
  title=${title//,/%2C}
  title=${title//$cr/%0D}
  title=${title//$lf/%0A}
  message=${message//%/%25}
  message=${message//$cr/%0D}
  message=${message//$lf/%0A}
  echo "::error title=$title::$message" >&2
  printf '%s\n' "$HOLD_TITLE: $HOLD_CAUSE" >> "$GITHUB_STEP_SUMMARY"
  exit 1
}
trap review_error ERR
HOLD_CAUSE='temporary directory creation failed'
REVIEW_TMP=$(mktemp -d "${RUNNER_TEMP:-/tmp}/review-hold.XXXXXXXXXX")
trap 'rm -rf -- "$REVIEW_TMP"' EXIT
# Return a validated REST collection's file path. API data stays off argv.
review_pages() {
  local endpoint="$1" key="$2" dir count page
  local -a files=()
  dir=$(mktemp -d "$REVIEW_TMP/pages.XXXXXXXXXX")
  for ((page=1; page<=20; page++)); do
    HOLD_CAUSE="read failed: $endpoint page $page"
    gh api --method GET "$endpoint" -F per_page=100 -F page="$page" > "$dir/response.json"
    jq --binary -ce --arg key "$key" '
      (if $key == "" then . else .[$key] end) |
      if type == "array" then . else error("expected API collection") end' "$dir/response.json" > "$dir/$page.json"
    files+=("$dir/$page.json")
    count=$(jq --binary 'length' "$dir/$page.json")
    if ((count < 100)); then
      jq --binary -cs 'add // []' "${files[@]}" > "$dir/result.json"
      printf '%s\n' "$dir/result.json"
      return
    fi
  done
  HOLD_CAUSE="pagination bound reached: $endpoint (20 pages)"
  review_error
}
review_count() {
  local cursor='' page more count checks statuses
  local data="$REVIEW_TMP/thread-response.json" threads="$REVIEW_TMP/threads.json" pending="$REVIEW_TMP/pending.json"
  REVIEW_THREADS=0
  REVIEW_PENDING=0
  REVIEW_NAMES=''
  for ((page=1; page<=20; page++)); do
    HOLD_CAUSE="thread read failed: pull request $PR_NUMBER page $page"
    gh api graphql -f query="$REVIEW_QUERY" \
      -f owner="${GITHUB_REPOSITORY%/*}" -f name="${GITHUB_REPOSITORY#*/}" \
      -F number="$PR_NUMBER" -F cursor="${cursor:-null}" > "$data"
    jq --binary -ce --arg head "$HEAD_SHA" '
      if ((.errors // []) | length) > 0 then error("GraphQL errors") else . end |
      .data.repository.pullRequest |
      if .headRefOid != $head then error("pull request head changed") else .reviewThreads end |
      if (.nodes | type) != "array" or (.pageInfo.hasNextPage | type) != "boolean"
        or any(.nodes[]; (.isResolved | type) != "boolean")
      then error("invalid thread response") else . end' "$data" > "$threads"
    count=$(jq --binary '[.nodes[] | select(.isResolved == false)] | length' "$threads")
    REVIEW_THREADS=$((REVIEW_THREADS + count))
    more=$(jq --binary -r '.pageInfo.hasNextPage' "$threads")
    if [[ "$more" == false ]]; then break; fi
    HOLD_CAUSE='thread pagination bound reached (20 pages)'
    if ((page == 20)); then review_error; fi
    cursor=$(jq --binary -er '.pageInfo.endCursor | select(type == "string" and length > 0)' "$threads")
  done
  HOLD_CAUSE='check-run read failed'
  checks=$(review_pages "repos/$GITHUB_REPOSITORY/commits/$HEAD_SHA/check-runs?filter=latest" check_runs)
  HOLD_CAUSE='commit-status read failed'
  statuses=$(review_pages "repos/$GITHUB_REPOSITORY/commits/$HEAD_SHA/statuses" '')
  HOLD_CAUSE='invalid bot check/status response'
  jq --binary -cn --arg bots "$REVIEW_BOTS" --slurpfile checks "$checks" --slurpfile statuses "$statuses" '
    $checks[0] as $checks | $statuses[0] as $statuses |
    if any($checks[]; (.name | type) != "string" or (.status | type) != "string")
      or any($statuses[]; (.context | type) != "string" or (.state | type) != "string" or (.id | type) != "number")
    then error("invalid bot response") else
      [$checks[] | select(.status != "completed") |
        select(([.name, (.app.slug // "")] | join(" ")) | test($bots; "i")) | .name] +
      [$statuses | sort_by(.id) | reverse | unique_by(.context) | .[] |
        select(.state == "pending") |
        select(([.context, (.creator.login // "")] | join(" ")) | test($bots; "i")) | .context]
    end' > "$pending"
  REVIEW_PENDING=$(jq --binary 'length' "$pending")
  REVIEW_NAMES=$(jq --binary -r 'join(", ")' "$pending")
  HOLD_CAUSE="unresolved threads=$REVIEW_THREADS; pending bot reviews=$REVIEW_PENDING; bots=$REVIEW_NAMES"
}
`
)

// renderReviewHoldScript formats the barrier script for a given duration and poll interval.
func renderReviewHoldScript(waitMinutes, pollSeconds int) string {
	maxPolls := strconv.Itoa(waitMinutes * 60 / pollSeconds)
	return reviewHoldLibrary +
		"review_deadline=$((SECONDS + " + strconv.Itoa(waitMinutes*60) + "))\n" +
		"for ((review_poll=0; review_poll<=" + maxPolls + "; review_poll++)); do\n" + `  review_count
  if ((REVIEW_THREADS == 0 && REVIEW_PENDING == 0)); then break; fi
  review_remaining=$((review_deadline - SECONDS))
  if ((review_remaining <= 0 || review_poll == ` + maxPolls + `)); then review_error; fi
  review_delay=` + strconv.Itoa(pollSeconds) + `
  if ((review_delay > review_remaining)); then review_delay=$review_remaining; fi
  sleep "$review_delay"
  if ((SECONDS > review_deadline)); then review_error; fi
done
printf 'clear=true\n' >> "$GITHUB_OUTPUT"
printf '%s\n' "$HOLD_CAUSE" >> "$GITHUB_STEP_SUMMARY"
`
}

// reviewHoldScript polls within a deadline and refuses work unless both counts are zero.
var reviewHoldScript = renderReviewHoldScript(ReviewHoldWaitMinutes, ReviewHoldPollSeconds)

// RenderReviewHoldStep renders the barrier for a dedicated review-gate job.
// The reference parameter is retained for signature stability but ignored: the
// gate-job pattern uses one inline step per workflow with no YAML anchors.
func RenderReviewHoldStep(_ bool) string {
	return "      - name: Stop while review is open\n" +
		"        id: review-hold\n" +
		"        if: " + ReviewHoldCondition + "\n" +
		"        timeout-minutes: " + strconv.Itoa(ReviewHoldStepTimeoutMinutes) + "\n" +
		"        shell: bash\n" +
		"        env:\n" +
		"          GH_TOKEN: ${{ github.token }}\n" +
		"          PR_NUMBER: ${{ github.event.pull_request.number }}\n" +
		"          HEAD_SHA: ${{ github.event.pull_request.head.sha }}\n" +
		"        run: |\n" + indentReviewScript(reviewHoldScript)
}

// RenderReviewCheckStep renders the gate-check step for heavy matrix legs.
// The step fires when review-gate did not succeed on a non-draft PR and emits
// an annotation with a properly escaped title (colons → %3A).
func RenderReviewCheckStep() string {
	escaped := strings.ReplaceAll(ReviewHoldTitle, ":", "%3A")
	return "      - name: Stop on held review\n" +
		"        id: review-check\n" +
		"        if: " + ReviewCheckCondition + "\n" +
		"        shell: bash\n" +
		"        env:\n" +
		"          TITLE: '" + escaped + "'\n" +
		"          MESSAGE: >-\n" +
		"            " + ReviewCheckMessage + "\n" +
		"        run: |\n" +
		"          echo \"::error title=$TITLE::$MESSAGE\"\n" +
		"          echo \"$MESSAGE\" >> \"$GITHUB_STEP_SUMMARY\"\n" +
		"          exit 1\n"
}

// indentReviewScript nests shared Bash under a workflow step's literal run scalar.
func indentReviewScript(script string) string {
	return "          " + strings.ReplaceAll(strings.TrimSuffix(script, "\n"), "\n", "\n          ") + "\n"
}
