# Agent hooks

`praetorctl hook <client> <event>` is the one agent-hook entrypoint. The engine call takes
no shell substitution, no interpreter name and no flags; a hand-written registration can
contain that call and nothing else.
The command reads the client's payload from stdin, takes the workspace from the payload,
judges the call in process and answers in that client's dialect.

This page describes what ships today. The legacy command and checkpoint rows in
`.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json` and
`.config/lefthook/praetor.yml` still call the Python adapters under `.config/agent/hooks/`
and guard live sessions (see [Registrations in use today](#registrations-in-use-today)).
They move to this entrypoint after both implementations have been replayed against the same
payloads (see [Parity](#parity-with-the-python-guard)).

The subagent text rows are tracked now. `.claude/settings.json`, `.codex/hooks.json` and
`.gemini/settings.json` reach this entrypoint through the skew guard
`.config/agent/hooks/praetor_hook.py`; `.agents/plugins/praetor/hooks.json` reaches it
through the guard's copy in the plugin directory. The guard exists so that an engine older
than a row can never block a client ([Rollout](#rollout-engine-skew-never-blocks-a-client)).

## Registrations in use today

Every Python adapter finds the repository from its own file location
(`ROOT = Path(__file__).resolve().parents[3]`), so a registration only has to locate the
script. The one exception is a linked worktree of that repository, covered below. Each
client gets the form its own documentation or shipped source supports:

| Client | Registration | How the client runs it | Why this form |
| :-- | :-- | :-- | :-- |
| Claude Code | `"command": "python3"`, `"args": ["-B", "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/<script>.py"]` | Exec form: spawned directly, no shell, `${CLAUDE_PROJECT_DIR}` substituted into each element. The process runs in the session's current directory, which a Bash `cd` can move anywhere. | `${CLAUDE_PROJECT_DIR}` is "the project root where the session started" ([hooks reference](https://code.claude.com/docs/en/hooks)). Needs Claude Code 2.1.139 or later, the release that added the `args` field ([CHANGELOG](https://github.com/anthropics/claude-code/blob/main/CHANGELOG.md)); the installed 2.1.259 build has it and the substitution. An older client ignores `args` and runs bare `python3`, which reads the payload as Python source, exits 1 and so blocks nothing. |
| Gemini CLI | `python3 -B .config/agent/hooks/<script>.py` | `bash -c` on Linux and macOS, PowerShell on Windows, in the workspace root. | Gemini reads project settings only from `.gemini/settings.json` in the directory it starts in ([hooks guide](https://geminicli.com/docs/hooks/)) and spawns every hook with that directory as `cwd`, the value it also exports as `GEMINI_PROJECT_DIR` (`packages/core/src/hooks/hookRunner.ts`, checked against the installed 0.50.0 bundle). The relative path needs no quoting in either shell. |
| Codex | `python3 -B "$(git rev-parse --show-toplevel)/.config/agent/hooks/<script>.py"` | `$SHELL -lc` (else `/bin/sh`) on Linux and macOS, `%COMSPEC% /C` on Windows, in the session cwd (`codex-rs/hooks/src/engine/command_runner.rs`). | Codex exports no project-root variable. Its [hooks docs](https://learn.chatgpt.com/docs/hooks) recommend resolving from the Git root, because a session may start in a subdirectory. |

What this means in practice:

- **A drifted Claude session still runs this repository's guard.** Resolving the root from
  the session cwd gave an empty path outside any repository and the other repository's root
  inside one, so every guarded Bash, Edit and Write call failed closed until the session
  returned. `test_claude_registrations_run_from_any_session_cwd` in
  `scripts/test_checkpoint_hooks.py` runs the Claude registrations from a temporary
  directory and from a foreign repository.
- **A session inside a linked worktree is judged by that worktree.** After a Claude session
  enters a worktree, `${CLAUDE_PROJECT_DIR}` still names the checkout it started in, while
  the payload's `cwd` follows it into the worktree (hooks reference, "Worktrees are
  different"). So the start checkout's adapter runs. `session_root` in
  `.config/lefthook/scripts/common.py` compares Git common directories. When the payload
  `cwd` sits in a linked worktree of the same repository, `command_guard.py`,
  `checkpoint_scope.py` and `checkpoint.py` under `.config/agent/hooks/` run their Lefthook
  job in that worktree. That worktree's policy, ledger and dirty batch then decide the call.
  This holds in either direction, and for worktrees under `.claude/worktrees/` or anywhere
  else. A cwd Git places anywhere else keeps the adapter's own repository: no cwd, a path
  outside every repository, a registered submodule, and a foreign or nested repository. The
  due-state checks below then still deny. When Git cannot answer within the hook's time
  budget, the call is refused rather than judged by a guessed checkout.
  `test_claude_hooks_judge_a_linked_worktree_session_by_that_worktree`,
  `test_claude_hooks_started_in_a_worktree_judge_the_checkout_the_session_moved_to`,
  `test_session_root_selects_only_a_linked_worktree_of_the_same_repository` and
  `test_session_root_refuses_to_guess_when_git_cannot_answer_in_time` in
  `scripts/test_checkpoint_hooks.py` cover each case.
- **The session cwd picks the checkout, not the file a tool targets.** A worktree session
  that edits a file in the start checkout is judged by the worktree's batch, and a
  worktree's own uncommitted `lefthook.yml` and scripts judge the calls made from it. The
  earlier registrations, which resolved `$(git rev-parse --show-toplevel)` from the session
  cwd, behaved the same way.
- **Every adapter answers before its client gives up.** Each client cancels a command hook
  at its registered timeout and then lets the call through: Claude Code ("A timed-out
  command ... hook doesn't block the tool call", hooks reference, "Timeouts"), Gemini CLI
  (a timed-out hook resolves unsuccessful with no decision, `hookRunner.ts`) and Codex (a
  timed-out run is an error, never a block, `codex-rs/hooks/src/events/pre_tool_use.rs`).
  So each adapter draws every process it starts from one budget (`HookBudget` in
  `common.py`): 10 s for the before-tool guards, registered at 15 s, and 42 s for
  `checkpoint.py`, registered at 60 s. The checkout lookup takes at most 2 s per Git call
  from that budget, and a step that overruns is stopped within a short grace. A backstop
  timer (`refuse_after`) answers for the adapter at 12 s or 55 s, whatever it is still
  blocked on, such as a child in uninterruptible sleep on a stalled file system. A guard
  then exits 2; `checkpoint.py` blocks a stop and annotates a tool event.
  `test_every_adapter_answers_before_its_client_gives_up` checks this arithmetic against
  every registration, and
  `test_registered_hooks_fail_closed_before_the_client_timeout_when_git_stalls` runs the
  Claude registrations against a Git that never answers.
- **Gemini and Codex keep a fixed session directory**, so no drift is possible there.
- **Windows.** The Claude and Gemini registrations need no POSIX shell. The Codex one cannot
  run under `cmd.exe`, which has no `$( )`. Its `commandWindows` override stays unset rather
  than shipping a form no test here can run. The self-tests skip the Codex cases on Windows
  and say why. The gap closes when the registrations move to `praetorctl hook codex <event>`,
  one executable call that `cmd /c` runs. All three registrations still name `python3`,
  which the Windows runner is never given (#339).
- **The batch-scope hook judges nothing before a checkpoint is due.**
  `.config/lefthook/scripts/checkpoint_scope.py` checks only the payload's shape until then,
  so a memory or scratch file outside the repository, or a session cwd outside it, passes.
  Once a checkpoint is due, the cwd and the path must both sit inside this repository.
  Anything else denies that one call, and the next call is judged on its own.
  "Inside" is decided by filesystem identity, not by spelling: a path the client spells
  through macOS's `/var` symlink or a Windows 8.3 short name still names the checkout, while
  a symlink below the checkout is refused (`unresolved_relative_to` in
  `.config/lefthook/scripts/common.py`; covered by
  `test_registered_file_guard_matches_the_checkout_by_identity_not_spelling` in
  `scripts/test_checkpoint_hooks.py`).

## Command line and support matrix

```text
praetorctl hook <client> <event>
```

Both arguments match `^[a-z-]+$`. The registration table is the support matrix; a pair
without a row is rejected before any input is read. The one exception is engine skew,
described under [Rollout](#rollout-engine-skew-never-blocks-a-client).

| Client | Event | Native event | Matcher | Budget | Registration string |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `claude` | `pre-tool` | `PreToolUse` | `^Bash$` | 15 s | `praetorctl hook claude pre-tool` |
| `claude` | `pre-edit` | `PreToolUse` | `^(Edit\|Write)$` | 15 s | `praetorctl hook claude pre-edit` |
| `claude` | `post-tool` | `PostToolUse` | none | 60 s | `praetorctl hook claude post-tool` |
| `claude` | `stop` | `Stop` | none | 60 s | `praetorctl hook claude stop` |
| `claude` | `pre-dispatch` | `PreToolUse` | `^Agent$` | 15 s | `praetorctl hook claude pre-dispatch` |
| `claude` | `dispatch-receipt` | `PostToolUse` | `^Agent$` | 15 s | `praetorctl hook claude dispatch-receipt` |
| `claude` | `dispatch-abort` | `PostToolUseFailure` | `^Agent$` | 15 s | `praetorctl hook claude dispatch-abort` |
| `claude` | `dispatch-abort` | `PermissionDenied` | `^Agent$` | 15 s | `praetorctl hook claude dispatch-abort` |
| `claude` | `pre-handback` | `PreToolUse` | `^SubagentHandback$` | 15 s | `praetorctl hook claude pre-handback` |
| `claude` | `handback-receipt` | `PostToolUse` | `^SubagentHandback$` | 15 s | `praetorctl hook claude handback-receipt` |
| `claude` | `handback-abort` | `PostToolUseFailure` | `^SubagentHandback$` | 15 s | `praetorctl hook claude handback-abort` |
| `claude` | `handback-abort` | `PermissionDenied` | `^SubagentHandback$` | 15 s | `praetorctl hook claude handback-abort` |
| `claude` | `post-return` | `SubagentStop` | `^.+$` | 60 s | `praetorctl hook claude post-return` |
| `codex` | `pre-tool` | `PreToolUse` | `^Bash$` | 15 s | `praetorctl hook codex pre-tool` |
| `codex` | `post-tool` | `PostToolUse` | none | 60 s | `praetorctl hook codex post-tool` |
| `codex` | `stop` | `Stop` | none | 60 s | `praetorctl hook codex stop` |
| `codex` | `pre-dispatch` | `PreToolUse` | `^spawn_agent$` | 15 s | `praetorctl hook codex pre-dispatch` |
| `codex` | `post-return` | `SubagentStop` | none | 60 s | `praetorctl hook codex post-return` |
| `gemini` | `pre-tool` | `BeforeTool` | `run_shell_command` | 15 s (written as ms) | `praetorctl hook gemini pre-tool` |
| `gemini` | `pre-edit` | `BeforeTool` | `^(replace\|write_file)$` | 15 s (written as ms) | `praetorctl hook gemini pre-edit` |
| `gemini` | `post-tool` | `AfterTool` | none | 60 s (written as ms) | `praetorctl hook gemini post-tool` |
| `gemini` | `stop` | `AfterAgent` | none | 60 s (written as ms) | `praetorctl hook gemini stop` |
| `gemini` | `pre-dispatch` | `BeforeTool` | `^invoke_agent$` | 15 s (written as ms) | `praetorctl hook gemini pre-dispatch` |
| `lefthook` | `pre-tool` | job `agent-pre-tool` | none | none | `praetorctl hook lefthook pre-tool` |
| `lefthook` | `environment` | job `pre-rebase` | none | none | `praetorctl hook lefthook environment` |
| `agy` | `pre-tool` | `PreToolUse` | `*` | 30 s | `praetorctl hook agy pre-tool` |
| `agy` | `pre-dispatch` | `PreToolUse` | `invoke_subagent` | 30 s | `praetorctl hook agy pre-dispatch` |
| `agy` | `stop` | `Stop` | none | 30 s | `praetorctl hook agy stop` |

Codex carries no `pre-edit` row: it registers no native pre-edit event today. The checkpoint
rows above exist in the table and answer real calls, but none of the tracked registration
files (`.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json`) name them yet,
and Lefthook's own checkpoint jobs (`agent-checkpoint-tool`, `agent-checkpoint-pre-edit`,
`agent-checkpoint-stop`) still call the Python adapters directly; both flips are a later
change (see [Not in this change](#not-in-this-change)).

The registration string in the table is the engine call: one executable call that resolves
through `PATH` (and `PATHEXT` on Windows) and is valid under `sh -c` and under `cmd /c`.
The tracked client files of this repository carry the same pair behind the skew guard
instead. The three native client files use the form of their legacy adapter rows; the AGY
plugin names its own copy of the guard:

```text
python3 -B "${CLAUDE_PROJECT_DIR}/.config/agent/hooks/praetor_hook.py" claude pre-dispatch
python3 -B "$(git rev-parse --show-toplevel)/.config/agent/hooks/praetor_hook.py" codex pre-dispatch
python3 -B .config/agent/hooks/praetor_hook.py gemini pre-dispatch
python3 -B praetor_hook.py agy pre-dispatch
```

Claude Code rows use `CLAUDE_PROJECT_DIR`, the tree the settings were loaded from, which
stays fixed when the session enters a worktree that may predate the guard. Codex runs hooks
from the session directory and its documentation recommends resolving paths from the Git
root. Gemini CLI runs every hook from the project directory it started in, so its row names
the guard relative to that directory, as its legacy adapter rows do; the path needs no
quoting or substitution in bash or PowerShell.

AGY runs a hook command through `sh -c` or `cmd /c` from the directory that holds
`hooks.json` (the "Hook Handler Fields" section of the contract embedded in the installed
AGY binary). A plugin found in a workspace runs from `.agents/plugins/praetor/`; a plugin
installed for a user is a copy under the AGY configuration root, outside any checkout
(`~/.gemini/config/plugins/praetor/`, measured on a Linux host with AGY 1.2.11, whose
changelog resolves plugin variables to "the final installation directory"). So the plugin
carries its own copy of the guard, `.agents/plugins/praetor/praetor_hook.py`, and its row
names that file relative to the plugin directory. Both copies sit three directories below the checkout root,
so inside a checkout the plugin copy finds the same `bin/praetorctl`.
`TestPluginLauncherIsTheTrackedLauncher` fails when the copy differs from the canonical
guard by one byte. `TestRegistrationTableMatchesTheTrackedClientFiles` and
`TestAgyDispatchRegistrationMatchesTrackedPlugin` pin each tracked string to its form.

## One invocation

1. **Arguments.** Grammar, then the table. A failure prints the usage with every supported
   pair and exits 2, the blocking code of the native clients. An event that no row of this
   engine carries, named for a `claude`, `codex`, `gemini` or `lefthook` registration, is
   a stated skip instead (`unsupportedResponse`, `internal/agenthook/hook.go`).
2. **Input.** Every payload-carrying event reads stdin up to 1 MiB within the evaluation
   budget, so a client that never closes stdin cannot hold the hook. `environment` reads
   nothing.
3. **Decode.** Exactly one JSON object of valid UTF-8. The dialect reads
   `hook_event_name`, `tool_name`, `cwd` and `tool_input.command`. The command-line event is
   authoritative: a payload that names another event is an error. A member of the wrong
   type is an error, never a silent default.
4. **Classify.** A tool in the dialect's command-tool list needs a nonempty
   `tool_input.command`. A payload without `tool_name` is treated as a command tool,
   because the registration matcher already selected it. Any other tool is allowed. The
   `lefthook` dialect treats every tool as a command tool, as the Python guard does. `agy`
   decodes and encodes a different shape entirely; see
   [The agy (Antigravity) dialect](#the-agy-antigravity-dialect).
5. **Workspace.** `cwd` from the payload, else the process working directory. It must be
   absolute. The repository root comes from the isolated Git probe
   (`git rev-parse --show-toplevel` without inherited Git variables), so an ambient
   `GIT_DIR` cannot redirect the answer.
6. **Governed.** The root holds `.standards.yaml`. Anything else is skipped with a reason,
   unless `hooks.scope` is set to `all`: that setting makes the command policy run in an
   ungoverned workspace too, instead of skipping it
   (`agenthook.AppliesEverywhere`, `internal/agenthook/evaluate.go`). The default,
   `governed`, is the behavior described in the rest of this step.
7. **Judge.** The process environment first, then the command policy.
8. **Encode.** The verdict in the client's dialect.

## Subagent text register gate

The agent-traffic events reuse the same bounded stdin, workspace resolution, record mode and
dialect encoder as command hooks. `pre-dispatch` extracts the brief's `task:` field through
the Caveman scanner, verifies that routing declares the label, resolves
`register.tasks.<label>`, and calls the shared runtime validator with kind `brief`. An
internal result must pass Caveman; docs and social results remain full prose by policy.
The label and the manifest resolve through `compiler.LoadRegisterTaskAuthority`, the same
digest-bound `config.RegisterAuthority` snapshot that `compile-context` renders, so every
resolution names the manifest SHA-256 that `config.ValidateEmission` requires
(`internal/agenthook/agent_brief_authority_test.go`).

Claude's pre-tool hook stores only the resolved register row, never the prompt. Its
post-tool receipt atomically binds that row to the native agent id. The private bounded
store lives below Git's shared directory at
`$GIT_COMMON_DIR/praetor/agenthook-correlations`, so an isolated agent worktree reaches the
same correlation as its parent without adding working-tree state. Failed and auto-mode
denied launches remove their pending row. A pending row otherwise expires after five
minutes; an active agent row expires after 24 hours.

The store holds at most 128 rows (`MaxCorrelationEntries`,
`internal/agenthook/correlation.go`). A launch at that cap evicts the least recently
written row instead of refusing, so bindings leaked by agents killed before `SubagentStop`
never shut off later launches. An evicted agent is unowned, so its return is a stated skip.
Each sweep also removes an atomic-write temporary file older than the two-minute lock lease,
which a writer killed mid-write leaves behind. Only rows count against the cap: temporary
files and entries Praetor did not write do not, and the sweep leaves the latter alone. One
sweep reads at most 1,024 directory entries (`correlationScanLimit`); a directory with more
fails closed with that bound in the error rather than being read in part. To reset the store
by hand, delete the `praetor/agenthook-correlations` directory under the path
`git rev-parse --git-common-dir` prints. Every bound agent then reports as unowned, and the
next launch creates the directory again (`TestCorrelationStoreEvictsOldestAtCapacity`,
`TestCorrelationStoreFullOfRowsSurvivesDebris`, `TestCorrelationStoreScanBoundIsNotTheRowCap`,
`TestClaudeDispatchSurvivesAFullStoreOfLeakedBindings`).

Claude Code 2.1.271 and later can deliver an auto-mode report through the documented
`SubagentHandback.tool_input.message` field. The `pre-handback` hook checks that message
before delivery, using the `session_id`, `agent_id`, and `tool_use_id` supplied to subagent
tool hooks. It stores only a digest binding that tool use to the validated report.
`handback-receipt` marks delivery after the tool succeeds and only when the delivered input
matches that digest. Failure or permission denial clears only the matching provisional
digest, so a stale event cannot alter a newer attempt. `SubagentStop.last_assistant_message`
remains the checked fallback when delivery did not complete. After a confirmed handback,
`SubagentStop` removes the binding without treating its optional closing text as the report. See the
[Claude Code hook reference](https://code.claude.com/docs/en/hooks#subagentstop).

Claude rejects an explicitly foreground `Agent` call because its return can precede the
dispatch receipt needed for correlation. An omitted background flag keeps the client's
documented background default, which is background only from Claude Code v2.1.198
(`tool_response.status` under the
[Agent tool input](https://code.claude.com/docs/en/hooks#agent) of the hook reference). On an
older client, an omitted flag runs the agent in the foreground. Its receipt then reports
`completed`, which the receipt hook refuses, the pending row expires after five minutes, and
the agent's return is an unowned skip. Set `run_in_background: true` on those clients.

A deny at `SubagentStop` blocks nothing: Claude Code and Codex keep the subagent running
and hand it the reason as its next instruction (exit 2 table and `SubagentStop` decision
control in the [Claude Code hook reference](https://code.claude.com/docs/en/hooks#subagentstop);
[Codex hooks](https://developers.openai.com/codex/hooks)). `SubagentStop` also fires for
Claude Code's internal agents, such as prompt suggestions and `/btw` side questions. The
return boundary therefore denies only a register violation in a Praetor-owned return, which
the subagent can rewrite, and a payload it cannot decode, which fails closed:

- The Claude row's matcher `^.+$` never matches an empty `agent_type`, so internal agents of
  a session without a named agent never reach the hook.
- An agent without an active binding is not Praetor-owned: an internal agent under a named
  session agent, an SDK or foreground launch without a dispatch receipt, a completed agent
  the parent resumes, or an expired binding. Its `SubagentStop`, `pre-handback`,
  `handback-receipt` and `handback-abort` calls report `no Praetor-owned dispatch binds this
  agent` as a skip (`unownedAgent`, `internal/agenthook/agent_traffic.go`).
- A correlation store failure at `SubagentStop` is a skip that names the fault
  (`subagent return not judged: …`). The pre-launch gate still fails closed on the same fault.
- `stop_hook_active` bounds the retry: once a stop hook has continued the subagent, a second
  deny becomes a skip and the binding is released, so it cannot outlive the agent. A later
  resume of that agent is unowned, like any completed agent (`evaluateAgentReturn`,
  `internal/agenthook/agent_traffic.go`; `returnBoundary`, `internal/agenthook/evaluate.go`).
  The bound also covers a payload that fails to decode, such as one without `agent_id`:
  `Dialect.Decode` keeps its `stop_hook_active` flag, so the first stop is denied as
  `Invalid hook input` and the continued one is a skip. Only a JSON `true` counts; an
  absent or non-boolean flag keeps the deny.
- Codex enforces no register at this boundary, so every Codex `SubagentStop` result is a
  skip, including a null `last_assistant_message`.

`internal/agenthook/agent_return_boundary_test.go` replays each case.

Codex exposes both the dispatch brief and `SubagentStop.last_assistant_message`, so its
brief and return capture adapters are tracked and replayed. The current spawn
`PostToolUse` payload does not expose a documented key that links the tool call to the
later subagent id. Praetor therefore does not infer that ownership: the return hook checks
the bounded body shape and reports an explicit skip, while `register_enforcement` remains
`unenforceable`. No unusable pending correlation row is stored.

Gemini's documented `invoke_agent` input exposes the prompt, so Praetor gates its brief.
The generic hook schema does not document the nested field containing that tool's returned
report, and no redacted native recording is tracked. Gemini return capture and register
enforcement therefore remain `unenforceable`; Praetor does not install an `AfterTool`
return gate based on an inferred shape. AGY's documented `invoke_subagent` input exposes
one to 64 `Subagents[].Prompt` values and therefore gates briefs. Its documented post-tool
and stop payloads expose no subagent return body, so AGY return capture and full register
enforcement are also `unenforceable`. OpenCode v1, Continue, Cline, and Kilo have no
audited native text boundary and report all three states as `unenforceable`.

`clients capabilities` reports `brief_capture`, `return_capture`, and
`register_enforcement` independently. A tracked, replay-tested adapter is
`adapter-defined` with activation `unverified`; only a redacted payload recorded from the
installed native client may promote that activation to observed. Static configuration is
never that proof. `return_capture` proves that the body can be decoded, not that the task
register can be recovered; the separate `register_enforcement` state carries that claim.
Main-agent `Stop` and Gemini `AfterAgent` keep their checkpoint purpose: no Caveman hook is
registered on a human-operator reply surface.

## Verdicts

| Situation | `claude`, `codex`, `gemini` | `lefthook` |
| :-- | :-- | :-- |
| unsupported or malformed arguments | exit 2, usage on stderr | exit 2, usage on stderr |
| well-formed pair no row of this engine carries (engine skew) | exit 0, `praetor hook: this praetorctl serves no <client> <event> row: …, skipped` on stderr | same |
| stdin missing, empty, over 1 MiB, not one JSON object, late | exit 2, reason on stderr | exit 1, reason on stderr |
| payload event contradicts the argument | exit 2 | exit 1 |
| command tool without a command string | exit 2 | exit 1 |
| workspace relative, missing, or Git cannot answer | exit 2 | exit 1 |
| no repository | exit 0, `praetor hook: no repository, skipped` on stderr | same, and no marker |
| repository without `.standards.yaml`, `hooks.scope: governed` (default) | exit 0, `praetor hook: workspace not governed, skipped` on stderr | same, and no marker |
| repository without `.standards.yaml`, `hooks.scope: all` | policy still evaluated (see below) | same |
| environment check fails | exit 2 | exit 1 |
| command matches a policy rule | exit 2, `[BLOCKED BY <rule>] …` on stderr | exit 1, same text |
| allowed | exit 0, both streams empty | exit 0, `PRAETOR_COMMAND_POLICY_OK` on stdout for `pre-tool`, nothing for `environment` |

A skip is a neutral allow that always states its reason; it never prints the marker,
because no policy was evaluated. Reasons are bounded to 4096 bytes and never echo the
command, which may carry a secret. A deny keeps its exit code even when the client has
already closed a stream.

## Rollout: engine skew never blocks a client

An engine installed before a row existed answers that row with its usage and exit 2. Claude
Code, Codex and Gemini CLI all document exit 2 as a block. Claude Code and Gemini CLI
document every other exit code as a non-blocking error: they report it and the action
proceeds ([Claude Code hook reference](https://code.claude.com/docs/en/hooks),
[Gemini CLI hooks](https://geminicli.com/docs/hooks/), both fetched 2026-09-26), so a
missing command, the shell's status 127, blocks nothing either. The Codex hook documentation
([Codex hooks](https://developers.openai.com/codex/hooks), fetched 2026-09-26) describes only
exit 0 and exit 2, so how Codex treats exit 1 or status 127 is unverified. The same page
treats exit 0 with no output as success, yet says `SubagentStop` expects JSON on stdout when
it exits 0, so whether Codex accepts an empty-stdout skip on the `codex post-return` row is
unverified as well. AGY documents no exit codes at all, only the decision object on stdout;
a report on the Google AI developer forum says a nonzero `PreToolUse` exit blocks the tool
call.

So an engine older than the subagent rows would stop every Claude `Agent`, Codex
`spawn_agent`, Gemini `invoke_agent` and AGY `invoke_subagent` launch, and keep a Claude or
Codex subagent that reaches `SubagentStop` running, until someone reinstalled. Two layers
keep that from happening.

**The skew guard.** The tracked client files of this repository call
`praetor_hook.py <client> <event>`, not `praetorctl` from `PATH`. The engine that is already
installed cannot be changed, so the check runs before any engine sees the call. Every engine
version prints the pairs it serves when `praetorctl hook` runs with no arguments. The guard
asks each candidate in order and hands the call, stdin included, to the first that lists the
pair:

1. `bin/praetorctl` of the checkout the guard belongs to. `make hook-cli` builds it from
   that tree, and the Git hooks rebuild it after a checkout or merge that changes Go sources,
   so it serves the rows the tree registers. Outside a checkout, as in an installed AGY
   plugin, there is no such candidate.
2. `praetorctl` from `PATH`, the installed engine.

The chosen engine's stdout, stderr and exit code reach the client unchanged, so a deny stays
a deny. When no candidate lists the pair, or none exists, the guard drains the payload and
exits 0 with the reason on stderr:

```text
praetor hook: no engine serves claude pre-dispatch (checked /home/example/.local/bin/praetorctl); gate unenforced until bin/praetorctl rebuilt (make hook-cli) or engine reinstalled (make dev-install), skipped
```

For AGY, the same skip also prints the answer the engine's agy encoder gives a skip:
`{"decision":"allow"}` for `pre-tool` and `pre-dispatch`, `{}` for `stop` (`PROCEED` in the
guard; `agyEncodePreTool` and `agyEncodeStop` in `internal/agenthook/dialect_agy.go`). An agy
event without a known answer is a malformed argument. The drain reads the payload from file
descriptor 0 in a daemon thread and gives up after two seconds, so a client that keeps stdin
open still gets exit 0.

An engine that starts but gives no verdict within 45 seconds, or cannot start after its
probe, is exit 1 for the native clients, which Claude Code and Gemini CLI document as a
reported, non-blocking error; AGY gets its allow answer and exit 0 instead. The 45 seconds
(`RUN_TIMEOUT`) outwait every engine budget of a guarded row, 10 s for the dispatch and
handback rows and 30 s for `post-return`, so a slow engine still gives its own verdict and a
late deny stays a deny. Both 5 s probes plus that wait end before the 60 s `post-return`
rows give up (`TestLauncherTimeoutsOutwaitEngineBudgets`). On a shorter row the client's own
timeout comes first: 15 s for the dispatch and handback rows, and 30 s for AGY, which
documents nothing about a timed-out hook. Malformed guard
arguments keep exit 2, as the engine does for malformed arguments; the tracked strings are
pinned, so that only happens on a broken edit. A host without `python3` gets the shell's
command-not-found status, which blocks nothing in Claude Code or Gemini CLI and is
unverified for Codex and AGY. `scripts/test_praetor_hook.py` runs the tracked strings through
`sh -c`, the AGY row from inside the checkout and from an installed copy, against the engine
built from the tree, against a stand-in for the engine that predates these rows (it is only
ever probed), and with no engine at all. It also checks that the guard's AGY answers equal
the built engine's skip answers.

**The engine's own skip.** An engine built from this change on answers a well-formed pair it
has no row for as a stated skip in the client's dialect, whether the event is new or an
existing event gained a row for that client. That covers registrations the guard does not
front, such as a hand-written row. Tracked registration strings are pinned to the table in
both directions: `TestRegistrationTableMatchesTheTrackedClientFiles` checks every row against
the files, and `TestTrackedRegistrationsNameOnlyEngineRows` checks that every tracked engine
command is a row. So a runtime pair this engine does not know means skew, not a typo
(`TestRunSkipsAnEventNewerThanTheEngine`, `TestHookProcessSkipsAnEventNewerThanTheEngine`).
Malformed arguments, an unknown client and an unknown `agy` event keep the usage and exit 2:
the agy encoder has no response shape for an event it does not know.

**What a skip costs.** Either layer skips, so a skewed host does not enforce the gate. Keep
the engine current: after pulling a change that adds rows, reinstall before starting a
client session, then check what the installed engine serves.

```bash
make dev-install            # or: praetorctl workstation install --source <checkout>
praetorctl workstation status
praetorctl hook             # lists every pair; after this change it names claude post-return and agy pre-dispatch
```

`status` reports the installed commit ([Workstation install and status](workstation-update.md)).
Nothing stops a downgrade: `workstation install` records the prior commit in its manifest but
never compares it with the commit it installs (`internal/workstation/install.go`), and
`make dev-install` runs the installer from the source checkout (`scripts/dev_install.py`).
Installing from a checkout based before these rows puts back an engine that neither serves
nor skips them. The tracked clients then fall through the guard to a skip, so nothing blocks,
but nothing is enforced either until the next forward install.

An AGY plugin installed before this change still carries the bare
`praetorctl hook agy pre-dispatch` row, if it carries a `hooks.json` at all. Reinstall it from
the checkout so that it carries the guard's copy too.

## Built-in command policy

The rules are the ones of the Python guard, ported one to one. They judge the command
text. A command is denied when it

- passes the Git option that skips the commit or push hooks, in its long form anywhere or
  in its short form on a commit: alone, inside a bundle of argument-free flags (`-an`,
  `-sn`, `-name`; gitcli(7) bundles short options), and after Git's global options
  (`git -C dir commit`, `git -c key=value commit`);
- disables Lefthook inline for one command (`0` or `false`, the two values Lefthook
  honours), or sets the skip variable in front of a Git call;
- sets `core.hooksPath` to anything, with `=` or a space-separated value, per command or
  in config (reading it stays allowed);
- removes, moves, copies over, rewrites in place, re-permissions or redirects into the
  repository's hooks directory, in either path separator, or runs `lefthook uninstall`
  (listing, printing or searching the directory stays allowed);
- runs `adopt`, `conform`, `bootstrap` or a `needs` scan, report, migration or epic
  against the workstation dev root instead of a leaf repository (DEV-01).

The sources live in `internal/agenthook/policy.go` (`builtinEvasion`, `builtinDevRoot`).
`.config/agent/hooks/block_evasion.py` carries the evasion list and the dev-root rule byte
for byte (`TestPythonGuardCarriesTheBuiltinEvasionList`,
`TestPythonGuardCarriesTheBuiltinDevRootRule`), and `praetorctl adopt` renders the
interceptor it writes from `agenthook.BuiltinRules` (see
[The adopted interceptor](#the-adopted-interceptor)). The allow and deny cases, including
the false-positive ones, are in `internal/agenthook/testdata/pre-tool/cases.json`.

The environment check denies when the hook process itself runs with Lefthook disabled
(`LEFTHOOK=0` or `LEFTHOOK=false`, compared exactly, as lefthook v2.1.14 does in its
[command runner](https://github.com/evilmartians/lefthook/blob/v2.1.14/internal/command/run.go))
or with a Lefthook exclusion or skip list. It runs for `environment` and before every
`pre-tool` judgement.

### The adopted interceptor

`praetorctl adopt` writes `.config/agent/hooks/block_evasion.py` into the adopted
repository for direct wiring as a shell-tool `PreToolUse` hook. It is rendered from the
engine's built-in rules and environment lists (`buildBlockEvasionPY`,
`internal/adopt/hooks.go`) and never carries operator rules such as organisation
containers; those belong in `hooks.command_policy.deny`. It reads at most 1 MiB of stdin
and blocks with exit 2 on any input that is not one JSON object with a nonempty
`tool_input.command`, including empty stdin, a JSON array and other clients' native shapes,
which are the dialects `praetorctl hook` owns. `internal/adopt/evasion_hook_test.go` replays
the corpus above against the rendered script.

Both Python scripts, the adopted interceptor and praetor's own guard, refuse a command over
the scan bounds instead of scanning it: more than 65,536 characters in all, or one line over
2,048 characters (`agenthook.MaxScanChars`, `agenthook.MaxScanLineChars`). Python's `re`
backtracks, so the find and `sed -i` rules cost cubic time in the length of one line and a
16 KiB line held the find rule for 16 s, long enough to outlive a client's hook timeout. A
command is refused, never truncated, because a truncated scan allows what lies past the cut;
split it, or write the long content to a file first. Within both bounds the slowest rule
answers in under a second (`TestEmittedInterceptorScanBounds`,
`test_guard_answers_the_slowest_admitted_commands_inside_the_bound` in
`.config/lefthook/scripts/test_hooks.py`). The Go policy uses RE2, which is linear, and has
no such bound; `TestPythonGuardCarriesTheScanBounds` keeps praetor's guard on the same
numbers. The Git global-option prefix of the commit rule parses one way only, so the corpus
cases `allow-directory-options-without-commit` and
`allow-directory-options-split-by-carriage-returns`, which took the earlier rule minutes,
are answered at once.

Organisation container names are operator data, not engine data. The policy accepts a
bounded operator deny list (RE2, at most 64 patterns of at most 512 bytes; an empty,
oversized or non-compiling pattern fails the whole list). `hooks.command_policy.deny`
is read from the operator settings and merged into the built-in rules before every hook
call: `runHook` resolves the section through the operator-settings loader every command
shares (`defaultOperatorSettingsFlags` in `cmd/standardsctl/operator_settings.go`: the
environment, then the install manifest, since hook takes no flags), then builds the policy
with `agenthook.BuildPolicy(settings.Hooks)` (`cmd/standardsctl/hook.go`,
`internal/agenthook/settings.go`). See [checkpoint evaluators](#checkpoint-evaluators)
below for the layering `hooks.command_policy.deny` shares with `hooks.scope` and
`hooks.python`.

### Organisation folders (DEV-01)

DEV-01 keeps every repository inside an organisation folder under the dev root. The
built-in hook rule refuses only the dev root itself; neither the Go policy nor praetor's
Python guard names an organisation folder, so `praetorctl adopt ~/dev/acme` passes both
(`deny-organisation-container` in the corpus is an `operator: true` case). An operator who
also wants a folder root refused adds a pattern of the shape the replay uses
(`organisationContainerPattern` in `internal/agenthook/support_test.go`) to their own
settings:

```yaml
hooks:
  command_policy:
    deny:
      - '(?i)(standardsctl|praetorctl)\s+(adopt|conform|bootstrap|needs\s+(scan|report|migrate|epic))\b.*/dev/(acme|acme-labs)/?(\s|$)'
```

`praetorctl topology audit` and `topology clean` recognise organisation folders without a
hook rule. A dev-root directory is a container when its name, compared lowercased, is one
of the built-in names (`upstream`, `local`, `stacks`, `worktrees`, `scratch`;
`topology.BuiltinOrgContainers`) or is listed in `topology.org_containers`, read through
`--fleet-config`, `--workstation-config` and `--manifest` or the settings environment
(`cmd/standardsctl/topology.go`). Whatever its name, a directory that is not a repository
itself and directly holds at least one child repository is a container too
(`topology.HoldsChildRepository`). A submodule or linked worktree whose gitlink resolves
inside the directory's own `.git` belongs to that directory's repository and is not a
child repository, so a dev-root repository with a lost `HEAD` stays a DEV-01 violation.
The scan reads at most `MaxScanEntries` entries. A listing it cannot read truncates the
audit, which then blocks cleanup. A listing cut at the bound before a repository turns up
adds a line under `Notes` and leaves the folder out of the containers, so a large data
folder does not fail the audit. Configure a name when structure cannot reveal the folder,
for example one that is itself a repository or holds no valid child repository yet.
Cleanup never deletes a recognised container, any directory that holds a repository, or
headless `.git` metadata that still holds a non-empty `modules` or `worktrees` directory;
the last is reported for manual review (`internal/topology/containers_test.go`).
`praetorctl adopt` refuses a dev-root folder with a built-in name or a child repository as
an organisation directory (`internal/adopt/validate.go`).

## Record mode

Set `PRAETOR_HOOK_RECORD_DIR` to a private, writable directory and every hook call, for
any registered client and event, writes its raw bounded stdin verbatim to one new file
under that directory instead of judging it, then answers a neutral allow in the client's
own dialect. `environment` reads no stdin, so a recorded `environment` call writes
nothing. A read or write failure denies closed rather than silently dropping the payload.

Each file is named `<client>-<event>-<UTC timestamp>-<random suffix>.json` and written
`0600` (owner read-write only); the directory is created `0700` if it does not exist. The
random suffix only guards against a same-nanosecond collision; the timestamp already
makes one exceedingly unlikely on its own.

Record mode exists to build a fixture a client's own docs do not cover — the exact
process shell and working directory, real exit-code behaviour, an argument key besides
the ones a docs page states, an edit-tool name — from one real session, without reading
or writing that session's own operator configuration (`~/.gemini`, `~/.codex`, or
equivalent). A recorded file is operator-local by construction (it may carry a real
workspace path or other local detail) and is never committed as-is: redact it by hand
into a tracked fixture under `internal/agenthook/testdata/<client>/recorded-<os>/*.json`
(HISS-20) before it backs a test.

No fixture under `internal/agenthook/testdata/agy/` was produced this way yet (see
[The agy (Antigravity) dialect](#the-agy-antigravity-dialect)); only `PRAETOR_HOOK_RECORD_DIR`
itself is exercised by tests today, against synthetic payloads.

## The agy (Antigravity) dialect

agy's hooks.json contract shares nothing with the four dialects above: no
`tool_input`/`cwd`/`hook_event_name` shape, and a JSON decision object on stdout instead
of an exit code and an optional marker line. `Dialect` carries an optional decode/encode
override for exactly this case (`internal/agenthook/dialect_agy.go`); the generic decoder
and encoder above are untouched and still serve claude, codex, gemini and lefthook.

Everything below is **docs-confirmed**: read from the "Lifecycle Hooks (`hooks.json`)"
contract that ships embedded in the installed Antigravity CLI binary itself (`agy --help`
prints the subcommand list; the contract text is a string literal inside the binary,
verified on the installed 1.2.7 build, not taken from training memory or from
`.workingdir/research/antigravity-docs-20260917.json`, which this text supersedes for
everything it covers). It is not a live-recorded fixture; see the note at the end of this
section for what remains unverified.

<!-- praetor:docs-references:off .agents/hooks.json is Antigravity's own workspace hook file in a governed repository; this repository ships only the plugin copy, .agents/plugins/praetor/hooks.json -->

Hooks are configured in `.agents/hooks.json` (or a plugin's own copy), one JSON object
keyed by hook name, each hook naming one or more of `PreToolUse`, `PostToolUse`,
`PreInvocation`, `PostInvocation` and `Stop`. `PreToolUse`/`PostToolUse` groups carry a
`matcher` regex against the tool name; the other three are a flat handler list with no
matcher. A handler's default (and today, only supported) `type` is `"command"`, run via
`sh -c` on Unix and `cmd /c` on Windows, in the directory containing `hooks.json`, with a
default 30 second timeout. `PreToolUse` serves both command policy and the
`invoke_subagent` brief gate; `Stop` keeps its existing evaluator. `PostToolUse`,
`PreInvocation` and `PostInvocation` have no AGY return-body enforcement because the
public payload contract does not expose that body.

<!-- praetor:docs-references:on -->

Every payload carries `conversationId` and `workspacePaths` (an array; the entrypoint
uses element 0 and falls back to the process working directory when the array is empty
or absent, the same rule as `cwd` for the other four dialects).

**`PreToolUse`** — gate, block or audit a tool call before it runs:

```json
{"toolCall": {"name": "run_command", "args": {"CommandLine": "npm test"}}, "stepIdx": 19}
```

`run_command` is the one docs-confirmed command tool; its argument key is `CommandLine`
(capitalised, unlike every other dialect's lower-case `command`). A `run_command` call
without a nonempty `CommandLine` denies. Any other tool name is allowed without
inspection: the docs name `run_command` and `view_file` as *matcher* examples only, never
as a full call, so there is no docs-confirmed "edit tool" list yet (decision row 7 of the
rollout spec) — an edit-classification path stays unbuilt rather than guessed, and a real
edit tool call is simply allowed today, same as any other unclassified tool. Response:

```json
{"decision": "allow"}
{"decision": "deny", "reason": "..."}
```

The full contract also has `"ask"` and `"force_ask"` decisions and an `overwrite` key;
nothing here produces them, since the command policy only ever allows or denies.

**`Stop`** — let the agent stop, or force it to keep going:

```json
{"executionNum": 1, "terminationReason": "model_stop", "fullyIdle": true}
```

Response `{"decision": "continue", "reason": "..."}` blocks the stop and re-enters the
loop; any other value (this entrypoint always answers `{}`) lets the agent stop. The
checkpoint evaluators below (state-ledger verify, then the due check) are wired for
claude, codex and gemini's `stop`, not for agy's: `checkpointWired` in
`internal/agenthook/evaluate.go` names the three clients it covers, so the only way
`agy stop` denies today is still a payload the entrypoint could not read or a workspace
it could not resolve — and per the rollout spec's verdict-rules table (3.4), that only blocks **once**
per conversation: `Canonical.StopActive` is `executionNum > 1`, and a Stop verdict answers
`{"decision":"continue",...}` only while `StopActive` is false. A skip (ungoverned
workspace) is neutral and always answers `{}`, never blocks.

**Unverified** (decision row 7 again): whether `executionNum` is 1-based (assumed here
from the docs' own un-labelled example, matching how `stepIdx` and `invocationNum` are
shown above zero too, never stated as zero-based), the real process shell/cwd/exit-code
behaviour, any argument key besides `CommandLine`, and any edit-tool name. A redacted
native AGY record is still absent, so none of these docs-derived fields counts as observed
activation. Record mode above remains the qualification path.

## Checkpoint evaluators

`pre-edit`, `post-tool` and `stop` call the two existing checkpoint evaluators directly, in
the governed repository root, with no Lefthook hop:

| Event | Script | Arguments / stdin | Marker |
| :-- | :-- | :-- | :-- |
| `pre-edit` | `.config/lefthook/scripts/checkpoint_scope.py` | stdin: `{"hook_event_name", "tool_name", "tool_input":{"file_path"}, "cwd"}` normalised from the decoded payload | `PRAETOR_CHECKPOINT_SCOPE_OK` |
| `post-tool` | `.config/lefthook/scripts/checkpoint.py` | `--event tool --json --marker` | `PRAETOR_CHECKPOINT_RESULT=<json>` |
| `stop` | same script | `--event stop --json --marker`, after `state.VerifyStateSync` passes | same |

Neither script changes; the marker contract is the one the Lefthook job already relied on
(exactly one marker line, JSON that satisfies `schema_version: 1`, and a `due` result that
never comes back without `actions`). Reading the wrong number of marker lines, invalid JSON,
or a timeout is treated as the evaluator failing, with the outcome below.

**Interpreter resolution.** `hooks.python` (a layered operator setting) names the candidates,
tried in order through `PATH`: the built-in default is `python3`, then `python`, then the
two-token `py -3` for a stock Windows install. Each candidate is a bare command or, in the
workstation layer only, a clean absolute path, with up to eight of its own literal arguments
(`-X utf8`, for example); `-B` (no `.pyc` writes) is always appended. `hooks.scope` (the
`pre-tool` gate) and `hooks.command_policy.deny` (the command policy) come from the same
`hooks` settings section (`internal/config/operator_sections.go`); a fleet or workstation
document named by `PRAETOR_FLEET_CONFIG` / `PRAETOR_WORKSTATION_CONFIG` or the install
manifest is merged through `config.LoadOperatorSettings` before every hook call, with the
built-in defaults (`python3`, `python`, `py -3`, governed scope, no operator deny patterns)
when neither is configured. See [effective policy](effective-policy.md) for the layering
model this section shares with `complexity`.

**No interpreter resolves.** `pre-edit` and `stop` fail closed (deny); `post-tool` can only
annotate, so it is a stated skip. A `stop` deny after a Python failure reads `checkpoint
evaluator unavailable: no Python interpreter` the same way a due checkpoint reads `Praetor
checkpoint due: …`; both are `[BLOCKED BY HISS]` on `claude`/`codex`/`gemini`.

**`stop` state verification.** Before the checkpoint itself, `stop` calls the Go state-sync
verifier (`internal/state.VerifyStateSync`, the function behind `praetorctl state sync
--verify .`) against the governed root. A missing, stale or unverifiable `.workingdir` ledger
blocks on its own, independent of whether a checkpoint is due; the reason names the repair
(`praetorctl state sync .`).

**`stop_hook_active`.** The client's own `Stop`/`AfterAgent` payload can mark a second,
repeated pass. The Go evaluator already carries that flag on the canonical payload
(`Canonical.StopActive`) and changes its wording on a repeat block accordingly, but no
tracked dialect populates it from a real payload yet (`dialect.Decode` reads
`hook_event_name`, `tool_name` and `cwd` only); a follow-up that extends `Decode` is needed
before a live client sees the distinct repeated-pass wording.

## Platform notes

The command is one Go binary and needs `git` on `PATH`; nothing else. It does not need a
POSIX shell or a Python interpreter on Linux, macOS or Windows. Git answers with forward
slashes on Windows; the root is converted to the native separator before it is used.
Where `git` is absent the tests skip with that reason, and the command itself denies,
because it cannot tell a governed workspace from an ungoverned one.

## Parity with the Python guard

The JSON-expressible payloads of the Python suites
(`.config/lefthook/scripts/test_hooks.py`) live in one fixture file,
`internal/agenthook/testdata/pre-tool/cases.json`; the byte-level cases (empty, truncated,
invalid UTF-8, two documents, exactly 1 MiB, 1 MiB plus one byte) are generated beside it.
One Go test feeds every payload to `.config/agent/hooks/block_evasion.py` and to the Go
entrypoint in the `lefthook` dialect and requires the same exit code and the same marker.
Both sides run their built-in rules only, so an `operator: true` case, which only an
operator deny pattern refuses, is allowed by both (`TestParityWithThePythonGuardOnTheSuitePayloads`
in `internal/agenthook/parity_test.go`). A second test does the same for the environment check. Both skip with a stated reason on
a host without a working `python3` or `python`. The replay is removed together with the
Python guard.

White space means the same to both: the built-in rules are compiled with the class
Python's `\s` matches on text (Unicode separators, the vertical tab, the information
separators), because RE2's `\s` is ASCII only. Operator patterns are plain RE2.

Measured differences, both on the closed side: the Go decoder rejects the non-standard
JSON literals Python accepts (`NaN`, `Infinity`), and RE2's word boundary is ASCII, so a
skip flag directly followed by a non-ASCII letter is denied by Go and allowed by Python.

One difference by contract: a payload that names a tool outside the command-tool list is
allowed without a command. The Python guard never sees such a payload, because its
registrations match the shell tool only, and the `lefthook` dialect keeps its behaviour.

## Not in this change

- `agy`'s `PostToolUse`, `PreInvocation` and `PostInvocation`, and its edit-tool
  classification (unverified, see
  [The agy (Antigravity) dialect](#the-agy-antigravity-dialect)); its `stop` also keeps
  the plain command-policy flow rather than the checkpoint evaluators below
  (`checkpointWired`, `internal/agenthook/evaluate.go`).
- AGY return capture: its public `PostToolUse` and `Stop` payloads expose no subagent
  return body, so capability reporting keeps the boundary `unenforceable`.
- Gemini return capture: its public hook schema does not establish the exact nested report
  field for `invoke_agent`, and no redacted native recording is tracked. Capability
  reporting keeps return capture and register enforcement `unenforceable`.
- Codex return register enforcement: `SubagentStop` exposes the returned text, but the
  spawn receipt exposes no documented dispatch-to-agent correlation key. Capability
  reporting keeps enforcement `unenforceable` instead of guessing ownership.
- The per-dialect encoding that comes with the `agy` dialect:
  `post-tool`'s due-checkpoint note and `stop`'s block/continue decision still reach the
  client through the same generic `Deny`/`Skip` encoding `pre-tool` uses (stderr text and
  an exit code), not the native JSON shape (`hookSpecificOutput.additionalContext`,
  `decision: block` vs `continue: false`) those two events are specified to use; that
  needs `Dialect.Encode`'s per-dialect override the way agy's own already has one.
- `dialect.Decode` populating `Canonical.FilePath`, `ConversationID`, `Step` and
  `StopActive` from a real payload outside the agent-traffic events (`post-return` already
  decodes `stop_hook_active`); `pre-edit`'s normalised stdin and `stop`'s repeated-pass
  wording are ready for it but a real payload does not carry it yet, so a real `pre-edit`
  call denies fail-closed and `stop_hook_active` never changes the wording a live client
  sees.
- The session receipt log.
- Moving the client registrations and the Lefthook jobs to this entrypoint, and deleting
  the Python adapters (`.config/agent/hooks/checkpoint.py` and `checkpoint_scope.py` keep
  running today; `praetorctl hook <client> post-tool|stop|pre-edit` is a second, parallel
  path to the same two scripts until that flip lands).
