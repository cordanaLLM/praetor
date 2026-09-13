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

## Policy and local-only work

The version-1 JSON policy is strict. Its `enabled`, `commit_after_minutes`,
`commit_after_files`, `on_stop`, `publish`, `remote`, `base`, `repository`,
`branch_prefixes` and `require_pr` fields control this repository's behavior.
Tool events check public changes against file count or age of the HEAD commit.
Stop additionally applies `on_stop`, so a completed work chunk can be checkpointed
before its timer expires. A missing policy is explicitly disabled.

Set `publish: false` for local-only repositories: local checkpoint guidance still
runs, and no remote or forge lookup occurs. Publication requires an explicit
GitHub repository matching the configured Git remote. Missing base objects need
an explicit fetch; the checker never silently changes remote-tracking state.
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
