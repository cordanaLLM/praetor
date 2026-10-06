# State ledger integrity

The entire `.workingdir/` directory is private, Git-ignored workstation state.
Keep cluster connection guides, backend notes and raw evidence there; publish
only reviewed, sanitized documents under `docs/`. Run `make state-audit` in a fresh
checkout to initialize an absent ledger and audit it. Initialization is keyed on
the ledger files, not on the directory: `.workingdir` is routinely created first
by another command (milestone, forge, docdistill, dedupe, hindsight), and such a
directory is seeded with the five ledger files rather than left empty. A
directory that already holds some of them is a *partial* ledger — a file was
removed or corrupted — and is left exactly as it stands, so the audit and
`state sync` still fail on it and it requires explicit repair. Initialization
never imports another workstation's private state.

`praetorctl state init --if-absent` reports which of five things it did, because
three of them write nothing and an operator told a ledger was seeded stops
looking:

| Report | What happened |
| :--- | :--- |
| `Initialized private .workingdir/ in <dir>` | the directory was absent; this call made it and wrote the five ledger files |
| `Seeded a ledger into the existing .workingdir/ in <dir>` | the directory existed and held no ledger file; this call wrote the five |
| `Existing .workingdir/ in <dir> already holds ledger files and was left untouched` | a complete or partial ledger; nothing was written, and a partial one needs explicit repair |
| `Existing .workingdir/ in <dir> is another Git repository's working tree and holds no ledger` | the directory holds its own `.git` and no ledger file; nothing was written, so that repository stays clean; run `state init` to seed it deliberately |
| `.workingdir in <dir> is not a directory; nothing was written` | the path is a symlink or a regular file; remove it before initializing |

