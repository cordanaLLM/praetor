# Shared lifecycle management

Status: staged implementation plan. This document describes the safety slice
and the later work required to make agent sessions, workspaces, state and
evidence reclaimable as one system.

## Scope and current boundary

Before this safety change, `internal/gc` was a filesystem age sweep. Worktrees are
created by `internal/worktree`, while canary and gate stages remove them through
deferred cleanup. Scheduled dogfood and repair execution already persist locks,
durable IDs, fingerprints, start state and terminal outcomes. Suite, public-loop
and diagnostic outputs are retained under caller-selected artifact roots.

The first safety slice adds explicit `GC ReleasedPaths` to the collector input.
A caller assertion means only that the caller has released and quiesced the
named resource; it is not proof that a live session, lease or agent has ended.
The collector must protect every resource that is unlisted, unknown, locked,
dirty, unreadable or incompletely scanned. It must use confined complete scans,
report truthful dry-run and error outcomes, and avoid a default global cache
flush. Worktree cleanup uses the shared non-force removal path, preserving dirty
work and branches for review.

This slice does not claim session ownership, lease fencing, artifact retention,
or safe automatic deletion from age or an expired heartbeat.

## Shared lifecycle record

Later stages introduce one private, versioned record and catalog. A record has:

- stable `RecordID`, repository identity and owner kind (`agent`, `ide`, `bot`,
  `container`, or `scheduler`);
- session/execution identity, parent record, worktree task/branch/path, state
  path and artifact roots;
- source, configuration and effective-policy digests;
- created time, last heartbeat, lease expiry and fencing token;
- lifecycle state (`planned`, `running`, `interrupted`, `terminal`, `unknown`);
- retention class, terminal outcome, child artifact IDs and cleanup attempts.

The record is an index of relationships, not a replacement for evidence. The
schedule `run-%06d` identity and private `schedule.lock` remain authoritative
for scheduled attempts (`internal/dogfood/schedule.go`). Repair's deterministic
`ExecutionKey`, `execution.lock`, `started.json` and `result.json` remain
authoritative for repair attempts (`internal/repairrun`). The catalog links to
these records and verifies their digests.

Dependency edges are explicit:

```text
session -> execution -> worktree
execution -> state attempt
execution -> artifact manifest -> evidence files
policy snapshot -> session/execution admission and retention decision
reconciliation -> GC plan -> reviewed execution outcome
```

No path name, mtime, process ID, owner string, or heartbeat by itself establishes
ownership or collectability.

## Policy precedence

Lifecycle settings extend the existing effective-policy object and retain the
same source provenance and digest. Proposed lifecycle precedence is:

1. fleet baseline;
2. organization;
3. deployment;
4. workstation;
5. repository manifest;
6. explicit operation input.

Safety constraints join monotonically: the stricter retention, smaller byte or
item bound, longer grace period, and stronger protection wins. Operational
destinations and enabled lifecycle classes follow the documented selected-source
precedence and must remain explicit. A missing or changed policy snapshot
blocks execution. A policy setting never authorizes deletion by itself.

## Stages

### Stage 0: explicit release and protected collection

Extend `gc.Options` with released paths and a confined scan root. Build a
read-only candidate plan before any mutation. Require complete bounded scans;
truncation, missing metadata and read errors produce `unknown`/protected
outcomes. The report separates protected paths, planned removals, completed removals and
errors. Shared reason codes, policy snapshots and persisted plan digests belong
to the later reconciler and admitted execution stages.

Only explicitly released resources may enter an execution plan; worktrees must
also be registered, clean and unlocked. Artifact catalog registration is later work. Unlisted worktrees, state directories, evidence, caches and
root-level binaries remain protected. Do not run `go clean -testcache` by
default; an explicit shared-cache cleanup request is a separate opt-in operation.
Dry-run reports what the plan would do and whether discovery was
complete; it does not claim deletion. Errors remain errors in the report and
exit status.

Use the existing `worktree.Manager.Remove(..., false)` seam. It must preserve
branches and refuse dirty or locked worktrees. Administrative Git pruning is a
separate observed action and cannot be represented as successful resource
release without readback.

Acceptance tests use temporary roots and cover: released clean worktree,
unlisted worktree protection, dirty/locked protection, path confinement,
truncated/incomplete scan, dry-run truthfulness, cache opt-in, preserved branch,
and cleanup/readback failure. No real workstation directory is a fixture.

### Stage 1: producer registration and leases

Make `worktree.Manager` registration-aware and return a lifecycle handle bound
to a catalog record. Register before execution, heartbeat while active, fence
stale writers, and persist cleanup failures. Adapt canary and gate producers;
then schedule, repair, suite, public-loop, IDE adapters, bot jobs and container
tasks. Replace time-derived ownership inference with record-bound IDs while
retaining Git's validated task IDs.

Acceptance requires crash-before-defer recovery, concurrent owner fencing,
heartbeat renewal, lease expiry protection, idempotent release, and CLI/MCP
parity. An expired heartbeat remains protected until reconciliation proves the
session is fenced and terminal.

### Stage 2: state and evidence reconciliation

Add one read-only reconciler joining catalog records, Git porcelain, locks,
`started.json`/`result.json`, schedule state, artifact manifests and filesystem
observations. Reconcile running to interrupted only through the owning state
machine. Preserve dirty, changed, missing, invalid and partial evidence. A
referenced artifact or replay input remains retained regardless of age.

Acceptance covers interruption, duplicate IDs, changed digests, missing terminal
state, orphaned directories, stale catalog entries and bounded partial scans.

### Stage 3: manifests, quarantine and explicit purge

Every producer writes a private artifact manifest with parent record, content
digests, class, retention deadline, references and terminal evidence. Purge is a
separate admitted operation: reconcile, quarantine the exact unchanged target,
read back the quarantine manifest, then purge only after policy and reviewable
plan checks pass. Failed or ambiguous operations retain the quarantine and
evidence.

### Stage 4: scheduled operation

Add a bounded GC tick using the existing schedule lock/state pattern. It records
its own attempt and plan digest, respects byte/item/cadence limits, and reuses
the same reconciler. It does not become a parallel scheduler or infer deletion
from age, heartbeat expiry, or a missing process alone.

## Integration order and non-goals

Implement Stage 0 first, then registration and reconciliation, then manifests
and scheduled operation. Effective-policy integration follows a concrete
consumer and acceptance tests; inert knobs are not exposed. Generic cache and
binary cleanup is a separately scoped producer with explicit age, ownership and
opt-in policy.

IDE, bot and container adapters share the lifecycle contract and do not create
private cleanup loops. Public evidence remains sanitized and private paths,
credentials and personal identifiers stay out of this plan and exported
artifacts.
