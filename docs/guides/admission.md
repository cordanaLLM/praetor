# Admission for heavy local steps

`praetorctl admit` holds a heavy local step, such as a full gate, a race-detector test run or a
large build, until the host has the memory, swap and load headroom it asks for. It then runs the
step in its own memory-limited systemd scope and exits with the step's own status. The policy and
the scope live in `internal/admission/`, the host readings in `internal/sentinel/`, and the
command in `cmd/standardsctl/admit.go`.

Several sessions often share one workstation. When each starts its heavy step as soon as it is
ready, the steps start into a host that is already full: memory goes to swap, and the
out-of-memory daemon kills whatever scope is largest, which can be the editor with every session
in it. Admission makes a step wait for room instead, and the scope makes a step that grows past
its limit die alone.

## Run a step

```sh
praetorctl admit --label verify-all -- make verify-all
```

Everything after `--` is the step. While the host is under a threshold, the command prints one
line per reading and keeps waiting:

```text
19:04:05 admission wait verify-all: available 8.0 GiB < 12.0 GiB
```

Once the host has room, it prints one admission line, runs the step and returns when the step
ends:

```text
19:09:05 admitted verify-all: available 21.4 GiB, swap free 52.0 GiB, load 9.3
```

An agent runs `praetorctl admit` as a background command. Its client then reports the step's end,
and the admission line in the output shows when the step started.

## Thresholds

| Flag | Default | Meaning |
| :--- | :--- | :--- |
| `--min-available-gib` | `12` | least `MemAvailable` the host must report |
| `--min-swap-free-gib` | `15` | least `SwapFree` the host must report |
| `--max-load` | 1.25 per CPU | highest one-minute load average |
| `--wait` | `6h` | longest the step is held; after it the step is refused |
| `--poll` | `1m` | time between readings while held |
| `--timeout` | `6h` | longest the step runs once admitted |

A host exactly at a threshold is admitted. A host that stays under one until `--wait` passes
refuses the step with `still held after ...`, and the step never starts. A reading that fails
refuses the step at once, without waiting: the host could not be judged, so the step is not
admitted.

## Scope limits

| Flag | Default | What systemd does at the limit |
| :--- | :--- | :--- |
| `--memory-high` | `16G` | throttles the step and reclaims its memory |
| `--memory-max` | `24G` | moves the step's pages to swap |
| `--memory-swap-max` | `4G` | past `MemoryMax` plus this, kills the step |

Each limit is a whole number with `K`, `M`, `G` or `T`. The swap limit may also be `0`, which
keeps the step out of swap entirely. Any other value is refused before anything runs. The
properties are described in
[systemd.resource-control](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html).

The swap limit is part of the containment. With `MemoryMax` alone, a step that grew past it was
swapped out and finished, so a growing step pushed the rest of the host into swap. With
`MemoryHigh` below `MemoryMax`, a step that grew was throttled rather than killed, and ended only
at its deadline. Both cases were measured on 2026-10-17. The defaults leave room for a full gate
on a workstation with 64 GiB; a step that needs less should ask for less.

## Make agents use it

