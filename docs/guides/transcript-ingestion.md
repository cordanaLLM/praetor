# Local transcript ingestion

`praetorctl harvest transcript` persists observed Antigravity transcript events in
an explicit private local cache. Each record carries its source SHA256, physical
line, step and original timestamp. Events remain observations, not verified facts;
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
optional `cursor`, `max_records` and `expected_sha256`. Its JSON text contains
`report` and, on failure, `error`; `isError` marks failed calls. Paths follow the
server's existing root confinement. Select the intended root explicitly when
connecting to workstation data. The regular `make mcp-probe` checks pagination,
on-disk readback, replay deduplication and cross-cache cursor rejection using
temporary fixtures.

This first adapter accepts Antigravity JSONL. Claude/Codex/Pi/Copilot JSONL,
OpenCode and other live SQLite stores, protobuf histories, global rules and
skills need their own format adapters and consistent snapshots. They are tracked
in the workstation replay work. Existing `harvest memory` and
`standards_memory_recall` retain their separate contracts; this cache is not
silently promoted into either verified-fact store.
