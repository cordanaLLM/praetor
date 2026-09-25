#!/usr/bin/env bash
set -euo pipefail

# adopt_priority_repos.sh - Accelerated adoption and pre-migration epic generation
# Priority targets: golusoris, sveltesentio, goenvoy, vmafx, venio.
#
# The development root comes from the first argument, else PRAETOR_DEV_ROOT, else
# $HOME/dev, so the sweep is not bound to one operator's home directory.
# PRAETOR_STANDARDSCTL names an existing binary to run instead of building one.
#
# Every step is checked. A repository is reported [PASS] only when all three of its
# steps succeeded, [FAIL] naming the first step that did not, and the script exits
# non-zero when any step or reconciliation failed.

PRAETOR_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${PRAETOR_ROOT}/bin"
DEV_ROOT="${1:-${PRAETOR_DEV_ROOT:-${HOME}/dev}}"
STANDARDSCTL="${PRAETOR_STANDARDSCTL:-}"

if [ -z "${STANDARDSCTL}" ]; then
    STANDARDSCTL="${BIN_DIR}/standardsctl"
    mkdir -p "${BIN_DIR}"
    echo "=== Building standardsctl ==="
    go build -o "${STANDARDSCTL}" "${PRAETOR_ROOT}/cmd/standardsctl"
fi

PRIORITY_REPOS=(
    "${DEV_ROOT}/golusoris/golusoris"
    "${DEV_ROOT}/golusoris/sveltesentio"
    "${DEV_ROOT}/golusoris/goenvoy"
    "${DEV_ROOT}/vmafx/vmafx"
    "${DEV_ROOT}/lusoris/venio"
)

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

# reconcile_owner reconciles one owner's cross-repository issue dependencies.
reconcile_owner() {
    local owner="$1" repos="$2"
    if ! "${STANDARDSCTL}" issue reconcile --owner="${owner}" --repos="${repos}" --dry-run=false; then
        echo "    [FAIL] issue reconcile failed for owner: ${owner}"
        return 1
    fi
}

echo "=== Adopting and Hardening Priority Repositories ==="
for repo in "${PRIORITY_REPOS[@]}"; do
    if [ -d "${repo}" ]; then
        echo "--> Processing repository: ${repo}"
        adopt_repo "${repo}" || failures=$((failures + 1))
    else
        echo "    [WARN] Repository ${repo} not found on disk, skipping."
    fi
done

echo "=== Reconciling Cross-Repo Issue Dependencies ==="
reconcile_owner "golusoris" "golusoris/golusoris,golusoris/sveltesentio,golusoris/goenvoy" || failures=$((failures + 1))
reconcile_owner "VMAFx" "VMAFx/vmafx" || failures=$((failures + 1))

if [ "${failures}" -gt 0 ]; then
    echo "=== Priority Adoption Sweep Failed: ${failures} step(s) reported an error ===" >&2
    exit 1
fi

echo "=== Priority Adoption Sweep Complete ==="
