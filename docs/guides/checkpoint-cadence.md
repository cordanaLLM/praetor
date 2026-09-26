# Checkpoint cadence

Checkpoint cadence makes unfinished work visible in Git and draft review. The
canonical policy is `.config/agent/checkpoint.json`; the shared evaluator lives
with the vendorable Lefthook scripts. It is separate from `cadence.json`, which
records dedupe audits.

The evaluator and Lefthook jobs are shared across coding agents. Native event
adapters are client-specific; see [agent lifecycle coverage](agent-lifecycle.md)
for the tested and untested boundaries.

Run `lefthook run agent-checkpoint-tool` between work chunks and
`lefthook run agent-checkpoint-stop` before ending a turn. Inspect the returned
`due` and `actions` fields: a successful observation can report work still due.
Native adapters interpret those fields rather than treating exit zero as closure.
Malformed policy, incomplete Git observations and failed forge lookups are errors.

When due, review the diff and stage only the task's public files. Run the required
verification, then use a normal signed-off commit and gated push to an allowed
work branch. Inspect existing PRs for that head/base before creating one with an
explicit head, base and draft flag. Keep one ongoing review per branch; an
existing ready PR also satisfies review visibility. Creation and synchronization
must be read back against the actual pushed commit. A draft is a checkpoint,
not merge approval or a substitute for the pinned receipt and PR gates.

The evaluator never stages files, commits, pushes or opens PRs itself. The agent
performs those steps within session authorization, with a reviewed message and
description. This avoids assigning unrelated edits to the current task. Private
`.workingdir` data and the receipt artifact are excluded from commit reminders;
staged private data remains an error. Existing Git privacy and verification
gates continue to apply independently.

Publication and hosted acceptance are separate observations. When the optional
`require_checks` policy is true, an exact-head PR can be `publication_status:
present` while `review_status` is `failed`, `pending`, or `missing` and work
remains `due`. The agent must resolve the failures or retain an explicit blocker;
the existence of a draft cannot close that work. `passed` requires at least one
successful check, no failed or pending checks, and success for every configured
`required_checks` name. Optional skipped/neutral jobs are reported separately;
they cannot satisfy a required name. Duplicate names never hide a failure.

