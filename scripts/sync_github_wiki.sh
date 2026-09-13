#!/usr/bin/env bash
set -euo pipefail

readonly MAX_WIKI_FILES=256
readonly GIT_TIMEOUT_SECONDS=${WIKI_GIT_TIMEOUT_SECONDS:-60}

if [[ $# -ne 2 ]]; then
  echo "usage: $0 SOURCE_DIRECTORY WIKI_REMOTE" >&2
  exit 2
fi

readonly SOURCE_ARGUMENT=$1
readonly WIKI_REMOTE=$2

if [[ ! "$GIT_TIMEOUT_SECONDS" =~ ^[0-9]+$ ]] ||
  (( GIT_TIMEOUT_SECONDS < 1 || GIT_TIMEOUT_SECONDS > 300 )); then
  echo "WIKI_GIT_TIMEOUT_SECONDS must be an integer from 1 through 300." >&2
  exit 2
fi

if [[ ! -d "$SOURCE_ARGUMENT" ]]; then
  echo "Wiki source directory does not exist: $SOURCE_ARGUMENT" >&2
  exit 2
fi

case "$WIKI_REMOTE" in
  http://* | https://*)
    remote_authority=${WIKI_REMOTE#*://}
    remote_authority=${remote_authority%%/*}
    if [[ "$remote_authority" == *"@"* ]]; then
      echo "Wiki remote URL must not contain credentials; use WIKI_TOKEN." >&2
      exit 2
    fi
    ;;
esac

SOURCE_DIR=$(cd "$SOURCE_ARGUMENT" && pwd -P)
readonly SOURCE_DIR
RUN_DIR=$(mktemp -d "${TMPDIR:-/tmp}/praetor-wiki-sync.XXXXXX")
readonly RUN_DIR
readonly CLONE_DIR="$RUN_DIR/wiki"
readonly SOURCE_LIST="$RUN_DIR/source-files"
readonly CLONE_LOG="$RUN_DIR/clone.log"

cleanup() {
  rm -rf -- "$RUN_DIR"
}
trap cleanup EXIT

git_bounded() {
  timeout --kill-after=5s "${GIT_TIMEOUT_SECONDS}s" git "$@"
}

find "$SOURCE_DIR" -mindepth 1 -maxdepth 1 -print0 | sort -z >"$SOURCE_LIST"
mapfile -d '' -n "$((MAX_WIKI_FILES + 1))" -t source_entries <"$SOURCE_LIST"

if (( ${#source_entries[@]} == 0 )); then
  echo "Wiki source directory must contain at least one Markdown file." >&2
  exit 2
fi
if (( ${#source_entries[@]} > MAX_WIKI_FILES )); then
  echo "Wiki source directory may contain at most $MAX_WIKI_FILES files." >&2
  exit 2
fi

source_names=()
for source_entry in "${source_entries[@]}"; do
  if [[ ! -f "$source_entry" || -L "$source_entry" || "$source_entry" != *.md ]]; then
    echo "Wiki source entries must be regular Markdown files: ${source_entry##*/}" >&2
    exit 2
  fi
  source_names+=("${source_entry##*/}")
done

export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
export GIT_TERMINAL_PROMPT=0
unset GIT_CONFIG_COUNT GIT_CONFIG_PARAMETERS GIT_CONFIG_SYSTEM SSH_ASKPASS
if [[ -n "${WIKI_TOKEN:-}" ]]; then
  readonly ASKPASS="$RUN_DIR/git-askpass.sh"
  cat >"$ASKPASS" <<'EOF'
#!/usr/bin/env bash
case "$1" in
  *Username*) printf '%s\n' 'x-access-token' ;;
  *Password*) printf '%s\n' "$WIKI_TOKEN" ;;
  *) exit 1 ;;
esac
EOF
  chmod 0700 "$ASKPASS"
  export GIT_ASKPASS="$ASKPASS"
else
  unset GIT_ASKPASS
fi

echo "Cloning wiki repository..."
if git_bounded -c credential.helper= clone --quiet -- \
  "$WIKI_REMOTE" "$CLONE_DIR" 2>"$CLONE_LOG"; then
  :
else
  clone_status=$?
  echo "Wiki repository could not be cloned. Git reported:" >&2
  sed -n '1,20p' "$CLONE_LOG" >&2
  if (( clone_status == 124 || clone_status == 137 )); then
    echo "Wiki clone exceeded the ${GIT_TIMEOUT_SECONDS}-second Git timeout." >&2
  fi
  printf '%s\n' \
    "If the GitHub wiki has no pages, create its initial page in the GitHub web UI, then rerun this workflow." \
    "Otherwise, check token permissions and connectivity." >&2
  exit 1
fi

git_bounded -C "$CLONE_DIR" config user.name "${WIKI_GIT_NAME:-cordana-standards[bot]}"
git_bounded -C "$CLONE_DIR" config user.email "${WIKI_GIT_EMAIL:-standards-bot@cordana.ai}"

cp -- "${source_entries[@]}" "$CLONE_DIR/"
git_bounded -C "$CLONE_DIR" add -- "${source_names[@]}"

if git_bounded -C "$CLONE_DIR" diff --cached --quiet; then
  echo "Wiki is already up to date. No changes to commit."
  exit 0
fi

git_bounded -C "$CLONE_DIR" commit --quiet \
  -m "docs(wiki): synchronize wiki from docs/wiki [skip ci]"
wiki_branch=$(git_bounded -C "$CLONE_DIR" symbolic-ref --quiet --short HEAD)
if git_bounded -c credential.helper= -C "$CLONE_DIR" push --quiet \
  origin "HEAD:refs/heads/$wiki_branch"; then
  :
else
  push_status=$?
  if (( push_status == 124 || push_status == 137 )); then
    echo "Wiki push exceeded the ${GIT_TIMEOUT_SECONDS}-second Git timeout." >&2
  else
    echo "Wiki push failed with Git exit status $push_status." >&2
  fi
  exit 1
fi
echo "Wiki sync completed successfully."
