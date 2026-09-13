# Released-resource garbage collection

`praetorctl gc` defaults to a read-only dry-run. Every resource is protected until
its owner explicitly releases it. Age limits control retention after release;
they do not establish whether an agent session has ended.

```sh
praetorctl gc --path . --json
praetorctl gc --path . --released-path .standards/worktrees/task-123 --json
```

Only add `--dry-run=false` after reviewing the plan and ensuring the owner has
stopped writing. Repeat `--released-path` for each resource. Paths must be exact
direct children of a configured collection pool, relative to `--path` or absolute
within that root. Missing releases, overlapping pools, outside paths, symlink
components, incomplete scans and inspection failures stop collection with a
nonzero exit status. The complete scope is preflighted before any removal.

The pools are `.standards/worktrees`, `.standards/ephemeral`, and
`.standards/tmp`. `--worktrees-dir` can select another nonoverlapping directory
inside the repository root. The Go API also accepts `EphemeralDir`. Root-level
and `bin/` files are not swept by filename. Git metadata and nested repositories
cannot be collected as artifacts.

Both `--max-age` (worktrees) and `--artifact-max-age` default to 24 hours. The
newest modification time in the complete resource determines retention. Scans
are bounded to 2,000 direct entries per pool and 50,000 entries / 10,000
directories per released resource, with at most 50,000 planned entries in total. Exceeding a bound is an error, never partial
successful discovery. Symlinks and special files in a released resource are
protected; this intentionally also protects otherwise clean symlink-containing
worktrees.

Released worktrees must be registered linked worktrees belonging to the root
repository. Primary, bare, detached, locked, prunable, dirty, ignored-only,
submodule-containing and configured-filter worktrees are refused. Effective
Git configuration is checked conservatively: even unused global filter commands
(such as Git LFS defaults) and configured attributes, excludes or filesystem
monitors currently protect the worktree. Removal uses
Git without force and preserves the branch, including unpublished commits.
`praetorctl worktree remove` has the same preservation behavior unless explicitly
forced. Administrative `worktree prune` remains a separate operation.

`--clean-go-test-cache` explicitly requests clearing the shared Go test cache;
it is off by default and only runs on an applying invocation. It does not widen
the artifact pools or authorize deletion of binaries. `SkipTestCache` still
suppresses it for API callers. GC never automatically runs Git administrative
pruning; `SkipGitPrune` remains accepted for source compatibility.

Resource paths in the report are relative to the collection root.
The JSON report separates `planned_*` fields from completed removal fields and
`reclaimed_bytes`. Dry-run completed-removal fields stay empty and actual bytes
stay zero. Byte counts describe file lengths, not allocated disk savings. A
report with `complete: false` and an error is incomplete; an apply failure can
leave earlier resources removed or the failing directory partially removed.
The error exit status must be checked even when a partial report is available.

Release is currently a caller assertion of quiescence, not a verified session
lease. Collection compares per-entry filesystem identities before removal, checks
cancellation between artifact removals, and confines filesystem access with an open root, but it does not fence another process that resumes writing.
Do not derive automatic releases from a missing PID, old mtime or expired
heartbeat. Producer registration, interrupted-state reconciliation, referenced
evidence retention and quarantine are specified in the
[shared lifecycle plan](../plans/lifecycle-management.md).
