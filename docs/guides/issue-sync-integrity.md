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

`praetorctl issue reconcile` accepts at most 256 comma-separated repository entries;
larger selections fail before authentication or network reads instead of being
truncated. It loads every selected repository before reconciling or applying transitions. Any listing failure now returns an error naming that
repository, including in dry-run mode. It cannot report a successful fleet
reconciliation from partially fetched input. Complete inventories retain normal
dry-run and apply behavior.

## Migration

Previously accepted duplicate planned titles must be consolidated. If an intended
existing title is ambiguous, resolve that ambiguity before syncing it. Large or
incomplete inventories require a separately reviewed lookup strategy; increasing a
local bound or treating a partial result as empty does not establish completeness.

Automation that tolerated offline, unauthorized or incomplete repositories during
`issue reconcile` must handle its nonzero result and obtain complete selected
inputs before retrying. Previously returned partial inventories and success
summaries are not evidence of a complete reconciliation; rerun affected checks.

Preflight failures cause no mutations. Later provider failures can still leave an
explicitly reported partial batch, and simultaneous independent sync processes are
not serialized by this change. No new forge provider, publication workflow,
distributed idempotency mechanism or MCP mutation tool is introduced.
