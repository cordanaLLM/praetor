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

# The byte-order sort keeps the recorded manifest identical across runner locales.
find "$SOURCE_DIR" -mindepth 1 -maxdepth 1 -print0 | LC_ALL=C sort -z >"$SOURCE_LIST"
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
  # The manifest holds one page name per line, so a name cannot contain a line break.
  if [[ "${source_entry##*/}" == *$'\n'* ]]; then
    echo "Wiki source file names must not contain line breaks." >&2
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

# The manifest in the wiki repository lists the pages the previous sync published.
# Only those names are ever removed, so pages created in the wiki itself are kept;
# a wiki without a manifest removes nothing and records one for the next run.
readonly MANIFEST_NAME=.praetor-wiki-manifest
readonly MANIFEST_PATH="$CLONE_DIR/$MANIFEST_NAME"

fail_manifest() {
  echo "Wiki manifest $MANIFEST_NAME $1; nothing was changed." >&2
  echo "Correct it in the wiki repository, or delete it to resume without removals." >&2
  exit 1
}

declare -A source_lookup=()
for source_name in "${source_names[@]}"; do
  source_lookup["$source_name"]=1
done

stale_names=()
collect_stale_names() {
  local previous_names=() previous_name
  if [[ ! -e "$MANIFEST_PATH" && ! -L "$MANIFEST_PATH" ]]; then
    return 0
  fi
  if [[ ! -f "$MANIFEST_PATH" || -L "$MANIFEST_PATH" ]]; then
    fail_manifest "is not a regular file"
  fi
  mapfile -n "$((MAX_WIKI_FILES + 1))" -t previous_names <"$MANIFEST_PATH"
  if (( ${#previous_names[@]} > MAX_WIKI_FILES )); then
    fail_manifest "lists more than $MAX_WIKI_FILES pages"
  fi
  for previous_name in "${previous_names[@]}"; do
    if [[ -z "$previous_name" || "$previous_name" == */* || "$previous_name" != *.md ]]; then
      fail_manifest "has an entry that is not a Markdown page name"
    fi
    if [[ -z "${source_lookup[$previous_name]+present}" ]]; then
      stale_names+=("$previous_name")
    fi
  done
}

collect_stale_names
if (( ${#stale_names[@]} > 0 )); then
  printf 'Removing wiki page no longer in source: %s\n' "${stale_names[@]}"
  # Literal pathspecs: a manifest entry names exactly one page, never a glob.
  git_bounded --literal-pathspecs -C "$CLONE_DIR" rm --quiet --ignore-unmatch -- \
    "${stale_names[@]}"
fi

cp -- "${source_entries[@]}" "$CLONE_DIR/"
printf '%s\n' "${source_names[@]}" >"$MANIFEST_PATH"
git_bounded --literal-pathspecs -C "$CLONE_DIR" add -- "${source_names[@]}" "$MANIFEST_NAME"

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
