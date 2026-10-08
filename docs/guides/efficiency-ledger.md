# Efficiency ledger

The efficiency ledger measures and reports engineering efficiency across landed pull requests and
milestone summaries. It aggregates metrics from three optional, bounded sources: forge records,
agent session transcripts, and gateway spend-log exports.

This is Slice 1 of the module specified in
[#870](https://github.com/cordanaLLM/praetor/issues/870) (epic
[#879](https://github.com/cordanaLLM/praetor/issues/879), milestone "1.0 — Measure and route cheap").
It provides the `praetorctl efficiency` command (alias `praetorctl efficiency-ledger`), the strict
manifest configuration schema, and table/JSON renderers.

## Quick start

Declare the efficiency section in `.standards.yaml`. Every source is optional, and manifest paths
must be repository-relative; a session directory outside the repository (for example under the
home directory) is passed with `--transcripts-dir`.

```yaml
efficiency:
  sources:
    forge:
      path: ".workingdir/forge_prs.json"
    transcripts:
      dir: ".workingdir/sessions"
    spend_log:
      path: ".workingdir/spend-export.jsonl"
    transcripts_via_gateway: false
  frontier_models: ["claude-opus-", "claude-sonnet-"]
  frontier_classes: ["reasoning"]
  light_classes: ["light"]
  local_models: ["local", "ollama"]
```

Every list is optional; an omitted list uses the default below. `transcripts` accepts `dir` or
`path` (the same setting); setting both to different values is a manifest error.
`transcripts_via_gateway` (bool, default `false`) controls request counting when both session
transcripts and gateway spend logs join the same unit.

```bash
praetorctl efficiency
praetorctl efficiency --milestone="1.0 — Measure and route cheap" --limit=20
praetorctl efficiency --json
praetorctl efficiency --transcripts-dir="$HOME/.claude/projects/<project>" --spend-log=spend.jsonl
```

`--milestone` matches the exact milestone title.

## Command reference

| Command / Flag | Description | On failure |
| :--- | :--- | :--- |
| `praetorctl efficiency` | Evaluates merged pull requests and renders table output | unreadable source, manifest error, limit overflow |
| `--format=table\|json` | Sets the output serialization format (default `table`) | invalid format name |
| `--json` | Convenience alias for `--format=json` | |
| `--milestone=<title>` | Restricts output to landed pull requests under the milestone; applied before `--limit` | |
| `--limit=<N>` | Upper bound on pull requests reported (default 20); a cut is printed under Notes | |
| `--path=<dir>` | Repository root where `.standards.yaml` lives (default `.`) | |
| `--manifest=<file>` | Manifest to read instead of `<path>/.standards.yaml` | file unreadable or invalid |
| `--forge-records=<file>` | Overrides forge records source with a specific JSON file | file unreadable or invalid JSON |
| `--transcripts-dir=<dir>` | Overrides agent session transcripts directory | directory unreadable, limit overflow |
| `--spend-log=<file>` | Overrides gateway spend-log export file | file unreadable, invalid row, limit overflow |

## Sources and bounds

Every source is read under HISS-02 bounds. A bound that is exceeded fails the run with the source
and the bound named; nothing is cut off silently.

| Source | Bound | Value |
| :--- | :--- | :--- |
| Transcripts directory | files | 20000 |
| Transcript or spend-log file | lines (records) | 1000000 |
| Transcript or spend-log file | bytes read | 256 MiB |
| Transcript line | bytes | 8 MiB (the harvester line cap) |
| Forge listing | closed pull requests scanned | 2000 (a hit is printed under Notes) |
| Forge listing | closing-issue lookups | 200 (a hit is printed under Notes) |

1. **Forge records:** Pull request number, head branch, creation and merge timestamps, milestone and
   closing issues with their creation times. Loaded from a local JSON file (`sources.forge.path` or
   `--forge-records`) or queried live through the manifest's forge kind and the shared token
   resolver. The live listing scans closed pull requests, filters by milestone first, sorts by merge
   time and keeps the newest `--limit`. Closing issues come from `Closes`/`Fixes`/`Resolves #N`
   in the pull request body (references qualified with a repository are ignored); the issue's
   `created_at` is fetched with a bounded read. Without a token the forge is `not measured` and the
   reason is printed under Notes.
2. **Agent session transcripts:** A directory of agent session JSONL files, read through the
   harvester's line reader and record decoder (8 MiB lines, UTF-8, surrogate and duplicate-key
   checks). Subagent sessions below the directory are read; hidden directories are skipped. Lines
   join to a pull request **only by exact head-branch match**. When a head branch name was reused,
   the sessions belong to the pull request merged last. Lines that fail the decoder checks (for
   example an unpaired surrogate) are listed under Notes with file and line; I/O errors and bound
   overflows fail the run.
3. **Gateway spend-log export:** Rows of the LiteLLM `LiteLLM_SpendLogs` table as JSON lines, a JSON
   array or CSV (by extension, else by content). Accepted fields:

   | Field | Use |
   | :--- | :--- |
   | `request_id` | repeated ids count once |
   | `model`, `model_group` | classification (the group, i.e. router class, first) |
   | `spend` | spend; rows with zero spend still count as requests |
   | `total_tokens`, `prompt_tokens`, `completion_tokens` | tokens: `total_tokens`, else prompt plus completion (prompt includes cached input) |
   | `request_tags`, `metadata.tags` | attribution tags: `branch:<name>`, a bare branch name, `pr:<n>`, `pull_request:<n>` |
   | `metadata.branch`, `metadata.pull_request`, `metadata.pr` | attribution |
   | `branch`, `pull_request`, `pr`, `tags` | the same attribution as flat columns |
   | `startTime`, `call_type`, other columns | accepted, not used |

   `request_tags` and `metadata` may be JSON values or JSON text (CSV exports). An entry
   attributes to a pull request **only when a tag or field names its branch or number**; every
   other entry is unattributed spend.

## Measured metrics

For each landed pull request unit the ledger computes:

- **Issue-to-merge time:** From the earliest closing issue's creation to the merge. A unit without a
  fetched closing-issue creation time prints `not measured` and is left out of the average. The
  pull request's own age is never substituted. The summary says how many units were measured.
- **Operator touches:** User records with `origin.kind` `human`. Meta records, compact summaries,
  sidechains, task notifications, peer messages, hook output and tool results are not touches.
- **Frontier tokens:** Input, cache creation, cache read and output tokens of frontier requests.
  A response written as several lines (one per content block) with the same `message.id` and
  `requestId` counts once, with its final usage.
- **Spend:** Attributed spend from the spend log; `not measured` when no entry names the unit.
- **Prompt-cache hit rate:** `cache_read / (input + cache_creation + cache_read)` from transcripts.
- **Local-first ratio:** Local requests over all requests of the unit.
- **Fact-hit ratio and checks-before-reviews:** `not measured (see #897)`.

- **Sources:** Sources that counted for requests and tokens: `transcripts+gateway`, `gateway`, `transcripts`, or `not measured`.

**Requests, local-first and frontier tokens join across configured sources.** By default
(`sources.transcripts_via_gateway: false`), transcript requests and gateway entries both count in
full (they represent distinct requests, for example when an agent calls its provider directly
without routing through the gateway). When `sources.transcripts_via_gateway: true`, the gateway
counts; transcript usage for joined units is not added (they represent the same requests).
Transcript-only metrics (operator touches, prompt-cache hit rate when the gateway carries no cache
fields) still come from transcripts. Every unit row and JSON output name the sources that counted;
when `transcripts_via_gateway: true`, any unadded transcript requests are reported under Notes
and in the JSON output.

### Model classes

A request is frontier when its router class (the `model_group`, last path segment) is a frontier
class, or, when the class is neither frontier nor light, when its model matches a frontier family.
A light class and any model name carrying a cheap-tier marker are never frontier. A model is local
when its class or model matches a local entry. Families are matched as prefixes of the provider
model ID with an optional provider path stripped.

The default model lists below are family prefixes of current provider IDs, so
`gpt-5` matches `gpt-5` and `gpt-5.1-codex` but not `gpt-5-mini`, and `gemini-3` matches the pro
models but not flash or flash-lite.

Default frontier_models: `claude-opus-`, `claude-sonnet-`, `claude-fable-`, `gpt-5`, `gpt-6`, `gemini-3`, `gemini-2.5-pro`

Default frontier_classes: `reasoning`, `coding`

Default light_classes: `light`

Default local_models: `local`, `cluster`, `ollama`, `vllm`, `gpu-local`

Default cheap markers: `mini`, `nano`, `flash`, `lite`, `haiku`

### Missing sources rule

When a source is omitted, a metric that needs it prints `not measured`, **never zero**; the same
holds for any ratio with a zero denominator and for a unit that no session or spend entry joins.

## Milestone summary

The summary aggregates over the selected units and states how many units each figure covers:
average issue-to-merge, operator touches, frontier tokens, the mean prompt-cache hit rate and
local-first ratio, and the spend split:

- **Attributed:** spend of the listed units.
- **Other units:** spend attributed to pull requests that were loaded but are not listed.
- **Unattributed:** spend that names no pull request.
- **Total:** every positive spend in the log.

The Notes section lists everything the run could not do completely: a forge listing cut by a
bound or by `--limit`, closing issues that could not be fetched, skipped transcript lines, a
missing token and repeated spend-log requests.
