# Local transcript ingestion

`praetorctl harvest transcript` persists observed Antigravity or Claude Code transcript events in
an explicit private local cache. Each record carries its source SHA256, physical
line, step and original timestamp. Claude observations also preserve the session,
record and parent UUIDs, plus tool-use IDs and replies. Events remain observations, not verified facts;
tool arguments are stored as data and never executed. Reports contain metadata
and counts. No external service is called.

```bash
go run ./cmd/standardsctl harvest transcript \
  --source=/path/to/conversation/.system_generated/logs/transcript_full.jsonl \
  --cache=/private/praetor/transcript-events \
  --max-records=1000
```

The JSON report includes `stored`, `already_present`, `skipped`, `remaining`,
`complete` and `next_cursor`. Pass the returned cursor as `--cursor='...'` with
the same source and cache to continue. Stop only when `complete` is true. A retry
deduplicates existing records. `--expected-sha256=...` additionally pins the
selected source bytes. If a call fails after storing records, its report describes
the partial outcome and supplies a retry cursor; the CLI exits nonzero.

The adapter prefers a sibling `transcript_full.jsonl` when invoked with
`transcript.jsonl`. Both names therefore identify the same complete source when
available. It preserves any declared truncation in records and reports the count.
Metadata-only records count as explicitly skipped. Internal thinking is excluded.

Inputs are bounded to 64 MiB, 100,000 physical records and 10,000 records per page.
The complete source is validated before cache writes. Malformed or unsupported
records, ambiguous duplicate JSON keys, changed sources, invalid cursors and
corrupt cache records return errors. A cursor cannot skip missing prior records
or be reused with another cache. The source directory and its ancestors cannot
serve as the cache. Cache directories and records use private permissions.

The development MCP exposes the same implementation as
`standards_transcript_ingest`, with required `source_path` and `cache_dir`, and
optional `format`, `cursor`, `max_records` and `expected_sha256`. Its JSON text contains
`report` and, on failure, `error`; `isError` marks failed calls. Paths follow the
server's existing root confinement. Select the intended root explicitly when
connecting to workstation data. The regular `make mcp-probe` checks pagination,
on-disk readback, replay deduplication and cross-cache cursor rejection using
temporary fixtures.

Codex/Pi/Copilot JSONL,
OpenCode and other live SQLite stores, protobuf histories, global rules and
skills need their own format adapters and consistent snapshots. They are tracked
in the workstation replay work. Existing `harvest memory` and
`standards_memory_recall` retain their separate contracts; this cache is not
silently promoted into either verified-fact store.

## Claude Code session JSONL

Select the adapter explicitly; the default remains `antigravity-jsonl-v1`:

```bash
praetorctl harvest transcript \
  --format=claude-code-jsonl-v1 \
  --source=/private/claude/projects/project/session.jsonl \
  --cache=/private/praetor/claude-events \
  --expected-sha256=SOURCE_SHA256 \
  --max-records=1000
```

The Claude adapter accepts an explicitly selected `.jsonl` file without looking
for siblings or scanning project directories. Its implementation is grounded in
the local Claude Code 2.1.269 session schema, including ToolSearch tool references;
this is an adapter version, not a promise to support every Claude release. The
installed CLI version may differ from the version recorded in an older session.

Supported observations are `user` and `assistant` messages containing text,
assistant `tool_use` blocks, and user `tool_result` blocks. Tool results may contain
text or `tool_reference` blocks. Tool calls and references remain inert data.
Text blocks are joined with a newline; tool calls, results and references retain
their order within their respective arrays. The source hash and physical line
provide provenance; this cache is not a lossless replacement for the JSONL file.

`thinking` and `redacted_thinking` blocks are excluded. Known non-message record
types (queue, attachments, session and history metadata, progress, summaries and
system records) are counted and skipped entirely, even if they contain text.
The `metadata_records` and `thinking_blocks` report fields count Claude exclusions
across the complete source, so do not sum them across pages. `skipped` counts the
current page's records without retained payload, including thinking-only messages.
The two new exclusion fields are zero for the original Antigravity adapter.

Messages require an original RFC3339 timestamp, UUID, session ID and matching
role. A single source cannot mix session IDs. Content and nested result arrays
are each limited to 128 blocks. Unsupported message blocks, nested tool calls,
malformed envelopes, ambiguous keys, media content and unknown record types fail
before any cache is written. They require a deliberate adapter extension.

Claude event IDs and resume cursors include the format. Existing Antigravity
cache IDs and old cursors remain usable; a cursor cannot be reused with another
format. Both adapters use the same byte, record, pagination, cache integrity and
private-permission checks. Unix source and cache opens are nonblocking and verify
file identity to reject concurrent FIFO replacements; Windows uses rooted file
opens. Other platforms fail explicitly. Filesystem calls may still be subject to
kernel I/O delays; the context deadline cannot forcibly interrupt those calls.

The development MCP probe also checks Claude format selection, real cache
readback, thinking exclusion, idempotent replay and unsupported-block rejection.