The nested-repository refusal keeps a clone staged as a private gitlink
unchanged until the commit hook's privacy check reports it
(`test_private_gitlinks_block_commit_before_snapshot_export` in
[`.config/lefthook/scripts/test_hooks.py`](https://github.com/cordanaLLM/praetor/blob/main/.config/lefthook/scripts/test_hooks.py));
`TestBootstrapLeavesANestedRepositoryUntouched` in
[`internal/state/bootstrap_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/bootstrap_test.go)
pins the outcome.

Every ledger file is published atomically: its content is staged under a private
`.praetor-*.pending` name, synced there, and linked into place. Seeding is no
longer arbitrated by one directory creator, so several processes may initialize
the same ledgerless directory at once; staged publication means each name is
either absent or holds its whole template, never a zero-byte file an audit would
read as a corrupt ledger.

Both forms of `praetorctl state init` then make Git ignore the directory, unless
the path is not a directory at all. Every other command that writes into the
ledger, and seeds it on first use, does the same: `state sync`, `state task add`,
`state bug add`, `state bug resolve` (which seeds the ledger before it reports an
unknown ID), `state question add` and `praetorctl flavor apply`
([`cmd/standardsctl/state.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/state.go),
`withLedgerIgnore`).

- **Existing ledger:** the command reconciles the rule before it writes
  anything. That covers a ledger an older binary created, one seeded before
  `git init`, and one whose first ignore step failed. If `.gitignore` cannot be
  merged, the command is refused and nothing is written, so running it again
  never records the same change twice.
- **Ledger created by this run:** the rule follows the run. If that ignore step
  fails, the error says the change was recorded and must not be repeated; the
  next ledger command reconciles before it writes.
- **`adoption.decline` warning:** printed only by the run that seeded the
  ledger, not by every later command.

`state sync` takes this step before it records the working tree, so the snapshot
it writes already includes the new `.gitignore` and the commit-msg hook's
`state sync --verify` accepts it.

Git is asked whether its own ignore rules exclude `.workingdir` itself: the
repository's `.gitignore` files and `.git/info/exclude` count, while a global
`core.excludesFile` does not, because it belongs to one host and not to the
repository. A directory
excluded as a whole keeps every file under it private, whatever negations
follow; a rule such as `.workingdir/*` followed by `!.workingdir/STATE.md` does
not. The command acts on the answer:

| Git's answer | What the command does |
| :--- | :--- |
| the directory is in no Git work tree | nothing; no commit can publish it |
| the directory is already excluded, in any spelling | nothing; `.gitignore` stays byte for byte |
| not excluded, and `adoption.decline` names `git-ignore` | prints a warning; `.gitignore` belongs to the operator |
| not excluded | appends the private-artifact block `praetorctl adopt` maintains, reports it, and asks Git again |

The block is written by the same function adoption uses, `writeManagedGitIgnore`
in [`internal/adopt/gitignore.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/gitignore.go),
so a later `praetorctl adopt` recognizes it as its own. The same block also
ignores the legacy scratch root `.workingdir2/` (`state.LegacyWorkingDirName`)
unless the repository retired it
([Adoption, audit, and CI](documentation-governance.md#adoption-audit-and-ci)).
A `.gitignore` that cannot be merged, such as one with an unterminated managed
block, fails the command. The behaviour is covered by
[`internal/adopt/private_ignore_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/private_ignore_test.go),
[`internal/state/ledger_present_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/ledger_present_test.go)
and
[`cmd/standardsctl/state_ignore_test.go`](https://github.com/cordanaLLM/praetor/blob/main/cmd/standardsctl/state_ignore_test.go).

Bug mutations validate the entire `.workingdir/BUGS.md` before changing it. A
malformed row, duplicate or noncanonical ID, invalid severity/status, unreadable
file, symlink, or exceeded size limit returns an error. Audit, state sync and
Hindsight ingestion propagate that error instead of reporting an empty ledger.

Use `praetorctl state bug add`, `list` and `resolve`. Mutations preserve unrelated
rows and surrounding Markdown byte for byte. IDs increase from the greatest
existing numeric ID; gaps are not reused. Recovery must retain source finding IDs
without overwriting another finding's already assigned bug number.

## Record format and compatibility

The six visible columns remain ID, Title, Severity, Status, Location and Resolution.
A row ends in one of three forms, and readers accept all three in the same ledger:

| Row ending | Written by | Cells | Context, CreatedAt, ResolvedAt |
| :--- | :--- | :--- | :--- |
| nothing | hand-written legacy rows | read literally, including backslashes and HTML entities | none |
| `<!-- praetor-bug:v1 BASE64 -->` | writers before the sidecar | pipes, line breaks and edge spaces encoded | Base64 JSON inside the comment |
| `<!-- praetor-bug:v2 -->` | `state bug add` and `state bug resolve` today | encoded as in v1 | `.workingdir/bugs.meta.json`, keyed by bug ID |

The sidecar exists so the file agents read carries no Base64: on this repository
the v1 comments were 45% of a 505 KB `BUGS.md`. It is one JSON object,
`{"version":1,"bugs":{"BUG-001":{"context":…,"created_at":…,"resolved_at":…}}}`,
written with sorted keys. Metadata is strict in both forms: unknown, missing, null,
duplicate and case-alias fields are rejected, and so are a wrong version, a
noncanonical ID and a v2 row whose ID has no sidecar record. A sidecar record no
row refers to is ignored; it is what an interrupted write leaves behind (see below).

The parser requires one complete ledger table. Fenced examples and unrelated
Markdown are preserved. An unterminated code fence is a ledger error naming the
line it was opened on, so rows after it are never hidden and bug additions never
reallocate an existing ID. A claimed bug row outside the table is an error. Limits
are 1 MiB per ledger and per sidecar, 10,000 records, 16 KiB per text field and IDs
through `BUG-999999999`. Invalid or oversized updates leave both files intact.

Go callers should use `ListBugsContext`, which reads the ledger together with its
sidecar. `ParseBugsMarkdownStrict` and `RenderBugsMarkdownStrict` work on one
self-contained document with v1 metadata; the strict parser reports a v2 row as an
error because the sidecar is not part of its input. The old slice/string-only
parser and renderer remain for source compatibility but cannot communicate detailed
errors. They return no partial records or an explicitly invalid document when
validation fails.

**Refresh older local binaries before writing this format.** An older writer can
discard the metadata comment, reinterpret encoded cells, or reject a v2 row it does
not know. `make dev-install` refreshes this checkout's local binaries, retains
rollback executables and records their source identity; see
[development MCP](development-mcp.md).

## Question ledger

`.workingdir/QUESTIONS.md` uses the same cell codec, text limits and sidecar envelope
as `BUGS.md` ([`internal/state/ledger_codec.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/ledger_codec.go),
[`ledger_meta.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/ledger_meta.go)). The five visible columns stay
ID, Question, Options, Status and Decision. A row ends in one of two forms:

| Row ending | Written by | Cells | Context, CreatedAt, DecidedAt |
| :--- | :--- | :--- | :--- |
| nothing | writers before this format | read literally; options split on every comma | none |
| `<!-- praetor-question:v2 -->` | `state question add` and `decide` today | pipes, line breaks, edge spaces and commas inside an option encoded | `.workingdir/questions.meta.json`, keyed by question ID |

The sidecar is `{"version":1,"questions":{"Q-001":{"context":…,"created_at":…,"resolved_at":…}}}`,
where `resolved_at` holds the decision time. It is checked as strictly as
`bugs.meta.json`. Any write re-renders every row in the current form, so a legacy ledger
upgrades on its first write. IDs continue from the greatest existing ID. A duplicate ID,
a claimed question row that does not parse, a status other than `pending`, `decided` or
`dismissed`, a blank question or option, more than 64 options, and a field holding NUL,
invalid UTF-8 or more than 16 KiB are errors, never skipped rows
([`internal/state/questions_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/questions_test.go)).
`ListQuestionsContext` reads the table together with its sidecar;
`ParseQuestionsMarkdownStrict` reads the table alone and returns no context or timestamps.

