#!/usr/bin/env python3
"""Check that a change touching a user-discoverable surface also updates its documentation.

Backported from VMAFx/vmafx, which built this mechanism first and runs it as a blocking CI gate
(its ADR-0167). The design is theirs; the surface map is praetor's own.

The problem it solves is one praetor otherwise had no answer to. `compile-context --verify` proves
the *agent* instructions stay in sync with AGENTS.md across thirty projections, and `docs sync`
harvests *dependency* documentation. Neither checks whether this repository's own `docs/` still
describes what its code does. Documentation drifted silently, and nothing reported it.

The mapping is deliberately narrow. A surface listed here is one an adopter reads about before
using it; everything else is internal and legitimately needs no documentation for a change. The
check therefore accuses rarely, which is what makes an accusation worth reading.
"""

from __future__ import annotations

import os
import re
import subprocess
import sys

# Bounded so a pathological diff cannot make the check unbounded (HISS-02).
MAX_DIFF_FILES = 5000

# (surface pattern, expected docs pattern, human label).
# Several surfaces may map to one document; that is normal, not a duplicate.
SURFACE_MAP: list[tuple[str, str, str]] = [
    (r"^\.config/archetypes/[^/]+\.yaml$", r"^docs/guides/archetype-authoring\.md$",
     "archetype catalog"),
    (r"^\.config/archetypes/facets/[^/]+\.yaml$", r"^docs/guides/archetype-authoring\.md$",
     "facet catalog"),
    (r"^internal/flavor/definitions\.go$", r"^docs/guides/(archetype-authoring|onboarding)\.md$",
     "flavor definitions"),
    (r"^internal/config/effective(_load)?\.go$", r"^docs/guides/effective-policy\.md$",
     "effective policy resolution"),
    (r"^internal/config/register(_render)?\.go$", r"^docs/guides/text-register\.md$",
     "text register policy"),
    (r"^internal/hiss/rules\.go$", r"^docs/standards/", "HISS rule matchers"),
    (r"^internal/hiss/go_callgraph\.go$", r"^docs/standards/", "Go call-graph matcher"),
    (r"^internal/hisscoverage/", r"^docs/(guides|standards)/", "HISS-20 coverage engine"),
    (r"^\.config/lefthook/scripts/[^/]+\.py$", r"^docs/guides/git-hooks\.md$", "git hook scripts"),
    (r"^lefthook\.yml$", r"^docs/guides/git-hooks\.md$", "hook configuration"),
    (r"^cmd/standards-mcp/", r"^docs/guides/development-mcp\.md$", "development MCP server"),
    (r"^internal/gating/pipeline\.go$", r"^docs/guides/adoption-verification\.md$",
     "gating pipeline"),
    (r"^(internal/readmegovernance/|internal/adopt/governance\.go$|cmd/standardsctl/audit_readme\.go$)",
     r"^docs/guides/adoption-verification\.md$", "managed README governance contract"),
    (r"^internal/wishes/", r"^docs/guides/wishes-and-polls\.md$", "wish ledger"),
    (r"^internal/state/", r"^docs/guides/state-ledger-integrity\.md$", "state ledger"),
    (r"^(internal/agenthook/[^/]+(?<!_test)\.go|cmd/standardsctl/hook\.go)$",
     r"^docs/guides/agent-hooks\.md$", "agent-hook entrypoint"),
    (r"^(\.github/workflows/portability\.yml|scripts/portability_selftest\.py)$",
     r"^docs/standards/hiss-21-platform-neutrality\.md$", "platform neutrality gate"),
    (r"^(internal/workstation/|cmd/standardsctl/workstation\.go$|scripts/dev_install\.py$)",
     r"^docs/guides/workstation-update\.md$", "workstation install/status"),
    (r"^(tools/markdownlint/|internal/adopt/documentation\.go$|internal/cifilter/filter\.go$|"
     r"\.github/workflows/praetor-docs\.yml$)",
     r"^docs/guides/documentation-governance\.md$", "Markdown documentation governance"),
    # The check guards its own documentation. Changing which surfaces are mapped changes what
    # contributors are required to document, which is itself user-discoverable.
    (r"^scripts/docs_drift\.py$", r"^docs/guides/documentation-drift\.md$", "docs-drift surface map"),
]

# An ADR records a decision; it is not the documentation of a surface. Counting one as a docs hit
# would let "I wrote it down somewhere" satisfy "the guide still describes the behaviour".
ADR_PATTERN = re.compile(r"^docs/adr/")

OPT_OUT = re.compile(r"no docs needed[: ]", re.IGNORECASE)


def changed_files(base: str, head: str) -> list[str]:
    """Return the paths changed between two commits."""
    out = subprocess.run(
        ["git", "diff", "--name-only", f"{base}..{head}"],
        capture_output=True, text=True, check=True,
    ).stdout.splitlines()
    return [line.strip() for line in out[:MAX_DIFF_FILES] if line.strip()]


def violations(files: list[str]) -> list[str]:
    """Return one message per surface touched without its documentation."""
    docs_touched = [f for f in files if f.startswith("docs/") and not ADR_PATTERN.match(f)]
    found: list[str] = []
    for surface_pat, docs_pat, label in SURFACE_MAP:
        surface = re.compile(surface_pat)
        if not any(surface.match(f) for f in files):
            continue
        expected = re.compile(docs_pat)
        if any(expected.match(f) for f in docs_touched):
            continue
        found.append(f"{label}: changed {surface_pat} with no edit under {docs_pat}")
    return found


def main() -> int:
    base, head = os.environ.get("BASE_SHA", ""), os.environ.get("HEAD_SHA", "")
    if not base or not head:
        print("docs-drift: BASE_SHA and HEAD_SHA are required", file=sys.stderr)
        return 2
    if OPT_OUT.search(os.environ.get("PR_BODY", "")):
        print("docs-drift: opt-out claimed in the pull request body ('no docs needed: ...').")
        return 0
    found = violations(changed_files(base, head))
    if not found:
        print("docs-drift: every touched surface carries a documentation change.")
        return 0
    print("docs-drift: a user-discoverable surface changed without its documentation.\n",
          file=sys.stderr)
    for message in found:
        print(f"  - {message}", file=sys.stderr)
    print(
        "\nUpdate the documentation in the same change, or state why none is needed by writing\n"
        "'no docs needed: <reason>' in the pull request body. An ADR does not count: it records a\n"
        "decision rather than describing the surface.",
        file=sys.stderr,
    )
    return 1


if __name__ == "__main__":
    sys.exit(main())
