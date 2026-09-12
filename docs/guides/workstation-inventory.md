# Local repository observations

The workstation harvester records Git metadata for local repositories, including
repositories that have no remote. Use the existing CLI to obtain a private report:

```bash
praetorctl harvest workstation --dir /path/to/dev --json
```

The existing `standards_harvest_workstation` MCP tool accepts the same directory
as `dev_dir` and an optional `json: true`. Its directory remains confined to the
server root unless the server explicitly permits outside paths. Both adapters use
the same harvester; neither creates remotes, adopts repositories, executes project
scripts, contacts a forge, or publishes the report.

`git_common_dir` is the local identity used to associate a primary checkout with
its linked worktrees. Adding a remote preserves this identity. Moving the Git
directory can change it; it is not a portable identity across machines. Remote
metadata is an observation and does not prove ownership, visibility or permission
to publish. Keep reports in private storage: they contain workstation paths and
repository URLs even after URL credentials have been removed.

The report retains existing governance counts and adds `repository_observations`:

| Field | Meaning |
| --- | --- |
| `path`, `git_common_dir` | Checkout location and local Git identity |
| `classification` | Observed repository or worktree classification |
| `remote_state`, `remote_urls` | Successfully read and sanitized remotes, or unknown state |
| `dirty_state`, `dirty_entries` | Status availability and count; bare repositories have no working tree |
| `workingdir_tracked_state`, `workingdir_tracked_entries` | Whether the tracked-file probe succeeded and its count, without filenames |
| `workingdir_probe_ignored` | Optional ignore result for one sentinel path; not proof that every file is private |
| `probe_errors` | Bounded diagnostics for unavailable observations |

Consumers must inspect `repository_inventory_scope`,
`repository_inventory_complete` and `repository_inventory_truncated` before using
the report. The declared scope covers flat checkouts, one organization level and
the recognized worktree containers. It excludes hidden and ignored development
directories and does not claim a recursive inventory of every disk path. Failed
Git probes and unsupported remote forms remain unknown rather than proving that a
repository is local-only. Incomplete reports retain their observations. The CLI
returns nonzero and MCP sets `isError`; consumers should still read the report.

Git probes use an isolated environment and disable filesystem monitors and hooks.
When repository configuration declares a clean or process filter, dirty state is
unavailable: computing it could execute repository-supplied commands. Identity,
remote and privacy probes still run, with incomplete coverage explicitly reported.

Use these observations to select a later, explicit dogfood run. The
[configured suites](dogfood-suites.md) accept pinned public repositories and
explicit transcript inputs; an inventory does not enroll or upload a local repo.
`harvest fleet` displays static reference examples and does not establish live
remote inventory. Live GitHub or Gitea coverage requires a configured remote
inventory source and its own completion evidence.
