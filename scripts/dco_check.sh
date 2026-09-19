#!/usr/bin/env bash
#
# Verify DCO 1.1 sign-off on every commit an event introduces.
#
# The gate lives in .github/workflows/compliance.yml, which runs on pull requests AND on
# pushes to main and lts-*. The check used to be gated on `github.event_name ==
# 'pull_request'` and hard-wired to `origin/<base_ref>..HEAD`, so a commit that reached a
# protected branch by a direct push was never inspected while the required status check
# still reported green (#292). The range therefore has to come from the event, and the one
# case where a push has no predecessor -- an all-zero `before` -- has to be named out loud
# rather than passed over as an empty loop that looks like a clean one.
#
# Linux only, by design: it is invoked from an ubuntu-latest runner step and from a Makefile
# target. Windows and macOS hosts never execute it, so no portability shim is warranted and
# its test skips itself where bash is absent (HISS-21).
set -euo pipefail

# Bounded so a pathological push cannot make the check unbounded (HISS-02).
readonly MAX_COMMITS=1000
readonly ZERO_SHA_PATTERN='^0+$'

if [[ $# -ne 4 ]]; then
  echo "usage: $0 EVENT_NAME BASE_REF BEFORE_SHA HEAD_SHA" >&2
  exit 2
fi

readonly EVENT_NAME=$1
readonly BASE_REF=$2
readonly BEFORE_SHA=$3
readonly HEAD_SHA=$4

if [[ -z "$HEAD_SHA" ]]; then
  echo "DCO 1.1 check requires a head commit; the event supplied none." >&2
  exit 2
fi

# resolve_base prints the commit the range starts at, or nothing when the event has no
# predecessor to compare against. A pull request names its target branch, which is a remote
# ref on the runner and may be a local one in a checkout without that remote, so both
# spellings are tried.
resolve_base() {
  if [[ "$EVENT_NAME" == "pull_request" ]]; then
    if [[ -z "$BASE_REF" ]]; then
      echo "A pull_request event must supply a base ref." >&2
      exit 2
    fi
    if git rev-parse --verify --quiet "origin/$BASE_REF^{commit}" >/dev/null; then
      printf '%s\n' "origin/$BASE_REF"
    else
      printf '%s\n' "$BASE_REF"
    fi
    return 0
  fi
  if [[ -z "$BEFORE_SHA" || "$BEFORE_SHA" =~ $ZERO_SHA_PATTERN ]]; then
    return 0
  fi
  printf '%s\n' "$BEFORE_SHA"
}

base=$(resolve_base)

if [[ -z "$base" ]]; then
  echo "DCO 1.1 check skipped: the $EVENT_NAME event reports an all-zero 'before' commit, so" \
    "the branch had no previous tip (the first push of a new branch) and there is no commit" \
    "range to inspect. The pull request that merges it inspects every commit on it."
  exit 0
fi

if ! git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
  echo "DCO 1.1 check failed: base commit '$base' is not present in this checkout." >&2
  echo "Fetch enough history (actions/checkout with fetch-depth: 0) and rerun." >&2
  exit 1
fi

readonly RANGE="$base..$HEAD_SHA"
echo "Verifying DCO 1.1 sign-off over $RANGE (event: $EVENT_NAME)."

inspected=0
unsigned=0
while IFS= read -r commit; do
  inspected=$((inspected + 1))
  if ((inspected > MAX_COMMITS)); then
    echo "DCO 1.1 check failed: $RANGE exceeds the $MAX_COMMITS-commit inspection bound." >&2
    exit 1
  fi
  if ! git log -1 --format='%B' "$commit" | grep -q "Signed-off-by:"; then
    echo "Error: Commit $commit is missing 'Signed-off-by:' signature required by DCO 1.1"
    unsigned=$((unsigned + 1))
  fi
done < <(git rev-list "$RANGE")

if ((unsigned > 0)); then
  echo "DCO check failed: $unsigned of $inspected commits in $RANGE without DCO signature."
  exit 1
fi
if ((inspected == 0)); then
  echo "DCO 1.1: $RANGE introduces no commits; nothing to verify."
  exit 0
fi
echo "DCO 1.1: all $inspected commits in $RANGE carry a valid signature."