## Task rows and selectors

`OPEN.md` holds one Markdown checkbox per task. `praetorctl state task list`,
`complete` and `archive` all resolve rows through `parseTaskLines` in
`internal/state/tasks.go`, which is the single numbering authority: the number
`list` prints for a row is the number `complete` acts on.

| Rule | Behaviour |
| :--- | :--- |
| numbering | pending and completed rows are numbered together, from 1, in file order |
| numeric selector | resolves only by that number; it never falls back to matching a digit inside a description, and a number naming an already completed row is refused |
| text selector | must match exactly one pending description; more than one match is an error listing the candidates, as `selectMilestone` does for milestones |
| code fences | a checkbox inside a ``` or `~~~` fence is an example, never a task: it is not listed, completed or archived |
| unterminated fence | a fence opened and never closed is a ledger error naming the line it was opened on; `list`, `complete`, `archive`, `add` and `state sync` all refuse the file rather than silently dropping the rows after it, and `add` refuses rather than appending a row inside the open fence |

A refused selector writes nothing, so `OPEN.md` stays byte-identical.

`praetorctl state task archive` moves completed rows to `BACKLOG.md` under a header
naming the commit they were discharged at. The commit comes from
`state.RecordedCommit` (`internal/state/sync_binding.go`): the HEAD SHA, or the stamp
`local` when the directory is outside a Git worktree or its branch has no commit yet,
so the unborn marker never reaches the ledger. A Git read that fails for any other
reason, such as broken `.git` metadata, stops the archive with an error and leaves
`OPEN.md` unchanged. `baseline --record` and adoption stamp a recorded baseline through
the same function. A new `BACKLOG.md` is created owner-only (0600), like `OPEN.md`.
Covered by `internal/state/recorded_commit_test.go`, the
`TestDispatchCommand_StateTaskArchive_*` tests in
`cmd/standardsctl/standardsctl_test.go` and
`TestArchiveCompletedTasks_CreatesPrivateBacklog` in `internal/state/tasks_test.go`.

The fence tracker is `util.MarkdownFence` in `internal/util/marked_block.go`, the one
implementation every scanner that follows fences across a whole document drives
(HISS-19): the task parser, the bug-ledger parser, the marked-block finder, the
`BACKLOG.md` milestone section remover (`RemoveMarkdownSection`), the
caveman line scanner (`internal/caveman/scan.go`) and the `AGENTS.md` vendor
splitter (`internal/agentcontext/render.go`) and the PR checklist and receipt
readers (`internal/forge/pr.go`). The ADR constraint block reader
(`internal/adr/constraint.go`) extracts a single labelled block, matches its own
label and keeps no fence state. A line closes a fence when it repeats the
delimiter run that opened it and carries nothing further but that delimiter
character, spaces and tabs; a shorter run never closes a longer one. A backtick
line whose info string holds another backtick, such as `` ```make``` must pass ``,
is an inline code span as CommonMark defines it and opens nothing; a `~~~` fence's
info string may carry backticks. Lines are
bounded at `maxTaskLineBytes`; a ledger line reaching that bound is reported
rather than truncated.

A freshly initialized ledger contains **no** task rows. `OPEN.md` and
`BACKLOG.md` are seeded with headings only, so every count `state sync` reports
is work somebody actually recorded. The behaviour is covered by
`internal/state/task_select_test.go` and `internal/state/bootstrap_test.go`.

## Milestones and BACKLOG.md

`praetorctl milestone` keeps `.workingdir/milestones.json` and renders it into the
delimited milestone block of `BACKLOG.md` (`internal/milestone/milestone.go`).

- **The remote repository is resolved, never assumed.** `milestone sync`, `milestone create --publish`, and
  `milestone close --publish` act on `--owner` and `--repo`, else the directory's
  `repository.owner` and `repository.name` or its origin remote, with
  `forge.default_owner` from the operator settings (`--fleet-config`,
  `--workstation-config`, `--manifest`) as the last owner step. Praetor ships no default
  owner: with none of these the command refuses with
  `owner unknown: pass --owner, set repository.owner, or set forge.default_owner`
  (`config.ErrOwnerUnknown`), and an owner without a repository name is refused with
  `config.ErrRepositoryNameUnknown` (`TestMilestoneSync_3D_RepositoryResolution` in
  `cmd/standardsctl/forge_owner_test.go`). `milestone create --publish` fails after the local create when no identity resolves; the recovery is to create the milestone on the forge and run a targeted sync passing the identity (e.g. `praetorctl milestone sync --owner=acme --repo=example`) to bind it by title. `list`, `create`, `status` and a local
  `close` need no remote.

- **Remote sync binds by forge number.** A row with a `remote_number` is matched by
  that number, so a remote rename updates the row and a title swap between two remote
  milestones keeps both bindings. Only an unbound row is matched by title, which binds
  it (`internal/milestone/merge.go`). Two rows sharing one number, left behind by the
  earlier title-keyed sync, are repaired: the row carrying the remote's current title
  keeps the binding, the other is unbound, kept as a local-only milestone and reported
  with a `[WARN]` line.
- **A local close is never reverted.** `milestone close` flags an open milestone
  `pending_remote_close`. While the forge still reports it open, `milestone sync`
  keeps it closed and prints a `[WARN]` line naming it. `milestone close <n> --publish`
  (same `--owner`, `--repo`, `--token` and `--endpoint` flags as `sync`) PATCHes the
  bound remote milestone to closed, reads it back and clears the flag only when the
  forge reports it closed. A local-only milestone has no remote to close and the
  command reports that.
- **BACKLOG.md writes are compare-and-swap.** The milestone block is published with
  `contextopt.ReplaceRootSnapshot` against the bytes it was rendered from: the same
  locked writer (`contextopt.ReplaceSnapshot`) `praetorctl state task archive` uses to
  append discharged tasks. A write that finds the file changed fails and leaves the
  other writer's bytes. `milestones.json` itself is still written without
  compare-and-swap: a store at its 10,000-entry bound exceeds the 1 MiB snapshot bound
  `ReplaceSnapshot` enforces.
- **A failed command restores the store it changed.** `milestone create`,
  `close` and `sync` read `milestones.json` (or note its absence) before writing it. When
  the store write or the `BACKLOG.md` write then fails, `commitStoreAndBacklog` rewrites
  the prior bytes, or removes the store it created, and the error says
  `milestones.json restored to its prior state`; a retry starts from the same ledger and
  creates no duplicate. The restore runs even when the caller's context was cancelled,
  uses the lock-free store writer so a busy `BACKLOG.md` lock cannot block it, and only
  replaces a store that still holds the bytes the command wrote: one another writer
  changed is left as found, and a restore that itself fails is reported with
  `keeps this change` (issue #412, `internal/milestone/commit_rollback_test.go`).
- **A `BACKLOG.md` write that landed is not undone.** The compare-and-swap writer can
  report an error after its rename or link already published the file: a caller
  cancelled in that window, or a failed directory sync, unlock or staging cleanup.
  `settleBacklogFailure` reads `BACKLOG.md` back first. When it holds the new render,
  the store write is not undone, so both ledgers carry the change, and the error says
  `BACKLOG.md was published before the failure`. When it cannot be read, the store is
  left with the change and the error says `could not be read back`; it is never
  restored blind.
- **BACKLOG.md stays confined.** The read and the compare-and-swap write both open
  `.workingdir` through `util.InConfinedDirectory`, a pinned handle on the repository
  root, like every other milestone ledger write. A `.workingdir` link inside the
  repository resolves; one that escapes it, including a link swapped in after the
  read, fails with `ErrPathEscapesRoot` and writes nothing outside (BUG-826).

The behaviour is covered by `internal/milestone/remote_sync_test.go`,
`internal/milestone/milestone_test.go`, `internal/milestone/commit_rollback_test.go` and
`cmd/standardsctl/milestone_close_test.go`.

## STATE.md entries

`praetorctl state sync` appends one entry per call: a `### [time]` heading with the
commit and branch, then one line with the activity and the counts.

