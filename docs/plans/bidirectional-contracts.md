# Bidirectional module contracts

Status: audited design proposal, 2026-09-13, baseline `eb3f0d8`.
Generated configuration, observations, verified repairs and remote acceptance
have different authority. Returning information must preserve that distinction.
This plan extends existing services; it does not introduce a second sync engine.

## Current seams and missing return paths

| Surface | Verified implementation | Missing return contract |
|---|---|---|
| Instructions/editor/templates | Compiler projects canonical AGENTS and verifies bytes; adoption preserves existing editor files. | Parsed local changes, field ownership and loss/conflict proposals. Generated instructions remain projections. |
| DevContainer | Typed synthesis and verification; custom adoption reports unverified execution. | Unknown-field preservation and explicit import capabilities. Typed equality alone is not lossless roundtrip proof. |
| Client tools | JSON/YAML merge, same-name conflict refusal, snapshot/CAS and readback. | Native client discovery/import; export-only clients must continue reporting their limit. |
| Dogfood/repair | Hashed cases/jobs and durable repair attempt states with scoped validation. | Bind a repair result to a fresh replay of its original case before consuming the finding. |
| Forge/upstream | Issue listing and outbound create/update; dependency reconciler proposes actions. | Persist remote number/URL and readback, reconcile inbound status, avoid titles as durable identity. |
| Harvest/memory/forks | Source observations, replay counts and local operational merge preparation. | Source-bound acknowledgements, correction/supersession, upstream acceptance/release and downstream rollout evidence. |
| Lifecycle/GC | Explicit release assertions and checked non-force resource removal. | Producer records, leases/fencing, crash reconciliation and retained-evidence relationships. |

Evidence pointers: `internal/compiler/transpiler.go`, `internal/editor/editor.go`,
`internal/devcontainer/devcontainer.go`, `internal/clientsetup/plan.go`,
`internal/dogfood/repair.go`, `internal/repairrun/run_state.go`,
`internal/forge/issues.go`, `internal/forge/reconciler.go`,
`internal/harvester/transcript_types.go`, `internal/operationalsync/prepare.go`.
The effective-policy resolver currently layers complexity only; broader settings
must gain real consumers before they can be advertised as fleet policy.

## Shared contract

Each adapter declares supported directions: observe, project, import-proposal,
apply, verify and reconcile. A direction can be unsupported or lossy. Export or
successful readback alone never implies reverse import support.

Use stable source/object IDs, base and observed revisions, configuration/policy
digests, field ownership, trust class and provenance. Compare base, desired and
observed state before producing a proposal. Preserve unknown fields or report
loss explicitly. Conflicts remain reviewable records; no unconditional last-writer
wins. Bind apply to the reviewed revision, then retain exact readback and an
idempotency key. Handle cancellation, duplicates and out-of-order events explicitly.

Reuse `contextopt` snapshot/CAS, client parsers, forge interfaces, repair attempt
keys and effective-policy provenance. Policy selects direction and authority per
deployment/repository/adapter. Safety requirements can tighten. Private settings
and generated files cannot become public upstream authority through import.
Missing identity, unsupported required direction or incomplete inventory blocks
the corresponding transition rather than reporting synchronization success.

## Staged delivery

1. Add additive capability and evidence contracts to existing reports; preserve
   public wire compatibility and make unsupported/lossy states visible in CLI/MCP.
2. Complete one feedback loop: original dogfood case -> candidate repair -> fresh
   source-bound replay -> retained outcome. Scoped tests alone do not close it.
3. Persist forge identity/readback and resume interrupted publications; reconcile
   observed upstream issue/PR/release status without inferring local code adoption.
4. Add client/config import proposals, field conflicts and unknown-field tests;
   promote only changes allowed by the selected authority policy.
5. Connect lifecycle records and retained evidence to the same outcomes before
   automatic cleanup or broader scheduled rollout.

Each stage requires positive, negative and boundary tests for stale bases,
concurrent edits, duplicates, partial responses, unknown fields, privacy and
restart recovery. Exercise real CLI/MCP tools with disposable roots. Remote
acceptance and rollout need separate observed evidence on exact revisions.

Related plans: [lifecycle management](lifecycle-management.md),
[package pipeline](package-development-pipeline.md),
[upstream contributions](upstream-contribution-workflow.md).
