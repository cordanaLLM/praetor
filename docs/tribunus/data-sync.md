# Tribunus data sync

Tribunus is a model catalog: one record per model (or, for a subscription
plan with no single model behind it, per usage window), each field tagged
with where it came from and whether it was measured live or declared by a
published catalog. `tribunus/cmd/tribunusctl sync` gathers those records
from a handful of sources into one JSON snapshot file; `show` renders that
snapshot as a table. There is no database and no merge across sources in
this slice: a snapshot is exactly what one sync run produced.

## Why this lives inside praetor for now

Tribunus starts inside `cordanaLLM/praetor` (operator decision Q-063,
`.workingdir/planning/tribunus-sync-design-20260918.md`) because a routing
policy needs full data sync working first, and there is no interim
praetor-side routing policy to build against yet. The layout is move-ready:
`tribunus/` sits inside praetor's Go module with no nested `go.mod`, so
`go build ./...` and `go test ./...` already cover it without a Makefile or
CI change. Nothing under `tribunus/` imports praetor's `internal` packages,
and praetor does not import `tribunus/catalog` yet -- that import is the
only thing praetor may eventually take from this tree, once Tribunus grows
routing logic and moves to its own repository (`git subtree split -P
tribunus` plus one import-prefix rewrite).

## The record shape

`tribunus/catalog.Record` carries a model id, provider, access path (`api`,
`subscription_cli`, `gateway`, or `local`), context window, price per
million tokens in/out, capabilities, rate limits, and a usage window --
every field optional except the model id, access path, and provenance.
Provenance is `{source, fetched_at, kind}`, where `kind` is `measured` (read
live from a real system at fetch time) or `declared` (copied from a
published catalog, not observed from a live account). A field a source
cannot support is left `nil`/empty and named in `Record.Absent` with a
reason string -- never guessed. See `tribunus/catalog/record.go` and
`tribunus/catalog/snapshot.go` for the full shape and `Validate()` rules.

## Sources

Every source in `tribunus/internal/sources/` is fetched independently: one
source failing never blocks or discards another's records. `sync` prints
one `<source> <status> count=<n> [detail]` line per source and writes every
record every source produced into the same snapshot.

| source | how | kind | verified |
| --- | --- | --- | --- |
| `codex-local` | Parses `rate_limits.primary` from the newest `~/.codex/sessions/**/*.jsonl` line, read-only. | measured | Live against a real session log on 2026-09-18: `primary.used_percent=100`, `window_minutes=10080`, matching the design doc's field shape. |
| `litellm-gateway` | `GET <base>/v1/models` with a bearer token read from a file (never logged). | measured | Live against `https://litellm.ai.cauda.dev` on 2026-09-18: HTTP 200, 28 models including `cordana/auto`. `/model/info` returned 403 for the agent token, so gateway records carry no price/context data -- `Record.Absent` says so per record. |
| `ollama-local` | `GET <endpoint>/api/tags` for installed models, `GET <endpoint>/api/ps` to mark which are loaded. | measured | Live against `http://localhost:11434` on 2026-09-18: both endpoints HTTP 200. Duplicates `internal/router`'s Ollama calls on purpose -- `tribunus` cannot import praetor `internal` packages yet, and `internal/router` is expected to build on `tribunus/catalog` once Tribunus moves out. |
| `public-catalog` | `GET` OpenRouter's public models API and LiteLLM's public price map. | declared | Both URLs and schemas verified live on 2026-09-18 (HTTP 200, real bodies) before writing the parser, per the design doc's requirement. The two catalogs use incompatible model-id schemes (e.g. `openai/gpt-4` vs `gpt-4`); slice 1 does not reconcile them, so each sub-fetch tags its own records with `public-catalog:openrouter` or `public-catalog:litellm-prices` even though `sync` reports both under one `public-catalog` line. |

Two sources from the design doc's table have **no implementation** in slice
1, by design, not oversight:

| source | why not |
| --- | --- |
| `claude-subscription` | No local source: the stats cache on this workstation is stale since 2026-02. Recorded here as a follow-up research item, not scraped from an undocumented endpoint. |
| `gemini`/`agy` quota | No readable local store for it. Same follow-up-item treatment. |

## Running it

```bash
# every source, written to ./tribunus-snapshot.json
tribunusctl sync

# a subset, explicit output path
tribunusctl sync --sources=codex-local,ollama-local --out=/tmp/snapshot.json

# litellm-gateway needs both flags or it reports a skip, not an error
tribunusctl sync --sources=litellm-gateway \
  --litellm-base=https://litellm.ai.cauda.dev \
  --litellm-token-file="$HOME/.config/cordana/litellm/agent-token"

# render a snapshot as a table
tribunusctl show --in=/tmp/snapshot.json
```

Flags: `--sources` (comma-separated, default all four), `--out` (default
`tribunus-snapshot.json`), `--litellm-base` / `--litellm-token-file`
(required together for `litellm-gateway`), `--ollama` (default
`http://localhost:11434`), `--sessions-dir` (default `~/.codex/sessions`),
`--openrouter-url` / `--litellm-prices-url` (default the URLs verified
above; override only for testing against a local fixture).

Every HTTP and file read carries a context timeout and a byte/line bound;
`sync` itself times out after 3 minutes total. No flag or output ever
prints a bearer token.

## Not in this slice

Routing, subscription scraping via undocumented endpoints, a UI, the
planned Rust kernel, and persistence beyond the snapshot file are explicitly
out of scope for slice 1 (design doc, "Not in slice 1"). A snapshot is a
point-in-time file an operator or a later routing layer reads; nothing here
schedules or repeats a sync.