```text
### [2026-09-18 06:21:57 UTC] `a7c646ac044d9d382dd51fc13862884c7ab937fa` on `main`
- sync | tasks 130 open 42 done | bugs 569 open | qs 4 pending | git available dirty 1

<!-- praetor-state:v1 sha256:… -->
```

The activity is the `--log` text collapsed to one line, so it cannot forge a heading
or a marker. The engine's own texts shorten: no `--log` becomes `sync`, and the
post-commit hook's text becomes `post-commit sync`. One rendered entry is bounded at
16 KiB; a longer `--log` text is refused before anything is written.

### History rotation

`state sync` keeps `STATE.md` bounded without a manual step
(`internal/state/state_rotate.go`). While the log holds at most 200 entries and at most
256 KiB, sync only appends. Once either trigger is crossed, the same sync moves the
oldest entries into a new file beside the ledger, `.workingdir/STATE.history-<UTC
timestamp>.md`, and keeps the newest 100 entries (at most 128 KiB of them), then appends
its own entry. That entry names the archive, for example
`- sync; archived 101 entries to STATE.history-20260926T101500.123456789Z.md | …`, and
`praetorctl state sync` prints the same.

- Only whole entries move, oldest first, as one contiguous run copied byte for byte;
  the preamble before the first entry stays. An entry larger than the 128 KiB keep bound
  is archived whole, so a rotation always ends under the bounds.
