# Issue inventory and synchronization integrity

`forge.SyncIssues` requires a complete bounded inventory before changing issues.
Its existing signature and identity rule remain: titles match exactly after
trimming surrounding whitespace. Matching is case-sensitive. This is declarative
title matching, not semantic duplicate detection or upstream contribution approval.

A planned mutation batch may contain at most 1,000 issues. Its trimmed titles must
be unique. The complete inventory may contain up to 2,000 entries, and sync checks
all of them, including entries beyond the mutation batch limit. If a selected title
matches multiple existing entries, the whole batch fails before any mutation.
Duplicate existing titles unrelated to the planned batch do not block that batch.

GitHub listing requests at most 20 pages of 100 items. Pull requests count toward
pagination even though they are omitted from the returned issues. A short page
establishes completion. A full twentieth page does not prove completion and returns
an error with no partial issue result, even when the inventory might contain
exactly 2,000 items. Oversized pages, `null` arrays, invalid issue numbers and empty
titles are rejected rather than treated as absent issues.

Callers can inspect `*forge.IssueListIncompleteError` and
`*forge.IssueTitleConflictError` with `errors.As`. The latter identifies the trimmed
title and whether the conflict is in `planned` or `existing` issues. These errors
must stop mutation; retrying creation without a complete inventory can duplicate
issues. Transport errors retain their existing wrapped context.

`praetorctl issue reconcile` reconciles the repositories `--repos` names. Without the
flag it reads `forge.reconcile_repos` from the operator settings that `--fleet-config`,
`--workstation-config` and `--manifest` select ([effective policy](effective-policy.md)),
and without that the repository in the working directory, identified by
`config.ResolveRepositoryIdentity`. With none of them it refuses, as it refuses an
explicit empty `--repos=`. A bare entry such as `kit` is qualified with `--owner`, else
the working directory's `repository.owner` or origin remote owner, else
`forge.default_owner`; with no owner the entry is refused by name. No owner or
repository list is built in (`TestIssueReconcile_Positive_ScopeChain` and
`TestIssueReconcile_Negative_NoInventedOwnerOrScope` in
`cmd/standardsctl/forge_owner_test.go`).

`praetorctl issue reconcile` accepts at most 256 comma-separated repository entries
(`config.MaxReconcileRepos`, the same cap as `forge.reconcile_repos`); larger selections
fail before authentication or network reads instead of being truncated. It loads every selected repository before reconciling or applying transitions. Any listing failure returns an error naming that
repository, including in dry-run mode. It cannot report a successful fleet
reconciliation from partially fetched input. Complete inventories retain normal
dry-run and apply behavior.

## Planning sync: parents, epics and milestones

