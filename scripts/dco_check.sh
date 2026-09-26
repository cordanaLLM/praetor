#!/usr/bin/env bash
#
# Verify DCO 1.1 sign-off on every commit an event introduces.
#
# The gate lives in .github/workflows/compliance.yml, which runs on pull requests AND on
# pushes to main and lts-*. The check used to be gated on `github.event_name ==
# 'pull_request'` and hard-wired to `origin/<base_ref>..HEAD`, so a commit that reached a
# protected branch by a direct push was never inspected while the required status check
# still reported green (#292). The range therefore has to come from the event.
#
# A push that creates a branch reports an all-zero `before`, and an `lts-*` branch is
# created by exactly such a push, so reporting that case as "nothing to inspect" would
# leave the same hole open on half the branch pattern the gate exists for: the commits
# would reach a protected branch with the gate green and no pull request would ever look
# at them. The range then runs from the repository's default branch, the only predecessor
# such a push has. A default branch the checkout does not have fails loudly; it is never
# passed over.
#
# Both range endpoints are verified before the range is read, and the enumeration's exit
# status is checked. A gate that cannot read its range must say so: an unread range looks
# exactly like a clean empty one, which is the silent green this gate was filed against.
#
# Linux only, by design: the only callers are the ubuntu-latest runner step in
# .github/workflows/compliance.yml and scripts/test_dco_check.py, which drives it under bash
# (`make dco-check-test`). Windows and macOS hosts never execute it, so no portability shim
# is warranted and its test skips itself where bash is absent (HISS-21).
set -euo pipefail

# Bounded so a pathological push cannot make the check unbounded (HISS-02).
readonly MAX_COMMITS=1000
readonly ZERO_SHA_PATTERN='^0+$'
readonly SIGN_OFF_TRAILER='Signed-off-by:'
readonly FETCH_ADVICE='Fetch enough history (actions/checkout with fetch-depth: 0) and rerun.'
# A ref name that is absent is a depth problem. A previous tip reported by a push is not
# always one: a force push leaves the tip it replaced reachable from no ref, so the runner
# never fetches it at any depth and the advice above cannot resolve the failure.
readonly DISCARDED_TIP_ADVICE='If a force push replaced that tip, no fetch depth restores it: it is reachable from no ref. Inspect the commits it carried through a pull request, or rerun the gate on a range this checkout has.'

if [[ $# -ne 5 ]]; then
  echo "usage: $0 EVENT_NAME BASE_REF BEFORE_SHA HEAD_SHA DEFAULT_BRANCH" >&2
  exit 2
fi

readonly EVENT_NAME=$1
readonly BASE_REF=$2
readonly BEFORE_SHA=$3
readonly HEAD_SHA=$4
readonly DEFAULT_BRANCH=$5

if [[ -z "$HEAD_SHA" ]]; then
  echo "DCO 1.1 check requires a head commit; the event supplied none." >&2
  exit 2
fi

# resolve_branch prints the spelling of a branch name this checkout carries: the
# remote-tracking ref a runner's clone has (actions/checkout fetches
# +refs/heads/*:refs/remotes/origin/* at fetch-depth 0), or the local branch a plain clone
# has. A name neither spelling resolves is printed as given, so the caller's presence check
# names it in the failure rather than treating it as an empty range.
resolve_branch() {
  local name=$1
  if git rev-parse --verify --quiet "origin/$name^{commit}" >/dev/null; then
    printf '%s\n' "origin/$name"
    return 0
  fi
  printf '%s\n' "$name"
}

# resolve_base prints the commit or ref the inspected range starts at. A pull request names
# its target branch; a push names its previous tip, or, when it created the branch, the
# repository default branch the new commits are measured against.
resolve_base() {
  if [[ "$EVENT_NAME" == "pull_request" ]]; then
    if [[ -z "$BASE_REF" ]]; then
      echo "A pull_request event must supply a base ref." >&2
      exit 2
    fi
    resolve_branch "$BASE_REF"
    return 0
  fi
  if [[ -z "$BEFORE_SHA" || "$BEFORE_SHA" =~ $ZERO_SHA_PATTERN ]]; then
    if [[ -z "$DEFAULT_BRANCH" ]]; then
      echo "DCO 1.1 check failed: the $EVENT_NAME event reports no previous tip and names no" \
        "default branch, so the commits it introduces cannot be determined." >&2
      exit 2
    fi
    resolve_branch "$DEFAULT_BRANCH"
    return 0
  fi
  printf '%s\n' "$BEFORE_SHA"
}

# require_commit fails the check when one end of the range is absent from the checkout. The
# caller supplies the advice, because why an endpoint can be absent depends on which
# endpoint it is.
require_commit() {
  local role=$1 revision=$2 advice=$3
  if git rev-parse --verify --quiet "$revision^{commit}" >/dev/null; then
    return 0
  fi
  echo "DCO 1.1 check failed: $role commit '$revision' is not present in this checkout." >&2
  echo "$advice" >&2
  exit 1
}

base=$(resolve_base)
if [[ "$base" == "$BEFORE_SHA" ]]; then
  require_commit base "$base" "$FETCH_ADVICE $DISCARDED_TIP_ADVICE"
else
  require_commit base "$base" "$FETCH_ADVICE"
fi
require_commit head "$HEAD_SHA" "$FETCH_ADVICE"

readonly RANGE="$base..$HEAD_SHA"
echo "Verifying DCO 1.1 sign-off over $RANGE (event: $EVENT_NAME)."

# The enumeration lands in a file rather than a process substitution: a process
# substitution's exit status escapes both `set -e` and `pipefail`, so a rev-list that
# failed would leave the loop reading nothing and the gate reporting the unread range as a
# clean empty one.
commits=$(mktemp)
trap 'rm -f "$commits"' EXIT
if ! git rev-list "$RANGE" >"$commits"; then
  echo "DCO 1.1 check failed: the commits in $RANGE could not be enumerated." >&2
  exit 1
fi

inspected=0
unsigned=0
while IFS= read -r commit; do
  inspected=$((inspected + 1))
  if ((inspected > MAX_COMMITS)); then
    echo "DCO 1.1 check failed: $RANGE exceeds the $MAX_COMMITS-commit inspection bound." >&2
    exit 1
  fi
  # The message is captured, not piped into grep: grep exits at its first match, git then
  # takes SIGPIPE, and under pipefail that status would report a correctly signed commit
  # with a message larger than one pipe buffer as unsigned.
  message=$(git log -1 --format='%B' "$commit")
  if [[ "$message" != *"$SIGN_OFF_TRAILER"* ]]; then
    echo "Error: Commit $commit is missing '$SIGN_OFF_TRAILER' signature required by DCO 1.1"
    unsigned=$((unsigned + 1))
  fi
done <"$commits"

if ((unsigned > 0)); then
  echo "DCO check failed: $unsigned of $inspected commits in $RANGE without DCO signature."
  exit 1
fi
if ((inspected == 0)); then
  echo "DCO 1.1: $RANGE introduces no commits; nothing to verify."
  exit 0
fi
echo "DCO 1.1: all $inspected commits in $RANGE carry a valid signature."
