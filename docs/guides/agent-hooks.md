# Agent hooks

`praetorctl hook <client> <event>` is the one agent-hook entrypoint. A client registration
contains that call and nothing else: no shell substitution, no interpreter name, no flags.
The command reads the client's payload from stdin, takes the workspace from the payload,
judges the call in process and answers in that client's dialect.

This page describes what ships today. The Python adapters under `.config/agent/hooks/` and
the registrations in `.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json`
and `.config/lefthook/praetor.yml` are unchanged and still guard live sessions; they move
to this entrypoint in a later change, after both implementations have been replayed
against the same payloads (see [Parity](#parity-with-the-python-guard)).

## Command line and support matrix

```text
praetorctl hook <client> <event>
```

Both arguments match `^[a-z-]+$`. The registration table is the support matrix; a pair
without a row is rejected before any input is read.

| Client | Event | Native event | Matcher | Budget | Registration string |
| :-- | :-- | :-- | :-- | :-- | :-- |
| `claude` | `pre-tool` | `PreToolUse` | `^Bash$` | 15 s | `praetorctl hook claude pre-tool` |
| `claude` | `pre-edit` | `PreToolUse` | `^(Edit\|Write)$` | 15 s | `praetorctl hook claude pre-edit` |
| `claude` | `post-tool` | `PostToolUse` | none | 60 s | `praetorctl hook claude post-tool` |
| `claude` | `stop` | `Stop` | none | 60 s | `praetorctl hook claude stop` |
| `codex` | `pre-tool` | `PreToolUse` | `^Bash$` | 15 s | `praetorctl hook codex pre-tool` |
| `codex` | `post-tool` | `PostToolUse` | none | 60 s | `praetorctl hook codex post-tool` |
| `codex` | `stop` | `Stop` | none | 60 s | `praetorctl hook codex stop` |
| `gemini` | `pre-tool` | `BeforeTool` | `run_shell_command` | 15 s (written as ms) | `praetorctl hook gemini pre-tool` |
| `gemini` | `pre-edit` | `BeforeTool` | `^(replace\|write_file)$` | 15 s (written as ms) | `praetorctl hook gemini pre-edit` |
| `gemini` | `post-tool` | `AfterTool` | none | 60 s (written as ms) | `praetorctl hook gemini post-tool` |
| `gemini` | `stop` | `AfterAgent` | none | 60 s (written as ms) | `praetorctl hook gemini stop` |
| `lefthook` | `pre-tool` | job `agent-pre-tool` | none | none | `praetorctl hook lefthook pre-tool` |
| `lefthook` | `environment` | job `pre-rebase` | none | none | `praetorctl hook lefthook environment` |
| `agy` | `pre-tool` | `PreToolUse` | `*` | 30 s | `praetorctl hook agy pre-tool` |
| `agy` | `stop` | `Stop` | none | 30 s | `praetorctl hook agy stop` |

Codex carries no `pre-edit` row: it registers no native pre-edit event today. The checkpoint
rows above exist in the table and answer real calls, but none of the tracked registration
files (`.claude/settings.json`, `.codex/hooks.json`, `.gemini/settings.json`) name them yet,
and Lefthook's own checkpoint jobs (`agent-checkpoint-tool`, `agent-checkpoint-pre-edit`,
`agent-checkpoint-stop`) still call the Python adapters directly; both flips are a later
change (see [Not in this change](#not-in-this-change)).

The registration string is one executable call. It resolves through `PATH` (and `PATHEXT`
on Windows) and is valid under `sh -c` and under `cmd /c`. The `agy` rows are dialect data
only in this change: nothing yet writes them into a `hooks.json` file (that is C3/C4).

## One invocation

1. **Arguments.** Grammar, then the table. A failure prints the usage with every supported
   pair and exits 2, the blocking code of the native clients.
2. **Input.** `pre-tool` reads stdin up to 1 MiB within the evaluation budget, so a client
   that never closes stdin cannot hold the hook. `environment` reads nothing.
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
6. **Governed.** The root holds `.standards.yaml`. Anything else is skipped with a reason.
7. **Judge.** The process environment first, then the command policy.
8. **Encode.** The verdict in the client's dialect.

## Verdicts

| Situation | `claude`, `codex`, `gemini` | `lefthook` |
| :-- | :-- | :-- |
| unsupported or malformed arguments | exit 2, usage on stderr | exit 2, usage on stderr |
| stdin missing, empty, over 1 MiB, not one JSON object, late | exit 2, reason on stderr | exit 1, reason on stderr |
| payload event contradicts the argument | exit 2 | exit 1 |
| command tool without a command string | exit 2 | exit 1 |
| workspace relative, missing, or Git cannot answer | exit 2 | exit 1 |
| no repository | exit 0, `praetor hook: no repository, skipped` on stderr | same, and no marker |
| repository without `.standards.yaml` | exit 0, `praetor hook: workspace not governed, skipped` on stderr | same, and no marker |
| environment check fails | exit 2 | exit 1 |
| command matches a policy rule | exit 2, `[BLOCKED BY <rule>] …` on stderr | exit 1, same text |
| allowed | exit 0, both streams empty | exit 0, `PRAETOR_COMMAND_POLICY_OK` on stdout for `pre-tool`, nothing for `environment` |

A skip is a neutral allow that always states its reason; it never prints the marker,
because no policy was evaluated. Reasons are bounded to 4096 bytes and never echo the
command, which may carry a secret. A deny keeps its exit code even when the client has
already closed a stream.

## Built-in command policy

The rules are the ones of the Python guard, ported one to one. They judge the command
text. A command is denied when it

- passes the Git option that skips the commit or push hooks, in its long form anywhere or
  in its short form on a commit;
- disables Lefthook inline for one command, or sets the skip variable in front of a Git
  call;
- points the Git hooks path at the null device, or removes the repository's hooks
  directory;
- runs `adopt`, `conform`, `bootstrap` or a `needs` scan, report, migration or epic
  against the workstation dev root instead of a leaf repository (DEV-01).

The environment check denies when the hook process itself runs with Lefthook disabled or
with a Lefthook exclusion or skip list. It runs for `environment` and before every
`pre-tool` judgement.

Organisation container names are operator data, not engine data. The policy accepts a
bounded operator deny list (RE2, at most 64 patterns of at most 512 bytes; an empty,
oversized or non-compiling pattern fails the whole list). Loading that list from the
operator settings is a later change; until then the command runs the built-in rules only.

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
verified on the installed 1.2.6 build, not taken from training memory or from
`.workingdir/research/antigravity-docs-20260917.json`, which this text supersedes for
everything it covers). It is not a live-recorded fixture; see the note at the end of this
section for what remains unverified.

Hooks are configured in `.agents/hooks.json` (or a plugin's own copy), one JSON object
keyed by hook name, each hook naming one or more of `PreToolUse`, `PostToolUse`,
`PreInvocation`, `PostInvocation` and `Stop`. `PreToolUse`/`PostToolUse` groups carry a
`matcher` regex against the tool name; the other three are a flat handler list with no
matcher. A handler's default (and today, only supported) `type` is `"command"`, run via
`sh -c` on Unix and `cmd /c` on Windows, in the directory containing `hooks.json`, with a
default 30 second timeout. Only `PreToolUse` and `Stop` have a Go evaluator wired in this
change (the registration table above); `PostToolUse`, `PreInvocation` and `PostInvocation`
are H2/H4 territory and `agyEncode` fails closed if ever asked for one.

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
behaviour, any argument key besides `CommandLine`, and any edit-tool name. A headless
`agy --print` recording in a throwaway sandbox with a temp config was considered for this
change and not attempted: every real invocation needs an authenticated model call against
the operator's own Antigravity account, which is out of scope without the operator's own
explicit go-ahead. Record mode (above) is ready for whoever does that recording next.

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
checkpoint due: …`; both are `[BLOCKED BY HISS-16]` on `claude`/`codex`/`gemini`.

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
A second test does the same for the environment check. Both skip with a stated reason on
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
- Rendering the `agy` registration rows into an actual `.agents/hooks.json` (C3/C4).
- The per-dialect encoding that comes with the `agy` dialect:
  `post-tool`'s due-checkpoint note and `stop`'s block/continue decision still reach the
  client through the same generic `Deny`/`Skip` encoding `pre-tool` uses (stderr text and
  an exit code), not the native JSON shape (`hookSpecificOutput.additionalContext`,
  `decision: block` vs `continue: false`) those two events are specified to use; that
  needs `Dialect.Encode`'s per-dialect override the way agy's own already has one.
- `dialect.Decode` populating `Canonical.FilePath`, `ConversationID`, `Step` and
  `StopActive` from a real payload; `pre-edit`'s normalised stdin and `stop`'s repeated-pass
  wording are ready for it but a real payload does not carry it yet, so a real `pre-edit`
  call denies fail-closed and `stop_hook_active` never changes the wording a live client
  sees.
- The session receipt log.
- Moving the client registrations and the Lefthook jobs to this entrypoint, and deleting
  the Python adapters (`.config/agent/hooks/checkpoint.py` and `checkpoint_scope.py` keep
  running today; `praetorctl hook <client> post-tool|stop|pre-edit` is a second, parallel
  path to the same two scripts until that flip lands).