`praetorctl issue reconcile` also keeps the planning state on the forge in step with the work
that landed (issue #837). Without `--apply` every run is a dry run: it reads the issues and
milestones of each selected repository, prints every write it would make and makes none. The
first run on a repository is no exception, so existing drift is always listed before anything
repairs it:

```bash
# list the writes and the drift, write nothing (the default; --dry-run says the same)
praetorctl issue reconcile --repos=acme/example
# make the listed writes
praetorctl issue reconcile --repos=acme/example --apply
```

`--dry-run=false` still applies, as it did before `--apply` existed; `--apply` together with
an explicit `--dry-run` is refused. Applying needs a token with `issues: write`; a dry run
needs read access to issues and milestones.

### What the sync writes

The engine is the same `forge.ReconcileEngine` that unblocks dependencies
(`PlanPlanning` and `ApplyPlanning` in `internal/forge/reconciler.go`). It makes three kinds of
write, and never unticks a box or reopens an issue or a milestone:

| Write | When |
| :-- | :-- |
| Tick | A parent's task-list box names a child issue that is closed. Only that box changes; every other byte of the body is kept. |
| Close parent | An open parent carrying the `epic` or `tracking` label names at least one child, and every child is closed: each task item, and each GitHub sub-issue its `sub_issues_summary` counts. |
| Close milestone | An open milestone holds closed issues and no open one. |

A parent is an issue that carries the `epic` or `tracking` label, names a child in a task list,
or has sub-issues. A child is a task item whose text opens with the reference: `- [ ] #12`,
`- [ ] owner/repo#12` or `- [ ] https://github.com/owner/repo/issues/12`. A reference later in
the item, or inside a fenced code block, makes no child (`forge.ParseTaskItems`). A child in a
repository outside `--repos` has no known state, so its parent is never closed.

Before each write the sync reads the issue or milestone again, under its own 30-second
timeout. A box someone ticked meanwhile, a parent that lost its label or gained an open child,
and a milestone that gained an open issue are skipped with the reason, not written.

### The write log and the findings

The run summary lists every write with its outcome, then every finding:

```text
=== Planning Sync (applied): 4 writes, cap 3 | 1 findings ===
  [APPLIED] acme/example#1 tick the boxes of closed children acme/example#2, acme/example#3
  [APPLIED] acme/example#5 close parent "Tracking": every child is closed (1 task-list, 0 sub-issue)
  [SKIPPED] acme/example milestone #1 close "Done": 1 open issues since the listing; left open
  [DEFERRED] acme/example milestone #2 close "Beta": 0 open, 4 closed issues (write cap 3 reached)
  [DRIFT] acme/example#1 ticked-open-child: box ticked for acme/example#4, which is open; left ticked
```

A dry run prints `[PLAN]` where an applied run prints `[APPLIED]`; a write the forge refused is
`[FAILED]` and fails the command. `[DRIFT]` lines are drift the sync leaves as it is:

| Finding | Meaning |
| :-- | :-- |
| `ticked-open-child` | A ticked box names an open child. |
| `closed-parent-open-child` | A closed parent has an open child or open sub-issue. |
| `untracked-child` | A child lives in a repository outside `--repos`. |
| `parent-without-children` | An `epic` or `tracking` issue names no child and has no sub-issues. |
| `complete-parent-without-label` | Every child of an open parent is closed, but it carries neither label, so it stays open. |
| `empty-milestone` | An open milestone holds no issue at all; it may be waiting for its first one. |
| `unreadable-task-list` | The body exceeds 2,000 lines or names more than 100 children; the parent is not reconciled. |

### Bounds

`--max-planning-writes` caps the writes of one run: 50 by default, at most 500. Writes beyond
the cap are printed as `[DEFERRED]` and the next run makes them; a parent's close is never made
before its tick. Reads are bounded by the listings themselves: 2,000 issues and 5,000
milestones per repository, an incomplete listing failing the run before anything is planned.
A milestone that this run's parent closes empties is closed by the next run.

### Task lists, not sub-issues

The sync writes task lists and reads both task lists and sub-issue progress. A task-list line
is plain Markdown in the parent's body: it renders wherever the body is shown, the sync ticks
it with the one body edit it already makes, and GitHub ticks the box itself when the child
closes ([About tasklists](https://docs.github.com/en/get-started/writing-on-github/working-with-advanced-formatting/about-tasklists)),
so the sync is the backstop for a box GitHub did not tick. Writing sub-issues would need one
request per child against the issue's database id rather than its number
([REST API endpoints for sub-issues](https://docs.github.com/en/rest/issues/sub-issues)) and
works on GitHub alone, so it is left to a later slice. Sub-issue progress is read from the
`sub_issues_summary` every listed issue carries, at no extra request.

`needs epic --publish` writes those lines: once every child exists, the parent's body names
each one as `- [ ] #N`, ticked when the child is already closed
([migration candidates and epics](needs-capability-evidence.md#migration-candidates-and-epics)).

### The scheduled run in this repository

`.github/workflows/planning-sync.yml` runs `issue reconcile --apply` on this repository when an
issue closes and daily at 04:17 UTC, with `issues: write`, and writes the output into the run
summary. A manual dispatch is a dry run unless its `apply` input is set. Like the other
scheduled workflows it runs in the canonical repository only
([operational sync](operational-sync.md)). Adopter repositories do not get this workflow from
`praetorctl adopt` yet; adoption emits it in a later slice.

The behaviour is covered by `internal/forge/planning_sync_test.go` and
`cmd/standardsctl/issue_planning_test.go`.

## Limits

Duplicate planned titles are rejected. If an intended existing title is
ambiguous, resolve that ambiguity before syncing it. Large or incomplete
inventories require a separately reviewed lookup strategy; increasing a local
bound or treating a partial result as empty does not establish completeness.

Partial inventories and partial success summaries are not evidence of a
complete reconciliation; rerun affected checks after obtaining complete inputs.

Preflight failures cause no mutations. Later provider failures can still leave an
explicitly reported partial batch, and simultaneous independent sync processes are
not serialized. There is no distributed idempotency mechanism and no MCP
mutation tool for sync.
