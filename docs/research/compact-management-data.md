# Compact, configurable management data

Status: researched and locally benchmarked on 2026-09-12. The release protocol,
upstream updater, lazy tool pool and global policy resolver described here are
implementation requirements, not deployed capabilities.

## Scope and ownership

The owner requires the same management capabilities in workstation clients, IDE
plugins, containers, forge bots and user-controlled GitOps forks: bootstrap,
tool discovery, agent routing and budgets, context preparation, package docs,
templates, changelogs and independently updatable model/provider data. Client
adapters translate a shared configuration; they do not each own policy or
implement their own updater.

Resolve explicit defaults, organization policy, deployment/workstation settings
and repository overrides into one typed effective configuration. Preserve field
provenance and distinguish mandatory constraints from overridable defaults.
Unknown values, explicit zero, disabled features and inheritance must have
different representations. Organization constraints must survive a local
override. Operator-specific configuration belongs in private overlays; public
releases must work without Cordana-specific paths, endpoints or accounts.

Machine artifacts need not be pleasant to edit. Generate reviewable diffs,
effective-policy explanations and ordinary client configuration from them.
Compression must reconstruct exact source bytes or explicitly versioned semantic
records; it must not silently summarize requirements away.

## Storage and update transport

Use a small signed manifest containing stable record identifiers, schema and
compatibility versions, source provenance, content hashes and decompression
bounds. Store independently hashed artifact groups so a model price update does
not invalidate an unrelated template pack. Validate signatures and compatible
schemas before activation; stage a complete version and switch its active
reference atomically, retaining the previous version for rollback.

Fetch missing or changed objects. Offer binary deltas as an optimization only
when the exact base hash is available and the transfer is smaller. Verify the
reconstructed target hash. A full object remains the recovery path. Zstandard
supports lossless compression and dictionaries; its ordinary frame format does
not itself provide arbitrary random access, so artifact boundaries matter.
[Zstandard format](https://www.rfc-editor.org/rfc/rfc8878.html)

Deterministic CBOR is a candidate for compact typed records, especially binary
hashes and repetitive structured data. It requires a specified deterministic
encoding profile, strict limits and duplicate-key handling. It has not yet been
benchmarked here, and replacing JSON solely because CBOR is binary is not a
measured improvement. [CBOR specification](https://www.rfc-editor.org/rfc/rfc8949.html)

Provider IDs, capabilities, prices, account quotas and observed availability have
different authorities and refresh cadences. Keep upstream timestamps and field
provenance; do not treat a community catalog as proof of an account's quota or a
model-name heuristic as measured capability. Unavailable observations stay
unknown. Templates and executable configuration use the same distribution
mechanism but separate validation and rollout policy. Do not silently distribute
credential values in data packs.

## Executed compression experiment

The corpus contains 21 tracked files from `.config/archetypes`, `.config/models`
and `.agents/agents`. Original file contents total 27,096 bytes. A lossless compact
JSON manifest with paths, source hashes and complete content is 30,787 bytes;
indented JSON is 31,292 bytes. Each result below reconstructed the exact manifest.
Five compression repetitions were measured; Zstandard times include CLI startup.

| Encoding | Bytes | Median encode time |
| --- | ---: | ---: |
| gzip level 9 | 8,481 | 0.43 ms |
| xz level 9 | 8,068 | 6.25 ms |
| Zstandard level 3 | 8,932 | 2.15 ms |
| Zstandard level 19 | 8,201 | 12.92 ms |

A synthetic single-record edit produced a 103-byte Zstandard delta against the
exact previous manifest; sending the changed record alone with level 19 took
1,268 bytes. The delta also reconstructed the target exactly. This is one small
corpus and one synthetic change, not a production distribution benchmark.

The result argues against inventing a compression format: gzip already obtains
most of the full-download reduction and is in the current Go standard library.
Evaluate dictionaries, CBOR and grouped content addressing on larger held-out
data before adding a runtime dependency. Packaging and model context must be
measured separately.

Retained private evidence: `routing-lint-20260912/pack_benchmark.py` and
`pack-benchmark/{source.json,changed.json,results.json,delta.zstd}` under the
continuation audit root. Corpus SHA-256:
`d3c54f1c825ba03c66bcf694bd1e35f48d15f32699b71d081ea4ff1f62814d18`.

## Model context and shared tools

Compressed bytes are unpacked by code, not inserted into prompts. Give each
model the required fields and source excerpts, with tool discovery loading
schemas only when needed. Keep large intermediate results outside model context
and expose bounded queries over them. OpenAI documents deferred tool loading;
Anthropic describes code execution and selective MCP discovery for reducing
unnecessary context. These mechanisms require client-specific support and
acceptance tests, not a universal environment variable.
[OpenAI tool search](https://developers.openai.com/api/docs/guides/tools-tool-search),
[Anthropic MCP context design](https://www.anthropic.com/engineering/code-execution-with-mcp)

A real AGY invocation using its live-listed Gemini Flash Low selection submitted
a 9,535-byte public source-review prompt. The client reported one turn, 33,932
input tokens, 852 output tokens and 12.21 seconds. This establishes substantial
additional context in that invocation; it does not identify all its sources or
prove an optimized replacement. One returned finding concerned pre-Go-1.22 loop
variables and was inapplicable to this Go 1.27 repository. Model output needs
verification even when its cost is low.

One shared registry should make the same tools reachable while preserving
per-tool authorization and client approval boundaries. Client-native tools and
session-only connectors cannot be assumed exportable to every other client.
Bootstrap must distinguish generated, configured, discovered, trusted and
successfully executed states. Loading every schema by default is especially
costly in clients such as OpenCode, whose own documentation warns about MCP
context growth. [OpenCode MCP](https://opencode.ai/docs/mcp-servers/)

## Acceptance before automatic promotion

Retain exact artifact regeneration, malformed/oversized-input rejection, native
client tool-call evidence and old-version rollback. Replay real package-doc and
changelog fragments through their CLI/MCP consumers. Compare cost, latency and
quality on held-out tasks before promoting prompt or routing changes. Run a
follow-up ownership and duplication audit after these shared boundaries settle.
Routine verified bug fixes may become patch releases; additive public APIs use
minor releases when following SemVer, and breaking changes require migrations.
Data artifacts can use an independent monotonically increasing release sequence.