- Archive files are never replaced or deleted. Each name is claimed exclusively; an
  existing name fails the sync with `STATE.md` untouched.
- The archive is written before `STATE.md` is replaced. If the replacement fails, the
  rotated entries are in both files, never in neither.
- The sync marker covers the rewritten log, so `state sync --verify` passes after a
  rotation exactly as after an append.

The behaviour is covered by `internal/state/state_rotate_test.go`.

### What the sync marker binds

The marker binds the Git state, the repository's own path, and the other ledgers
(`OPEN.md`, `BACKLOG.md`, `BUGS.md`, `QUESTIONS.md`, and `bugs.meta.json` and
`questions.meta.json` once each exists), so editing any of them stales it. Sync removes the previous marker before it
appends its own.

The binding is the SHA-256 of one JSON array that `stateBinding` in
`internal/state/sync_binding.go` builds in a fixed order:

1. the label `praetor-state:v1`, the canonical root, the Git state, branch and HEAD;
2. each ledger name with the SHA-256 of its bytes, then each sidecar that exists;
3. the output of six Git probes (`stateGitProbes`): HEAD, the index listing
   (`ls-files -v --stage -z`), status, the staged and the unstaged binary diff, and the
   untracked listing (`ls-files --others --exclude-standard -z`);
4. one record per untracked path, in the order git lists them.

