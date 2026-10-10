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
| `--forge-records=<file>` | Overrides forge records source with a specific JSON file | file unreadable, invalid JSON, unknown or repeated key, row without its epoch or a labelled vector field |
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

1. **Forge records:** Pull request records loaded from a local JSON file (`sources.forge.path` or
   `--forge-records`) or queried live through the manifest's forge kind and the shared token
   resolver.

   A records file is a JSON array of rows, or an object whose only member is `units` holding that
   array. It is read strictly (`forge.ParseMergedPullRequests`): an unknown or repeated key, a
   second document, `null`, or an object without `units` fails the run. Each row carries:

   - `number` (or its alias `pull_request_number`), `head_branch`, `title` and `milestone`. A
     row without a number, or whose two number fields name different pull requests, fails the
     run.
   - `created_at` and `merged_at` as RFC 3339 timestamps, and `closing_issues` with their
     `number` and `created_at`.
   - `disposition`: exactly one of `qualified`, `offered`, `rejected`, `abandoned`, `reverted`
     or `timed_out`. A row without one, or with any other spelling, fails the run.
   - `lane`: a lane identifier such as `claude-code` or `agy:flash`, counted per lane.
   - `metric_epoch`: the schema epoch tag, currently `2026-10-10`. A row without it, or a ledger
     that mixes epochs, fails the run.
   - All six [vector fields](#vector-fields-and-provenance), each as
     `{"value": <v>, "provenance": "<label>"}`, or `null` when the field was not measured. A row
     that omits a vector field fails the run and names the field.

   ```json
   {"units": [{"number": 12, "head_branch": "feat/x", "title": "Add x",
     "created_at": "2026-10-01T10:00:00Z", "merged_at": "2026-10-01T12:00:00Z",
     "disposition": "qualified", "lane": "claude-code", "metric_epoch": "2026-10-10",
     "tokens_by_provider": {"value": {"anthropic": 1200}, "provenance": "measured"},
     "wall_seconds": {"value": 7200, "provenance": "measured"},
     "review_rounds": {"value": 1, "provenance": "measured"},
     "retries": {"value": 2, "provenance": "interval", "low": 1, "high": 3},
     "operator_minutes": {"value": 5, "provenance": "modeled"},
     "escaped_defects": null}]}
   ```

   **Revert rule:** a `qualified` row titled `Revert "<title>"` (the git and GitHub form) or
   `revert: <title>` (the conventional-commit form) marks the latest `qualified` unit titled
   `<title>` that merged before it as `reverted`; the revert itself stays `qualified`. A revert
   that is not `qualified` reverted nothing, a target with another disposition keeps it, a later
   re-land under the same title stays `qualified`, and any other title such as `Revert <title>`
   names no target. Reverts are read from every loaded record, so `--milestone` and `--limit`
   do not hide them (`applyRevertDispositions` in `internal/efficiency/collector_unit.go`).

   **Live listing:** the GitHub listing scans closed pull requests and keeps the merged ones, so a
   live run sees only merged units; `rejected`, `abandoned` and `timed_out` units need a records
   file. A live row carries no ledger fields of its own, so the collector stamps them
   (`stampLiveUnit` in `internal/efficiency/collector_unit.go`) and states each choice under
   Notes:

   - `metric_epoch` is the current epoch, because the collector builds the row under the current
     schema.
   - `disposition` is `qualified`, or `reverted` under the revert rule. Check and review results
     are not read, and the revert rule sees only the listed pull requests, so a revert outside
     `--limit` or `--milestone` is missed.
   - `wall_seconds` is `measured`: the pull request's creation to its merge.
   - `tokens_by_provider`, `review_rounds`, `retries`, `operator_minutes` and `escaped_defects`
     are not measured, because the listing carries no such data. Pass a records file to report
     them.

   The live listing filters by milestone first, sorts by merge time and keeps the newest
   `--limit`. Closing issues come from `Closes`/`Fixes`/`Resolves #N` in the pull request body
   (references qualified with a repository are ignored); the issue's `created_at` is fetched
   with a bounded read. Without a token the forge is `not measured` and the reason is printed
   under Notes.
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
- **Run identity and estimate error:** Unit rows report the resolved physical model
  (`resolved_model`), full run identity (`identity`: physical model, harness, version,
  prompt digest, context digest, context bytes, tool-set digest and count, prior rounds,
  retries, cost estimate), deterministic run key (`identity_key`), and reconciled estimate
  error (`estimate_error` / `estimate_error_amount`). The error sums only the unit's runs
  that carry both a cost estimate and an actual cost; with none, `estimate_error` reports
  `not measured`. Outcomes come from `--outcomes`, which must exist, or the default
  `.workingdir/routing/outcomes.jsonl`, which may be absent; a log that cannot be read
  fails the report.
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
holds for a unit that no session or spend entry joins, and for a vector field that is `null` or
has no live source. Any rate with a zero denominator prints `undefined`, **never zero**.

### Vector fields and provenance

Cost is a vector of six fields, never one composite score:

| Field | Meaning |
| :--- | :--- |
| `tokens_by_provider` | tokens spent, keyed by provider |
| `wall_seconds` | wall-clock seconds to landing; a live row measures pull request creation to merge |
| `review_rounds` | review cycles before landing |
| `retries` | retry attempts and test re-runs |
| `operator_minutes` | human operator time in minutes |
| `escaped_defects` | defects found after landing, in later gates or production |

Every measured field carries one provenance label: `measured`, `modeled`, `cited` or `interval`.
An `interval` field also carries `low` and `high` with `low <= value <= high`, over the same
providers as its value; no other label carries bounds. A field without a label, with an unknown
label, with a negative value or with broken bounds fails the run (`ValidateRow` in
`internal/efficiency/vector.go`). `internal/efficiency/vector.go` is the one reader of a vector
field; the forge package hands the field over as raw JSON.

### Summary rules

The denominator is the **qualified** unit: a landed unit whose gates and review passed. Offered,
rejected, abandoned, reverted and timed-out units stay visible in the lane counts, one count per
disposition plus the total over every unit (`2 qualified, 1 reverted, 1 offered, 4 total`). Each
summary figure follows one of three rules, and its label and text name the rule:

| Rule | Figures | Computation |
| :--- | :--- | :--- |
| Resource per qualified unit | the six vector fields, operator touches, frontier tokens, attributed spend | total over **every** unit, failures included, divided by the qualified-unit count |
| Latency, qualified mean | issue-to-merge | mean over the qualified units that measured it |
| Ratio, all usage | prompt-cache hit rate, local-first ratio | numerator and denominator summed over the usage of every unit, then divided |

The resource rule keeps the cost of failed units in the numerator, so a lane that lands one unit
out of three pays for all three. The figure reads, for example,
`1.50 per qualified unit (total 3 over 3 units / 2 qualified) [measured 2, modeled 1]`:

- **Provenance mix:** the bracket counts the units behind the figure per label, so a total that
  adds measured and modeled values says so.
- **Intervals:** when an `interval` field contributed, the rate and the total print their bounds,
  `3m00s [2m35s, 3m50s]`, and the JSON output carries `total_bounds` and
  `per_qualified_unit_bounds`.
- **Partial coverage:** when only some units measured a figure, the total bounds the cost from
  below and prints as `>= ... (lower bound: total ... over k of n units measured ...)`; the JSON
  output sets `lower_bound`.
- **No qualified unit:** every per-qualified-unit figure prints `undefined`, never `0`; an empty
  ledger prints `undefined` for every rate.
- **Zero-failure claims:** escaped defects that every unit measured (provenance `measured`) and
  that total zero print the rule-of-three bound per qualified unit and its basis,
  `0 (rule-of-three bound <= 1.50 per qualified unit; n=2 qualified units, 3 units observed)`.
  With no failure in the observed units the expected count is at most 3 (95 %), so at most 3/n
  per qualified unit. A modeled, cited or interval zero prints as an ordinary rate.

## Milestone summary

The summary prints the units, the qualified units, the lane counts in total and per lane, and
then:

| Label | Rule |
| :--- | :--- |
| Issue-to-Merge (Qualified Mean) | latency |
| Wall Seconds, Review Rounds, Retries, Operator Minutes, Escaped Defects, Tokens by Provider per Qualified Unit | resource |
| Operator Touches, Frontier Tokens, Spend per Qualified Unit | resource |
| Prompt-Cache Hit Rate (All Usage), Local-First Ratio (All Usage) | ratio |

followed by the spend split:

- **Attributed:** spend of the listed units.
- **Other units:** spend attributed to pull requests that were loaded but are not listed.
- **Unattributed:** spend that names no pull request.
- **Total:** every positive spend in the log.

The Notes section lists everything the run could not do completely: a forge listing cut by a
bound or by `--limit`, closing issues that could not be fetched, skipped transcript lines, a
missing token and repeated spend-log requests.