A host can make every agent session route heavy steps through `praetorctl admit`. Set this
in one of the host's operator layers, usually the workstation or fleet document
([operator settings](effective-policy.md#operator-settings)):

```yaml
hooks:
  admission:
    enforce: true
    scope_form: true
    max_jobs_divisor: 3
```

The engine's pre-tool hook (`praetorctl hook <client> pre-tool`) and the engine checkout's
own Python guard (`praetorctl admission-check`) then refuse a recognised heavy command,
such as `make`, `go test`, `cargo build` or `git push`, that does not run through an admitted
form, and name the command to run instead. The recognised shapes and how commands are read
are in [heavy-step admission](agent-hooks.md#heavy-step-admission). The hook refuses rather
than rewriting the command: a rewritten step that waits for room would wait inside the
client's tool timeout, while a refused one is started again in the background.

### Worktree-isolated sessions

Claude Code's worktree isolation check can refuse a command that runs Git under a launcher
such as `praetorctl admit` or `systemd-run`. In such a session, put an admission label directly
before the push:

```sh
PRAETOR_ADMIT=git-push git push -u origin topic
```

The hook reads the leading assignment with the existing command parser and accepts this form
only for the recognised `git push` shape, including `git -C dir push` and `git -c key=value push`.
The label must match `[a-z0-9][a-z0-9._-]{0,30}`. An empty or invalid label, an assignment passed
through `env`, or an assignment after the command does not admit the push. The assignment applies
only to its own simple command: `PRAETOR_ADMIT=git-push git push; make` still refuses `make`.

This form requests admission; it does not exempt a push. The hook reads the live host through
`internal/admission` and applies `HostCappedPolicy` and `Judge`, retaining the load limit as well
as the memory and swap bounds. A host exactly at the thresholds is admitted. Under pressure,
the hook refuses immediately with the readings and advice to wait and retry; a failed host
reading also refuses. The assignment does not start a scope or wait for headroom itself.

Plain `git push` remains refused under enforcement, and `PRAETOR_ADMIT=x make` or
`PRAETOR_ADMIT=x go test` still requires another admitted form. The existing
`praetorctl admit --label git-push -- git push ...` path is unchanged. The policy cases are
covered by `TestAdmission_IsolatedPush_*` in `internal/agenthook/admission_test.go`; the Claude
pre-tool payload and exit codes are covered by `TestHookClaudeIsolatedPushAdmission` in
`cmd/standardsctl/hook_test.go`.

### Merged and closed pull request heads

Every judged `git push` (`PRAETOR_ADMIT=<label> git push ...` and `praetorctl admit ... -- git push ...` alike) also checks that the destination branch is not the head of an already merged or closed pull request (`internal/agenthook/admission.go`: `judgePush`, `judgePushHead`).

The hook resolves:

- the repository directory from the invocation's working directory or `git -C <dir>`;
- the remote from the named argument (`--repo` or positional remote, or the remote named by `@{push}` when omitted, else `origin` for `--all`) through `util.ReadOriginRemote`, `util.ReadGitRemote` and `util.ParseGitRemote` (`internal/util/util.go`);
- the destination branch from an explicit refspec destination (`src:dst`, plain branch name, or `HEAD` / `@` resolved to the current branch through `git symbolic-ref --quiet --short HEAD`), from `git rev-parse --symbolic-full-name @{push}` when the refspec is omitted (refusing when Git cannot resolve `@{push}`, such as no upstream, `push.default=nothing`, or a detached `HEAD`), or from `git for-each-ref refs/heads` for `git push --all` / `--branches` (up to `agenthook.MaxPushAllBranches = 32` local branches, refusing above the bound).

`git push --mirror` is refused because it rewrites every branch on the remote; callers push branches one by one instead. Delete pushes (`--delete`, `-d`, `:branch`) and tag pushes (`--tags`, `tag <name>`, `refs/tags/<name>`) are not judged. For branch pushes, the hook checks pull requests in this order under a 15-second context timeout:

1. Query open pull requests for the head branch, starting at `page=1` with a bounded page size of `forge.HeadPullRequestsPerPage = 20`. Filter each page to the pushed repository and branch; any matching `OPEN` pull request admits the push immediately, regardless of the number of closed pull requests on that branch. If no match is found on a full raw page, request the next open page, up to `forge.HeadPullRequestsMaxPages = 5`. A short raw page ends the open results. A full fifth page without a match refuses the push with `open pull request head listing ... exceeded 5 pages of 20 items`; closed pulls are not queried when the open-page bound is reached.
2. Only after the open results end without a matching open pull request, query the first page of closed pull requests, newest-created first. GitHub uses `state=closed&head={owner}:{branch}&sort=created&direction=desc&per_page=20&page=1`. Forgejo uses `state=closed&head={branch}&page=1&limit=20`, with `sort` omitted to select its newest-created-first default. The first terminal page decides; a full page does not trigger a history walk or a pagination-ceiling refusal.
3. Filter each response to the destination head branch and, when present, its repository metadata. Prefer the first `MERGED` match over the first `CLOSED` match on the terminal page. No matching pull request admits the push; network, HTTP status and parsing errors refuse it.

The open queries use the same driver parameters with `state=open`. The drivers are `forge.GitHubDriver.ListPullRequestsByHead` and `forge.GiteaDriver.ListPullRequestsByHead`; the reader interface and `PullRequestHead` shape are shared. Query parameters follow the [GitHub pull request API](https://docs.github.com/en/rest/pulls/pulls#list-pull-requests) and [Gitea pull request API](https://docs.gitea.com/api/1.25/operations/repo-list-pull-requests/). Forgejo v17.0.1's [OpenAPI schema](https://codeberg.org/forgejo/forgejo/src/tag/v17.0.1/templates/swagger/v1_json.tmpl) confirms `state`, `head`, `page` and `limit`; its [pull listing](https://codeberg.org/forgejo/forgejo/src/tag/v17.0.1/models/issues/pull_list.go) calls the [default sort implementation](https://codeberg.org/forgejo/forgejo/src/tag/v17.0.1/models/issues/issue_search.go), which orders by creation time descending, then ID descending. Gitea's [default sort](https://github.com/go-gitea/gitea/blob/v1.25.0/models/issues/issue_search.go) uses the same order. These references were checked on 2026-11-04.

When a `MERGED` or `CLOSED` pull request heads the branch and no `OPEN` pull request does, the push is refused naming the pull request number and state and instructing the caller to push to a new branch:

```text
[BLOCKED BY HISS] git push to branch merged-topic on acme/widgets refused: pull request #42 is MERGED; push to a new branch instead (docs/guides/admission.md)
```

When the target repository, remote, branch or forge pull request listing cannot be read, the check fails closed with a named refusal (`git push target resolution failed` or `git push forge pull request check failed`). A branch with no pull request or with an `OPEN` pull request passes (`TestAdmission_GitPushHead_*` in `internal/agenthook/admission_test.go`).

### Scope form and memory refusal

With `hooks.admission.scope_form: true`, the pre-tool hook also accepts a direct
`systemd-run` transient user scope around the heavy step when the wrapper carries `--user`,
`--scope`, a `MemoryHigh=` property (`-p MemoryHigh=8G` or `--property=MemoryHigh=8G`) and
`--expand-environment=no`:

```sh
systemd-run --user --scope -p MemoryHigh=8G --expand-environment=no make verify-all
```

A `systemd-run` wrapper missing any of those four parts is refused with the missing flags
named. When the wrapper is complete, the hook reads the host's live memory and swap headroom
through `internal/admission` (`HostReader`, `HostCappedPolicy`, `Judge`): if `MemAvailable`
is below `12 GiB` (or a quarter of total RAM on a smaller host) or `SwapFree` is below
`15 GiB` (or half of total swap), the hook refuses the scope and names both readings so the
session waits or uses `praetorctl admit`.

### Job count cap

While `hooks.admission.enforce` is on, any heavy step carrying `-j N`, `-jN`, `--jobs N` or
`--jobs=N` above `floor(cores / max_jobs_divisor)` (default divisor `3`), or a bare `-j` /
`--jobs` with no count, is refused naming the requested job count, the host's core count and
the computed limit.

| Operator key | Default | Meaning |
| :--- | :--- | :--- |
| `hooks.admission.enforce` | `false` | refuse heavy steps outside `praetorctl admit`, an admitted scope or a live-judged isolated push |
| `hooks.admission.scope_form` | `false` | accept `systemd-run --user --scope` with `MemoryHigh` and `--expand-environment=no` when host memory and swap clear the admission bounds |
| `hooks.admission.max_jobs_divisor` | `3` (when `enforce: true`) | cap `-j` / `--jobs` on heavy steps at `floor(cores / divisor)` (`>= 1`) |
| `hooks.admission.commands` | `[]` | up to 32 additional RE2 heavy-step shapes, appended across layers |

Enforcement is off by default, so an adopter who sets nothing sees no change. Once a layer
turns it on, a later layer cannot turn it off. With `hooks.scope: governed`, the default, the
hook runs only in repositories that carry `.standards.yaml`; `hooks.scope: all` covers every
workspace.

## Platforms

On Linux with a systemd user manager the step runs in a transient scope:

```sh
systemd-run --user --scope --quiet --collect --expand-environment=no \
  --unit=admit-<label>-<time> \
  --property=MemoryHigh=16G --property=MemoryMax=24G --property=MemorySwapMax=4G -- <command>
```

`--expand-environment=no` keeps systemd from rewriting `$` in the command's arguments. Before each
step the command checks that scopes work by starting an empty one. Where that fails (no user
manager, a container, macOS, Windows), the step runs without a scope, and a line names the reason:

```text
admission verify-all: runs unscoped, <reason>
```

The host readings follow what each platform reports:

| Platform | Memory | Swap | Load |
| :--- | :--- | :--- | :--- |
| Linux | `/proc/meminfo` `MemAvailable` | `/proc/meminfo` `SwapFree` | `/proc/loadavg` |
| Windows | `GlobalMemoryStatusEx` available physical memory | not measured | not measured |
| macOS | not measured | not measured | not measured |

A figure that is not measured is not judged, and the admission line says so. When memory itself is
not measured, the step is admitted with `admitted unjudged`.

## Evidence

The tests in `internal/admission/admission_test.go` and `cmd/standardsctl/admit_test.go` cover the
cases this guide describes:

- `TestRunAdmit_Negative_UnderThresholdRefused`: a step asked to start under a threshold the host
  cannot meet is held, then refused, and never runs.
- `TestWait_Positive_HeldThenAdmitted`: a held step is admitted once the readings clear, with one
  wait line per held reading.
- `TestRun_Positive_ScopeCarriesTheLimit`: inside its scope the step reads its own `memory.max`
  back as the limit it was given.
- `TestRun_Negative_SpikeKillsOnlyTheStep`: a step that tries to hold 1 GiB under a 128M limit with
  no swap is killed, and the test process that started it keeps running.

The two scope tests skip on hosts without user scopes.

## Why not a queue tool

The [admission runner survey](../research/admission-runners.md) compares Pueue, nq, Task Spooler,
systemd-run and GNU parallel. None of them both waits for memory and swap headroom and contains
the step once it runs:

- Pueue, nq and Task Spooler admit by slot count and contain nothing.
- GNU parallel waits for free memory and load, but it is a batch tool for one producer.
- systemd-run contains a step in cgroups, but it starts the step at once.

`praetorctl admit` therefore reads the host through the existing sentinel, waits in a short loop
and uses systemd-run for containment.
