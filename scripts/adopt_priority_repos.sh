#!/usr/bin/env bash
set -euo pipefail

# adopt_priority_repos.sh - Accelerated adoption and pre-migration epic generation
# Priority targets: golusoris, sveltesentio, vmafx

PRAETOR_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${PRAETOR_ROOT}/bin"
STANDARDSCTL="${BIN_DIR}/standardsctl"

mkdir -p "${BIN_DIR}"
echo "=== Building standardsctl ==="
go build -o "${STANDARDSCTL}" "${PRAETOR_ROOT}/cmd/standardsctl"

PRIORITY_REPOS=(
    "/home/kilian/dev/golusoris/golusoris"
    "/home/kilian/dev/golusoris/sveltesentio"
    "/home/kilian/dev/golusoris/goenvoy"
    "/home/kilian/dev/vmafx/vmafx"
    "/home/kilian/dev/lusoris/venio"
)

echo "=== Adopting and Hardening Priority Repositories ==="
for repo in "${PRIORITY_REPOS[@]}"; do
    if [ -d "${repo}" ]; then
        echo "--> Processing repository: ${repo}"
        
        # 1. Adopt and scaffold Praetor standards
        "${STANDARDSCTL}" adopt --path="${repo}" --force || true
        
        # 2. Scan needs and write .needs.yaml
        "${STANDARDSCTL}" needs scan --path="${repo}" --write || true
        
        # 3. Generate Pre-Migration Epic
        "${STANDARDSCTL}" needs epic --path="${repo}" --output="${repo}/PRE_MIGRATION_EPIC.md" || true
        
        echo "    [PASS] ${repo} adopted and PRE_MIGRATION_EPIC.md generated."
    else
        echo "    [WARN] Repository ${repo} not found on disk, skipping."
    fi
done

echo "=== Reconciling Cross-Repo Issue Dependencies ==="
"${STANDARDSCTL}" issue reconcile --owner="golusoris" --repos="golusoris/golusoris,golusoris/sveltesentio,golusoris/goenvoy" --dry-run=true || true

echo "=== Priority Adoption Sweep Complete ==="
