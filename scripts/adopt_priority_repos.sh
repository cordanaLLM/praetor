#!/usr/bin/env bash
set -euo pipefail

# adopt_priority_repos.sh - Accelerated adoption and pre-migration epic generation
# Priority targets: golusoris, sveltesentio, vmafx

PRAETOR_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN_DIR="${PRAETOR_ROOT}/bin"
PRAETORCTL="${BIN_DIR}/praetorctl"

mkdir -p "${BIN_DIR}"
echo "=== Building praetorctl ==="
go build -o "${PRAETORCTL}" "${PRAETOR_ROOT}/cmd/praetorctl"

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
        "${PRAETORCTL}" adopt --path="${repo}" --force || true
        
        # 2. Scan needs and write .needs.yaml
        "${PRAETORCTL}" needs scan --path="${repo}" --write || true
        
        # 3. Generate and publish Pre-Migration Epic
        "${PRAETORCTL}" needs epic --path="${repo}" --output="${repo}/PRE_MIGRATION_EPIC.md" --publish=true || true
        
        echo "    [PASS] ${repo} adopted and PRE_MIGRATION_EPIC.md published to remote."
    else
        echo "    [WARN] Repository ${repo} not found on disk, skipping."
    fi
done

echo "=== Reconciling Cross-Repo Issue Dependencies ==="
"${PRAETORCTL}" issue reconcile --owner="golusoris" --repos="golusoris/golusoris,golusoris/sveltesentio,golusoris/goenvoy" --dry-run=false || true
"${PRAETORCTL}" issue reconcile --owner="VMAFx" --repos="VMAFx/vmafx" --dry-run=false || true

echo "=== Priority Adoption Sweep Complete ==="