The check observer uses `gh pr list --json statusCheckRollup`, verified against
GitHub CLI 2.100.0. It rejects 100 or more contexts as incomplete because that
[CLI query requests the first 100 contexts](https://github.com/cli/cli/blob/v2.100.0/api/query_builder.go#L222).
Unknown states and malformed output also fail closed. This establishes a bounded
check observation, not merge approval, review approval, fork synchronization, or
downstream deployment. Those require their own evidence and authorization.

## Policy and local-only work

The version-1 JSON policy is strict. Its `enabled`, `commit_after_minutes`,
`commit_after_files`, `on_stop`, `publish`, `remote`, `base`, `repository`,
`branch_prefixes` and `require_pr` fields control this repository's behavior.
`require_checks` defaults to false for existing policies and requires both
`publish` and `require_pr`. `required_checks` accepts 1 to 64 distinct names
when enabled; an empty selection cannot identify absent required workflows.
When provided, `require_checks` must be true. Praetor enables it and names its
standards, compliance and security gates. Local-only repositories perform no
remote check requests.
The optional backward-compatible `enforce_batch_scope` field enables native
file-tool admission once due: Claude `Edit`/`Write` and Gemini `replace`/
`write_file` must name one target path, which is allowed only when it is already
in the current public dirty batch. Confined `.workingdir` paths remain allowed
for private state and evidence. Before a checkpoint is due only the payload's
shape is checked, so a target or session directory outside the repository
passes; once due, both must be inside it. This is a bounded file-scope check; it does not
classify semantic repairs, inspect shell wrappers, or cover Codex `apply_patch`.
Tool events check public changes against file count or age of the HEAD commit.
Stop additionally applies `on_stop`, so a completed work chunk can be checkpointed
before its timer expires. A missing policy is explicitly disabled.

Set `publish: false` for local-only repositories: local checkpoint guidance still
runs, and no remote or forge lookup occurs. Publication requires an explicit
GitHub repository matching the configured Git remote. Missing base objects need
an explicit fetch; the checker never silently changes remote-tracking state.
An unpublished branch or a verified local advance can request a normal push.
Live remote advances and divergent commits instead request reconciliation, even
when local HEAD has no commits beyond the base. Missing branch objects and
shallow ancestry remain explicit verification errors. The observer uses live
remote tips and available commit history; cached tracking labels alone do not
establish whether publication is due.
On the configured protected base, a clean worktree also checks live ancestry:
`base_current`, `base_local_ahead`, `base_remote_ahead`, and `base_diverged`
distinguish its states. Differences request review, never a protected-branch
push. Base equality does not establish hosted release acceptance or fork rollout.
Forge failures, ambiguous PRs and stale PR heads cannot certify review visibility.
Other forge providers need a verified observation adapter before enabling this
publication policy. Go forge PR requests now separately support the draft flag.

## Agent activation and failure handling

Codex and Claude use PostToolUse and Stop; Gemini uses AfterTool and AfterAgent.
All three invoke `.config/agent/hooks/checkpoint.py`, which requires the actual
Lefthook job's structured result. PostToolUse/AfterTool adds guidance without
replacing the completed tool's output. Stop/AfterAgent requests one continuation
for due or unverifiable work. If
the continued turn still cannot qualify, it ends with an explicit blocked reason
instead of looping or claiming completion. Repair the reported cause and resume.
Repeated periodic reminders can occur until the checkpoint is reconciled.

`praetorctl hook <client> post-tool|stop|pre-edit` runs the same two evaluator
scripts (`checkpoint.py`, `checkpoint_scope.py`) directly, with no Lefthook hop:
[agent hooks](agent-hooks.md#checkpoint-evaluators) has the full contract. It
resolves its own Python interpreter from the `hooks.python` operator setting
(`python3`, `python`, then the two-token `py -3` a stock Windows install
carries) instead of the fixed `python3` the Lefthook job names, so a host
without any of the three gets a stated skip on the tool event and a fail-closed
block on stop rather than a silent `[WinError 2]`. No client registration calls
this path yet; it exists alongside `.config/agent/hooks/checkpoint.py` and the
Lefthook jobs below until a later change re-points the tracked registrations at
it and removes the Python adapters.

Stop/AfterAgent first runs the separate `agent-state-stop` Lefthook job, even
when cadence is disabled. Its unique execution marker must confirm a valid,
fresh existing ledger through `state sync --verify`; the bridge does not silently
write a replacement snapshot. After changing the ledger or staging public work,
run `praetorctl state sync .` explicitly. Generated adoptee bundles still need
their own compatible CLI/state-job bootstrap; this repository's wiring does not
prove native activation or freshness enforcement for every adopter.

Review the exact project definitions through the client's native hook discovery
and trust flow. For Codex, `scripts/dev_codex_hooks.py` also supports inspection
and reviewed activation; it does not configure other clients. Configuration
and adapter tests establish available behavior; a live hook event establishes
activation for a specific client/session. Clients without native lifecycle
adapters can call the shared Lefthook jobs explicitly.
The fallback instruction is compiled from AGENTS.md into vendor context.
The bounded process and file handling currently targets POSIX workstations;
the native lifecycle integration is exercised on Linux.

Contracts are documented by
[Codex](https://learn.chatgpt.com/docs/hooks),
[Claude Code](https://code.claude.com/docs/en/hooks), and
[Gemini CLI](https://geminicli.com/docs/hooks/reference/).
Gemini's adapter also follows the installed CLI 0.50.0 hook runner and schema.
The adapter does not configure an autonomous timer, publish from a Git post-hook,
or bypass verification when the network or signing setup is unavailable.
