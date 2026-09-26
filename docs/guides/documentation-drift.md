# Documentation drift

A change that alters a user-discoverable surface ships the documentation for it in the same change.
`scripts/docs_drift.py` enforces that on every pull request.

## Why this exists

Praetor already keeps two kinds of text in sync and neither one is this.

`compile-context --verify` proves the *agent* instructions match the canonical `AGENTS.md` across
thirty projections. `praetorctl docs sync` harvests *dependency* documentation from upstream
packages. Nothing checked whether this repository's own `docs/` still describes what its code does,
so documentation drifted and no mechanism reported it.

The design is backported from `VMAFx/vmafx`, which built it first and runs it as a blocking gate.

## What counts as a surface

Only what an adopter reads about before using it. The map in `SURFACE_MAP` is deliberately narrow:

| surface | documentation |
| --- | --- |
| `.config/archetypes/*.yaml`, `facets/*.yaml` | `docs/guides/archetype-authoring.md` |
| `internal/flavor/definitions.go` | `docs/guides/archetype-authoring.md` or `onboarding.md` |
| `internal/config/effective*.go` | `docs/guides/effective-policy.md` |
| `internal/config/register.go`, `register_render.go` | `docs/guides/text-register.md` |
| `internal/hiss/rules.go`, `internal/hiss/go_callgraph.go` | `docs/standards/` |
| `internal/hisscoverage/` | `docs/guides/` or `docs/standards/` |
| `.config/lefthook/scripts/*.py`, `lefthook.yml` | `docs/guides/git-hooks.md` |
| `cmd/standards-mcp/` | `docs/guides/development-mcp.md` |
| `internal/gating/pipeline.go` | `docs/guides/adoption-verification.md` |
| `internal/readmegovernance/`, the adoption README renderer, and its audit gate | `docs/guides/adoption-verification.md` |
| `internal/wishes/`, `internal/state/` | their respective guides |
| `internal/agenthook/*.go` (tests excluded), `cmd/standardsctl/hook.go` | `docs/guides/agent-hooks.md` |
| `.github/workflows/portability.yml`, `scripts/portability_selftest.py` | `docs/standards/hiss-21-platform-neutrality.md` |
| `internal/workstation/`, `cmd/standardsctl/workstation.go`, `scripts/dev_install.py` | `docs/guides/workstation-update.md` |
| `tools/markdownlint/`, its adoption emitter, CI selector, and dedicated workflow | `docs/guides/documentation-governance.md` |
| `scripts/docs_drift.py` | this document |

Everything else is internal. A refactor that changes no listed surface is never accused, and that
is what keeps the gate worth reading: one that fires on every change is one people learn to ignore,
and then it protects nothing.

## When documentation genuinely is not needed

Write `no docs needed: <reason>` in the pull request body. The reason is for the reviewer, not the
script — the script only looks for the phrase.

Use it for a pure internal refactor, a bug fix with no user-visible delta, or a test-only change
that happens to touch a mapped file.

## An ADR does not satisfy the gate

Edits under `docs/adr/` are explicitly excluded. An ADR records a *decision*; a guide describes a
*surface*. Accepting an ADR as documentation coverage would let "I wrote it down somewhere" stand in
for "the guide still matches the behaviour", which is the drift this exists to catch.

## Running it

```bash
make docs-drift-test          # the check's own tests
BASE_SHA=origin/main HEAD_SHA=HEAD python3 scripts/docs_drift.py
```

It runs inside `make verify-all` and as a pull-request step in `.github/workflows/ci.yml`.

## Extending the map

Add a row to `SURFACE_MAP` in `scripts/docs_drift.py`. A test asserts that every mapped document
exists: a surface pointing at a missing file could never be satisfied, so it would fail every change
to that surface with no way to clear it.

Keep additions narrow. The cost of a wrong row is not a missed document — it is an accusation
nobody can act on, which teaches people to reach for the opt-out.
