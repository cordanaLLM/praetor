# Bug ledger integrity

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
Legacy rows retain their original interpretation, including literal backslashes
and HTML entities. New or modified rows encode pipes, line breaks and edge spaces
and carry a `praetor-bug:v1` HTML comment. That comment contains Base64-encoded JSON
for Context, CreatedAt and ResolvedAt. Its version, fields and encoding are strict;
unknown, missing, null, duplicate and case-alias metadata fields are rejected.

The parser requires one complete ledger table. Fenced examples and unrelated
Markdown are preserved. A claimed bug row outside the table is an error. Limits
are 1 MiB per ledger, 10,000 records, 16 KiB per text field and IDs through
`BUG-999999999`. Invalid or oversized updates leave the original ledger intact.

Go callers should use `ListBugsContext`, `ParseBugsMarkdownStrict` and
`RenderBugsMarkdownStrict`. The old slice/string-only parser and renderer remain
for source compatibility but cannot communicate detailed errors. They return no
partial records or an explicitly invalid document when validation fails.

**Refresh older local binaries before writing this format.** An older writer can
discard the metadata comment or reinterpret encoded cells. `make dev-install`
refreshes this checkout's local binaries, retains rollback executables and records
their source identity; see [development MCP](development-mcp.md).

## Persistence and failure handling

Initialization and snapshots reuse the confined directory and file operations in
`contextopt`. Project and ledger symlinks are rejected before initialization writes.
Caller-aware reads preserve cancellation and use bounded file snapshots.

On Unix, cooperating writers hold a persistent `.workingdir/.bugs.lock` inode
through read, validation, ID allocation and replacement. Contention returns a
bounded busy error; callers decide whether to retry. The inode must not be removed
while writers may exist. Other platforms use an exclusive claim file; an
interrupted writer can leave a claim requiring inspection. Those platforms have
not received the Unix process-contention acceptance.

A mutation writes and syncs `BUGS.md.pending`, checks that the original bytes have
not changed, renames the candidate and syncs the directory. A failed staging file
is retained and blocks further mutations. Inspect the candidate, original ledger
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
