#!/usr/bin/env bash
#
# Publish one regeneration of the committed .devcontainer bundle (#338).
#
# .github/workflows/devcontainer-refresh.yml runs this after it has regenerated and verified the
# bundle in its checkout. The script commits the changed bundle files on the refresh branch and
# publishes them. How far it goes depends on the tokens in its environment:
#
# - PR_TOKEN set (the PRAETOR_PR_TOKEN secret): force-push the branch with it, then open one pull
#   request from the branch, or update the one already open. Every failure is an error: a
#   configured token that cannot publish needs an operator.
# - PR_TOKEN empty: this repository does not let GITHUB_TOKEN open pull requests, and GitHub starts
#   no workflow for a push made with GITHUB_TOKEN. The run therefore reports instead of failing.
#   With no refresh pull request open it pushes the branch with PUSH_TOKEN and warns with the link
#   that opens the pull request; a pull request a person opens runs its checks. With one open it
#   pushes nothing, because a push with GITHUB_TOKEN would move that pull request to a head no
#   check ran on, and warns that the open one should land.
#
# Tokens arrive in the environment only, never as arguments, and reach git only as the
# authorization header actions/checkout would have persisted. Warnings also go to the step
# summary when GITHUB_STEP_SUMMARY names one.
#
# Linux only, by design: the only callers are the ubuntu runner step in
# .github/workflows/devcontainer-refresh.yml and scripts/test_devcontainer_refresh_publish.py
# (`make devcontainer-refresh-test`), which skips itself where bash or GNU timeout is absent
# (HISS-21).
set -euo pipefail

# Every forge call is bounded (HISS-02); the workflow step adds its own 5-minute ceiling.
readonly NETWORK_TIMEOUT_SECONDS=60
readonly BUNDLE_DIR='.devcontainer'
readonly TITLE='chore(devcontainer): regenerate the committed source bundle'
readonly TOKEN_ADVICE='configure the PRAETOR_PR_TOKEN secret (contents and pull-requests write) so this job opens and updates the pull request itself'