The marker is the SHA-256 of that binding, a NUL byte and the `STATE.md` bytes before the
marker (`stateLogHash`). The same inputs always give the same marker. Each untracked
path's record is one of two kinds:

| Untracked path | Record |
| :--- | :--- |
| a regular file of at most 1 MiB, while 8 MiB of untracked content remains | the 64-hex SHA-256 of its bytes |
| any other path: a symlink, named pipe, socket or device, a nested repository, a larger file, or a file past the 8 MiB budget | `metadata <type> <size> <modification time in ns>` |

A metadata record changes when the path's type, size or modification time does, so
touching or resizing such a file stales the marker; an edit that keeps all three does
not. Such paths are rarely state inputs, and one stray file, such as a large decoded
video left in a source directory, must not stop the ledger from being written (#776).
Git does not list named pipes or sockets as untracked; one listed anyway is bound by
metadata. `TestStateUntrackedMetadataBinding_3D`, `TestStateSyncStrayUntrackedFiles_3D`,
`TestStateSyncNamedPipeUntracked_3D` and `TestStateSyncUntrackedSymlink` in
`internal/state/sync_bounds_test.go` replay these cases.

State inputs fail closed instead. A ledger that is not a regular file, or is over
1 MiB, fails the sync with its path and the reason, for example
`bind state ledger .workingdir/BACKLOG.md: 1048577 bytes, over the 1048576-byte limit,
and ledger inputs fail closed`. A tracked file git cannot read, such as one replaced by
a named pipe, fails with git's own diagnostic, which names the path
(`TestStateSyncIrregularStateInputFailsClosed_3D`). Binding metadata in their place would
let an edit that keeps the size and modification time pass as synchronized.

The sync is bounded by bytes and time, not by the size of the repository:

| Bound | Value | Reason |
| :--- | :--- | :--- |
| index listing | 16 MiB (`syncIndexBytes`) | the largest cap a bounded command accepts (`util.MaxCommandOutputBytes`), which `util.RefuseGitStatusFilters` also uses for its listings of the same tracked paths |
| every other Git output | 8 MiB each (`contextopt.MaxTotalBytes`) | these grow with uncommitted changes, not with the repository |
| each Git probe | 5 seconds (`util.GitProbeTimeout`) | the deadline every state probe shares |
| records per Git listing | 1,048,576 (`maxSyncRecords`) | the loop bound (HISS-02); an index record is at least 54 bytes, so the 16 MiB cap admits at most 310,689 and is reached first |
| untracked content | 1 MiB per file, 8 MiB in total | beyond either, a path is bound by metadata |

There is no tracked-file count limit. A repository is refused only when its index
listing outgrows 16 MiB, about 148,000 files with 60-byte paths, and that error names
the cap, the records read before it and what to do (`TestRunSyncProbeByteBound_3D`).
The earlier cap of 10,000 index entries refused an adopter repository once it tracked
10,115 files (#775); `TestStateSyncLargeIndex_3D` syncs a repository tracking 10,052.
These bounds change no input a ledger could already bind, so existing ledgers keep
verifying without a new sync.

One path is outside the Git binding besides `.workingdir`: the gate's Exit-0 receipt
`.standards-receipt.json` at the ledger root. `praetorctl gate run` writes it after the turn's
sync, and while it was bound every gate run staled the ledger it had just found current (#136).
It is excluded rather than having the gate sync the ledger because it carries nothing the ledger
must bind: its signed content derives from the HEAD commit and the clean tree the binding already
covers, and nothing commits it. The exclusion is the gate's own literal pathspec
(`gateReceiptExclude` in
[`internal/state/sync_receipt.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/state/sync_receipt.go)),
so only the root file is excluded; the same name in a subdirectory still binds. Any other change
beside the receipt still stales the ledger. The cases are replayed in
`internal/state/sync_receipt_test.go` and, with the real receipt stage, in
`TestRunReceiptStage_3D_StateLedgerStaysCurrent` (`internal/gating/receipt_ledger_test.go`).

Git state is observed with `core.autocrlf=input`. The fixed input normalization keeps
one logical text state across platforms without rewriting the worktree, while a path
marked `-text` remains byte-sensitive. This also prevents an ordinary Windows Git
command using `core.autocrlf=true` from changing the marker merely by refreshing index
stat metadata; staged changes, untracked bytes, and changed text content still stale it.
Every state observation runs through `util.RunGitTreeProbe`
([`internal/util/git_status.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/util/git_status.go)), the same probe the gate's
clean-tree check uses, so the two cannot drift to different line-ending models. State inspection
refuses a repository whose own configuration defines a clean or process filter that the filter
attribute of a tracked path selects, through `util.RefuseGitStatusFilters`, and refuses
assume-unchanged and skip-worktree index entries as `util.GitHiddenIndexReason` classifies them;
the refusals are replayed in `internal/state/sync_binding_test.go`.

A defined filter that no tracked path selects never runs, so it passes. Stock Git for Windows
defines `filter.lfs` in its system gitconfig and `git lfs install` writes it into the global
one; the probe reads neither file, and `git lfs install --local` puts the same block in the
repository's own configuration, where it passes until an attribute such as `*.bin filter=lfs`
selects it for a tracked path (#640). Git resolves the attribute itself, macros,
`.git/info/attributes` and nested `.gitattributes` files included; an untracked path does not
count, since status lists it without cleaning it. The Windows default block is replayed by
`TestStateInspectionSelectedFilterDriver_3D` and by the `TestRefuseGitStatusFilters_*` cases in
`internal/util/git_filters_test.go`.

The path is canonicalised before it is bound, so one repository reached under two
spellings of its directory binds to one state. Two callers rarely hold the same
spelling: on Windows a tool started from a short-name temporary directory reports that
name verbatim while git resolves the same directory to its long form, and on macOS a
path through `/var` names the directory `/private/var` holds. Binding the spelling
rather than the directory made a hook refuse its own repository's ledger as stale, and
that refusal blocked every commit and push (#135). A ledger written before this change
reports stale once; one `praetorctl state sync .` reconciles it. The `praetor-state:v1`
marker is unchanged on purpose, so such a ledger reports *stale* — which names the
action — rather than *missing*.
`state sync --verify` and every hook check only the last marker, whose hash covers
the whole file before it, so older markers were never read. Earlier versions wrote four labelled bullets
per entry and kept every marker; those entries still verify and are rewritten only
by `state compact`.

## One-time ledger compaction

Two commands shrink an existing ledger once. Both refuse a ledger whose last sync
marker no longer verifies, because they finish with a fresh sync and must not
certify changes nobody synchronized. Run `praetorctl state sync .` first if a hook
reports the ledger as stale. Both are idempotent: a second run prints
`no change` and writes nothing.

```bash
praetorctl state migrate-bugs .   # BUGS.md v1 metadata -> bugs.meta.json
praetorctl state compact .        # old STATE.md entries -> compact form
```

`state migrate-bugs` rewrites every v1 row as a v2 row and moves its metadata into
the sidecar. Legacy rows carry no metadata and stay byte for byte, as does all
surrounding Markdown. Before writing, it parses the migrated ledger together with
the new sidecar and compares every record with the original, field by field and in
order, down to time offsets and nanoseconds. Any difference refuses the migration
with both files unchanged. On success it resynchronizes.

`state compact` rewrites every engine-written legacy entry into the compact form and
drops every superseded marker, then appends a certifying entry and the new marker in
the same atomic write. An entry it does not recognise, including hand-written notes
under a `### [` heading, is kept line for line; only trailing blank lines are
normalized. Values are carried over unchanged.

Measured on a copy of this repository's ledger on 2026-09-18 (the real ledger was
not touched):

| File | Before | After |
| :--- | ---: | ---: |
| `STATE.md` (527 entries, 307 markers) | 181,987 B | 100,870 B (-45%) |
| `BUGS.md` (957 rows, 557 with v1 metadata) | 505,097 B | 291,376 B (-42%) |
| `bugs.meta.json` | none | 165,190 B |

The `BUGS.md` rows and prose that carried no v1 metadata were byte-identical after
migration, and an independent decode of every original v1 comment matched its
sidecar record.

## Persistence and failure handling

Initialization and snapshots reuse the confined directory and file operations in
`contextopt`. Caller-aware reads preserve cancellation and use bounded file snapshots.
The wrappers without a context parameter, `ListBugs`, `ListQuestions` and `ListTasks`
(`internal/state/bugs.go`, `questions.go`, `tasks.go`), give their read the 30-second
`contextopt.MaxDuration` deadline instead of an unbounded `context.Background()`, so the
HISS-02 scan (`internal/hiss/go_io.go`) finds no deadline-free context in them.

**The project root must be a real directory, and so must every component of the ledger beneath
it.** Initialization refuses a project root that is itself a symlink: accepting one would write
the ledger somewhere other than the repository the operator named. `.workingdir` is opened with
`contextopt.OpenDirectoryIn`, which walks each component below the root strictly, so a symlink
planted inside a repository under audit is refused rather than followed.

What is *not* rejected is a symlink in the path leading **to** the project root. That path is the
operator's filesystem, not repository content: macOS reaches its own temporary directory through
`/var`, a symlink, so refusing a symlinked ancestor refused every project under it and left much
of the suite unrunnable on that platform (#109). An attacker holding `/var` does not need a
symlink to defeat anything here.

On Unix, cooperating writers hold a persistent `.workingdir/.bugs.lock` inode
through read, validation, ID allocation and replacement. Contention returns a
bounded busy error; callers decide whether to retry. A writer unlocks before it
closes the descriptor, so a subprocess started meanwhile cannot keep the lock held
(`LockExclusive` in `internal/util/file_lock_unix.go`). The inode must not be removed
while writers may exist. Other platforms use an exclusive claim file; an
interrupted writer can leave a claim requiring inspection. Those platforms have
not received the Unix process-contention acceptance.

A mutation writes and syncs `BUGS.md.pending`, checks that the original bytes have
not changed, renames the candidate and syncs the directory. When the sidecar
changes, it goes through the same protocol as `bugs.meta.json.pending` first, under
the same lock. An interruption between the two renames never leaves a row without
its metadata. It can leave a sidecar record no row refers to yet (an interrupted
add; the next add of that ID replaces it), or a ResolvedAt that is ahead of a row
still shown open (an interrupted resolve; resolving again repairs it).
A failed staging file is retained and blocks further mutations. Inspect the candidate, original ledger
and active writers before recovering an interrupted operation. Never treat an
error as proof that a rename did not occur: directory sync or cleanup can fail
after replacement; read back the ledger before retrying.

This serializes cooperating CLI writers. It does not provide an atomic
compare-and-swap against editors that ignore the lock; the final external-edit
check and rename are separate operations. It is also not a transaction across
BUGS, OPEN, BACKLOG, QUESTIONS and STATE. Task/question mutations, question parsing,
STATE's unlocked append and Git-status error handling remain separate work.

## History: a 2026-09-12 recovery

This section is a dated record of one past incident and its private-ledger
counts at the time, not current guide content or a standing property of the
ledger.

All 712 surviving source mappings were checked against the retained 716-finding
audit. F5, F392, F485 and F634 were recovered as BUG-713 through BUG-716. Their
original attempted numbers had already been reused by other findings and were
preserved. Recovery changed none of the surviving rows. CreatedAt on recovered
records is the recovery registration time; original timestamps are unknown.

Only F392/BUG-714 and F389/BUG-190 were subsequently resolved against the parser
and read-error regression evidence at that time, leaving that ledger snapshot at
716 records: 713 open and three resolved. These were ledger statuses on that
date, not a claim that every open finding had been freshly reproduced, and the
counts have moved since.

The acceptance suite exercised that day covered full-field round trips,
legacy/prose preservation, P0 blocking, malformed and unreadable inputs,
failed-write preservation, goroutine and real CLI process contention, and fresh
CLI audit/sync failures, plus a bounded fuzz run of 728,092 executions. It did
not certify full-repository lint, security or dedupe cleanliness, and no release
receipt was implied by the passing ledger acceptance. The whole-workspace dedupe
run's `.claude/worktrees` exclusion (`internal/dedupe/dedupe.go`, via
`util.IsScratchDir`) postdates this incident.
