#!/usr/bin/env bash
set -euo pipefail

# adopt_repos.sh - adopt local repositories and publish their pre-migration epics.
#
# The script names no repository of its own: every target comes from the command line or a
# targets file, and without one it prints its usage and exits 2. Each target runs three
# steps: praetorctl adopt --force, needs scan --write, and needs epic --publish --yes.
#
# A target path that is not absolute resolves against the development root: --dev-root,
# else PRAETOR_DEV_ROOT, else $HOME/dev. PRAETOR_STANDARDSCTL names an existing binary to
# run instead of building one.
#
# Every step is checked. A repository is reported [PASS] only when all three of its steps
# succeeded, [FAIL] naming the first step that did not, and the script exits non-zero when
# any step or the optional reconciliation failed.

usage() {
    cat <<'USAGE'
Usage: scripts/adopt_repos.sh [options] [--] <repository-path>...

Adopts each repository, writes its needs scan and publishes its pre-migration epic.

Options:
  --targets-file FILE     Read repository paths from FILE, one per line; blank lines and
                          lines starting with # are skipped. Combines with arguments.
  --dev-root DIR          Resolve relative paths against DIR
                          (default: $PRAETOR_DEV_ROOT, else $HOME/dev).
  --reconcile             Afterwards run 'praetorctl issue reconcile --dry-run=false' over
                          its default scope (forge.reconcile_repos, else the current
                          repository).
  --reconcile-repos LIST  Reconcile the comma-separated <owner>/<name> LIST instead;
                          implies --reconcile.
  -h, --help              Print this help and exit.
USAGE
}

DEV_ROOT="${PRAETOR_DEV_ROOT:-${HOME}/dev}"
TARGETS_FILE=""
RECONCILE=0
RECONCILE_REPOS=""
TARGETS=()

# MAX_TARGETS bounds the repositories one run adopts.
MAX_TARGETS=256

while [ "$#" -gt 0 ]; do
    case "$1" in
        -h | --help)
            usage
            exit 0
            ;;
        --targets-file)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            TARGETS_FILE="$2"
            shift 2
            ;;
        --targets-file=*)
            TARGETS_FILE="${1#*=}"
            shift
            ;;
        --dev-root)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            DEV_ROOT="$2"
            shift 2
            ;;
        --dev-root=*)
            DEV_ROOT="${1#*=}"
            shift
            ;;
        --reconcile)
            RECONCILE=1
            shift
            ;;
        --reconcile-repos)
            [ "$#" -ge 2 ] || { usage >&2; exit 2; }
            RECONCILE=1
            RECONCILE_REPOS="$2"
            shift 2
            ;;
        --reconcile-repos=*)
            RECONCILE=1
            RECONCILE_REPOS="${1#*=}"
            shift
            ;;
        --)
            shift
            TARGETS+=("$@")
            break
            ;;
        -*)
            echo "adopt_repos.sh: unknown option: $1" >&2
            usage >&2
            exit 2
            ;;
        *)
            TARGETS+=("$1")
            shift
            ;;
    esac
done

if [ -n "${TARGETS_FILE}" ]; then
    if [ ! -f "${TARGETS_FILE}" ]; then
        echo "adopt_repos.sh: targets file not found: ${TARGETS_FILE}" >&2
        exit 2
    fi
    while IFS= read -r line || [ -n "${line}" ]; do
        line="${line#"${line%%[![:space:]]*}"}"
        line="${line%"${line##*[![:space:]]}"}"
        case "${line}" in
            "" | "#"*) continue ;;
        esac
        TARGETS+=("${line}")
    done <"${TARGETS_FILE}"
fi

if [ "${#TARGETS[@]}" -eq 0 ]; then
    echo "adopt_repos.sh: no repository to adopt; pass paths or --targets-file." >&2
    usage >&2
    exit 2
fi
if [ "${#TARGETS[@]}" -gt "${MAX_TARGETS}" ]; then
    echo "adopt_repos.sh: ${#TARGETS[@]} targets exceed the limit of ${MAX_TARGETS}." >&2
    exit 2
fi

PRAETOR_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${PRAETOR_ROOT}/bin"
STANDARDSCTL="${PRAETOR_STANDARDSCTL:-}"

if [ -z "${STANDARDSCTL}" ]; then
    STANDARDSCTL="${BIN_DIR}/standardsctl"
    mkdir -p "${BIN_DIR}"
    echo "=== Building standardsctl ==="
    go build -o "${STANDARDSCTL}" "${PRAETOR_ROOT}/cmd/standardsctl"
fi

failures=0

# adopt_repo runs the three adoption steps for one repository. Each step gates the next:
# a repository that could not be adopted has nothing for the later steps to scan.
# Publication needs --yes, because the forge target is derived from repository content.
adopt_repo() {
    local repo="$1"
    if ! "${STANDARDSCTL}" adopt --path="${repo}" --force; then
        echo "    [FAIL] ${repo} failed at step: adopt"
        return 1
    fi
    if ! "${STANDARDSCTL}" needs scan --path="${repo}" --write; then
        echo "    [FAIL] ${repo} failed at step: needs scan"
        return 1
    fi
    if ! "${STANDARDSCTL}" needs epic --path="${repo}" --output="${repo}/PRE_MIGRATION_EPIC.md" --publish=true --yes; then
        echo "    [FAIL] ${repo} failed at step: needs epic"
        return 1
    fi
    echo "    [PASS] ${repo} adopted and PRE_MIGRATION_EPIC.md published to remote."
}

# reconcile runs one cross-repository issue reconciliation, over RECONCILE_REPOS when set
# and otherwise over the scope praetorctl resolves itself.
reconcile() {
    local args=(issue reconcile --dry-run=false)
    if [ -n "${RECONCILE_REPOS}" ]; then
        args+=("--repos=${RECONCILE_REPOS}")
    fi
    if ! "${STANDARDSCTL}" "${args[@]}"; then
        echo "    [FAIL] issue reconcile failed"
        return 1
    fi
}

echo "=== Adopting ${#TARGETS[@]} repositories ==="
for target in "${TARGETS[@]}"; do
    case "${target}" in
        /*) repo="${target}" ;;
        *) repo="${DEV_ROOT}/${target}" ;;
    esac
    if [ -d "${repo}" ]; then
        echo "--> Processing repository: ${repo}"
        adopt_repo "${repo}" || failures=$((failures + 1))
    else
        echo "    [WARN] Repository ${repo} not found on disk, skipping."
    fi
done

if [ "${RECONCILE}" -eq 1 ]; then
    echo "=== Reconciling Cross-Repo Issue Dependencies ==="
    reconcile || failures=$((failures + 1))
fi

if [ "${failures}" -gt 0 ]; then
    echo "=== Adoption Sweep Failed: ${failures} step(s) reported an error ===" >&2
    exit 1
fi

echo "=== Adoption Sweep Complete ==="
