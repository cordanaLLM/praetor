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

Declare the efficiency section in `.standards.yaml`. Every source is optional:

```yaml
efficiency:
  sources:
    forge:
      path: ".workingdir/forge_prs.json"
    transcripts:
      dir: ".claude/transcripts"
    spend_log:
      path: ".workingdir/spend-export.jsonl"
  frontier_models:
    - "claude-3-7-sonnet"
    - "claude-3-5-sonnet"
    - "gpt-4"
    - "o1"
    - "o3"
    - "gemini-2.5-pro"
    - "heavy-frontier"
  local_models:
    - "local"
    - "cluster"
    - "ollama"
    - "vllm"
    - "nano"
    - "gpu-local"
```

Run the subcommand to produce a summary table:

```bash
praetorctl efficiency
```

Filter by milestone or format as structured JSON:

```bash
praetorctl efficiency --milestone="1.0" --limit=20
praetorctl efficiency --json
```

## Command reference

| Command / Flag | Description | On failure |
| :--- | :--- | :--- |
| `praetorctl efficiency` | Evaluates merged pull requests and renders table output | missing arguments or corrupt sources |
| `--format=table\|json` | Sets the output serialization format (default `table`) | invalid format name |
| `--json` | Convenience alias for `--format=json` | |
| `--milestone=<title>` | Restricts output to landed pull requests under the specified milestone | |
| `--limit=<N>` | Upper bound on pull requests reported (default 20) | |
| `--path=<dir>` | Repository root where `.standards.yaml` lives (default `.`) | repository not found |
| `--forge-records=<file>` | Overrides forge records source with a specific JSON file | file unreadable or invalid JSON |
| `--transcripts-dir=<dir>` | Overrides agent session transcripts directory | directory unreadable |
| `--spend-log=<file>` | Overrides gateway spend-log export file (JSON lines or CSV) | file unreadable or invalid shape |

## Configured sources

All sources are bounded by HISS-02 invariants (scalar loop bounds, context timeouts, file and line
caps):

1. **Forge records:** Pull request number, head branch, creation and merge timestamps, and closing
   issues with their creation times. Loaded from a local JSON fixture (`sources.forge.path` or
   `--forge-records`) or queried via the live forge adapter (`internal/forge`) when credentials
   are present.
2. **Agent session transcripts:** A configured directory of Claude Code JSONL files. Each line
   carries `gitBranch` and token usage metrics. Session lines join to a pull request **only by
   exact head-branch match**, never by guess. Operator touches count human-authored user messages;
   tool results and hook or system injections are strictly excluded.
3. **Gateway spend-log export:** A JSON lines or CSV file containing model, spend/cost, token
   counts, and metadata tags. An entry attributes to a pull request **only when its metadata or tags
   name the branch or pull request number**. Every other entry reports as unattributed spend, never
   spread or discarded.

## Measured metrics

For each landed pull request unit, the ledger computes:

- **Issue-to-merge time:** Duration from the earliest closing issue creation time (or the pull
  request creation time if no closing issue is linked) to pull request merge time.
- **Operator touches:** Count of human user messages in joined transcript session lines.
- **Frontier tokens:** Total tokens (input, cache creation, cache read, output) consumed by models
  classified as frontier.
- **Attributed spend:** Dollar amount attributed to the unit by the gateway spend log.
- **Prompt-cache hit rate:** Ratio of `cache_read_input_tokens` over total input tokens (`input + cache_creation + cache_read`).
- **Local-first ratio:** Ratio of requests served by local or cluster models over total requests.
- **Fact-hit ratio and checks-before-reviews:** In Slice 1, both print `not measured (Refs #881)`
  pointing to the follow-up milestone epic tracking fact retrieval and verification gates.

### Missing sources rule

When a source is omitted or unconfigured, the corresponding metrics print `not measured`, **never
zero**. A missing spend log does not report `$0.00`; missing transcripts do not report `0` touches
or `0%` cache hit rate.

## Milestone summary

The milestone summary aggregates across all units in scope:

- Total landed units count
- Average issue-to-merge time
- Total operator touches
- Total frontier tokens
- Attributed spend, unattributed spend, and total spend breakdown
- Overall prompt-cache hit rate across all attributed requests
- Overall local-first ratio across all attributed requests
- Pointers to follow-up verification tracking (`Refs #881`)