if [[ $# -ne 3 ]]; then
  echo "usage: $0 BASE_BRANCH REFRESH_BRANCH REPORT_FILE" >&2
  exit 2
fi

readonly BASE_BRANCH=$1
readonly REFRESH_BRANCH=$2
readonly REPORT_FILE=$3

for argument in BASE_BRANCH REFRESH_BRANCH GITHUB_REPOSITORY GITHUB_SERVER_URL PUSH_TOKEN; do
  if [[ -z "${!argument:-}" ]]; then
    echo "devcontainer-refresh: $argument is empty; the workflow step sets it" >&2
    exit 2
  fi
done
if [[ ! -f "$REPORT_FILE" ]]; then
  echo "devcontainer-refresh: the freshness report $REPORT_FILE does not exist" >&2
  exit 2
fi

open_number=''
body_file="$(mktemp)"
trap 'rm -f "$body_file"' EXIT

fail() {
  echo "::error::devcontainer-refresh: $1"
  exit 1
}

# warn prints a workflow warning and repeats it in the step summary, where a green scheduled run
# still shows it.
warn() {
  echo "::warning::devcontainer-refresh: $1"
  if [[ -n "${GITHUB_STEP_SUMMARY:-}" ]]; then
    printf '%s\n\n' "$1" >>"$GITHUB_STEP_SUMMARY"
    cat "$body_file" >>"$GITHUB_STEP_SUMMARY"
  fi
}

bounded() {
  timeout --kill-after=5s "${NETWORK_TIMEOUT_SECONDS}s" "$@"
}

push_branch() {
  local auth
  auth="$(printf 'x-access-token:%s' "$1" | base64 | tr -d '\n')"
  echo "::add-mask::$auth"
  GIT_CONFIG_COUNT=1 \
    GIT_CONFIG_KEY_0="http.$GITHUB_SERVER_URL/.extraheader" \
    GIT_CONFIG_VALUE_0="AUTHORIZATION: basic $auth" \
    bounded git push --quiet --force origin "HEAD:refs/heads/$REFRESH_BRANCH"
}

# find_open_pull_request sets open_number to the open refresh pull request, or to nothing. It
# runs in this shell, not in a substitution, so fail stops the script and its error is printed.
find_open_pull_request() {
  local listed
  if ! listed="$(GH_TOKEN="$1" bounded gh pr list --repo "$GITHUB_REPOSITORY" \
    --head "$REFRESH_BRANCH" --base "$BASE_BRANCH" --state open \
    --json number --jq '.[0].number // empty')"; then
    fail "could not list the open pull requests from $REFRESH_BRANCH"
  fi
  if [[ ! "$listed" =~ ^[0-9]*$ ]]; then
    fail "listing the pull requests from $REFRESH_BRANCH returned '$listed', not a number"
  fi
  open_number=$listed
}

write_body() {
  {
    echo "## Summary of Changes"
    echo
    echo "Scheduled regeneration of the committed \`$BUNDLE_DIR\` bundle from $1 (#338). Only the bundle files change."
    echo
    echo "Freshness before regeneration:"
    echo
    echo '```text'
    cat "$REPORT_FILE"
    echo '```'
  } >"$body_file"
}

publish_pull_request() {
  local number
  push_branch "$PR_TOKEN" || fail "could not push $REFRESH_BRANCH with PRAETOR_PR_TOKEN"
  find_open_pull_request "$PR_TOKEN"
  number=$open_number
  if [[ -n "$number" ]]; then
    GH_TOKEN="$PR_TOKEN" bounded gh pr edit "$number" --repo "$GITHUB_REPOSITORY" \
      --title "$TITLE" --body-file "$body_file" ||
      fail "pushed $REFRESH_BRANCH but could not update pull request #$number with PRAETOR_PR_TOKEN"
    echo "devcontainer-refresh: updated pull request #$number"
    return 0
  fi
  GH_TOKEN="$PR_TOKEN" bounded gh pr create --repo "$GITHUB_REPOSITORY" --base "$BASE_BRANCH" \
    --head "$REFRESH_BRANCH" --title "$TITLE" --body-file "$body_file" ||
    fail "pushed $REFRESH_BRANCH but could not open its pull request with PRAETOR_PR_TOKEN; check that the token has contents and pull-requests write on $GITHUB_REPOSITORY, or open the pull request from that branch"
  echo "devcontainer-refresh: opened a pull request from $REFRESH_BRANCH"
}

report_without_pr_token() {
  local number url
  find_open_pull_request "$PUSH_TOKEN"
  number=$open_number
  if [[ -n "$number" ]]; then
    warn "pull request #$number from $REFRESH_BRANCH is still open with an earlier regeneration. This run pushes nothing: a push with GITHUB_TOKEN would start no checks on it. Land #$number, or $TOKEN_ADVICE."
    return 0
  fi
  push_branch "$PUSH_TOKEN" || fail "could not push $REFRESH_BRANCH with GITHUB_TOKEN"
  url="$GITHUB_SERVER_URL/$GITHUB_REPOSITORY/compare/$BASE_BRANCH...$REFRESH_BRANCH?expand=1"
  warn "pushed $REFRESH_BRANCH without a pull request: GITHUB_TOKEN may not open one in this repository. Open it at $url, or $TOKEN_ADVICE."
}

# An assignment, not a test of a substitution, so a failing status read stops the script instead
# of reading as a clean tree. Untracked files count: a regeneration can add a source part.
changes="$(git status --porcelain -- "$BUNDLE_DIR")"
if [[ -z "$changes" ]]; then
  echo "devcontainer-refresh: the committed bundle already carries this checkout's source; nothing to publish"
  exit 0
fi

base_sha="$(git rev-parse HEAD)"
write_body "$base_sha"
git switch --quiet -C "$REFRESH_BRANCH"
git add --all -- "$BUNDLE_DIR"
git commit --quiet --signoff -m "$TITLE" \
  -m "Regenerated from $base_sha by .github/workflows/devcontainer-refresh.yml, so the bundle carries the build source of that commit again (#338)."

if [[ -n "${PR_TOKEN:-}" ]]; then
  publish_pull_request
else
  report_without_pr_token
fi
