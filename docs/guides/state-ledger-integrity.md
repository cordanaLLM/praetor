# Bug ledger integrity

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

`praetorctl state init --if-absent` reports which of four things it did, because
three of them write nothing and an operator told a ledger was seeded stops
looking:

| Report | What happened |
| :--- | :--- |
| `Initialized private .workingdir/ in <dir>` | the directory was absent; this call made it and wrote the five ledger files |
| `Seeded a ledger into the existing .workingdir/ in <dir>` | the directory existed and held no ledger file; this call wrote the five |
| `Existing .workingdir/ in <dir> already holds ledger files and was left untouched` | a complete or partial ledger; nothing was written, and a partial one needs explicit repair |
| `.workingdir in <dir> is not a directory; nothing was written` | the path is a symlink or a regular file; remove it before initializing |

Every ledger file is published atomically: its content is staged under a private
`.praetor-*.pending` name, synced there, and linked into place. Seeding is no
longer arbitrated by one directory creator, so several processes may initialize
the same ledgerless directory at once; staged publication means each name is
either absent or holds its whole template, never a zero-byte file an audit would
read as a corrupt ledger.

Both forms of `praetorctl state init` then make Git ignore the directory, unless
the path is not a directory at all. Git is asked whether its own ignore rules
exclude `.workingdir` itself; global excludes do not count, because every clone
must inherit the rule. A directory excluded as a whole keeps every file under it
private, whatever negations follow; a rule such as `.workingdir/*` followed by
`!.workingdir/STATE.md` does not. The command acts on the answer:

| Git's answer | What `state init` does |
| :--- | :--- |
| the directory is in no Git work tree | nothing; no commit can publish it |
| the directory is already excluded, in any spelling | nothing; `.gitignore` stays byte for byte |
| not excluded, and `adoption.decline` names `git-ignore` | prints a warning; `.gitignore` belongs to the operator |
| not excluded | appends the private-artifact block `praetorctl adopt` maintains, reports it, and asks Git again |

The block is written by the same function adoption uses, `writeManagedGitIgnore`
in [`internal/adopt/gitignore.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/gitignore.go),
so a later `praetorctl adopt` recognizes it as its own. A `.gitignore` that
cannot be merged, such as one with an unterminated managed block, fails the
command. The behaviour is covered by
[`internal/adopt/private_ignore_test.go`](https://github.com/cordanaLLM/praetor/blob/main/internal/adopt/private_ignore_test.go)
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
Markdown are preserved. A claimed bug row outside the table is an error. Limits
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

A refused selector writes nothing, so `OPEN.md` stays byte-identical. The
fence tracker is `util.MarkdownFence` in `internal/util/marked_block.go`, the one
implementation every scanner that follows fences across a whole document drives
(HISS-19): the task parser, the bug-ledger parser, the marked-block finder, the
caveman line scanner (`internal/caveman/scan.go`) and the `AGENTS.md` vendor
splitter (`internal/agentcontext/render.go`). Readers that extract a single
labelled block, such as the PR receipt fence, match their own label and keep no
fence state. A line closes a fence when it repeats the
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
post-commit hook's text becomes `post-commit sync`.

The marker binds the Git state, the repository's own path, and the other ledgers
(`OPEN.md`, `BACKLOG.md`, `BUGS.md`, `QUESTIONS.md`, and `bugs.meta.json` and
`questions.meta.json` once each exists), so editing any of them stales it. Sync removes the previous marker before it
appends its own.

Git state is observed with `core.autocrlf=input`. The fixed input normalization keeps
one logical text state across platforms without rewriting the worktree, while a path
marked `-text` remains byte-sensitive. This also prevents an ordinary Windows Git
command using `core.autocrlf=true` from changing the marker merely by refreshing index
stat metadata; staged changes, untracked bytes, and changed text content still stale it.

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
bounded busy error; callers decide whether to retry. The inode must not be removed
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

## Recovery verified on 2026-09-12

All 712 surviving source mappings were checked against the retained 716-finding
audit. F5, F392, F485 and F634 were recovered as BUG-713 through BUG-716. Their
original attempted numbers had already been reused by other findings and were
preserved. Recovery changed none of the surviving rows. CreatedAt on recovered
records is the recovery registration time; original timestamps are unknown.

Only F392/BUG-714 and F389/BUG-190 were subsequently resolved against the parser
and read-error regression evidence. The resulting ledger has 716 records: 713
open and three resolved. These are ledger statuses, not a claim that all open
findings have been freshly reproduced.

Acceptance covers full-field round trips, legacy/prose preservation, P0 blocking,
malformed and unreadable inputs, failed-write preservation, goroutine and real CLI
process contention, and fresh CLI audit/sync failures. The bounded fuzz run passed
728,092 executions. Full repository verification still fails existing lint and
security checks; the whole-workspace dedupe run also includes ignored Claude
worktrees. No release receipt is implied by the passing ledger acceptance.
