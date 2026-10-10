# Declared-task model routing

`praetorctl models route` makes an offline selection from
`.config/models/routing.yaml`. It filters by declared task and model capabilities,
checks projected request capacity against supplied counters and declared limits,
and minimizes configured token cost.
It does not dispatch an agent or call a provider. Prices, capabilities and task
assignments are operator declarations, not measured quality or current availability.

## Current execution path

```figure
model-routing
```

The CLI calls `LoadRoutingConfigContext`, optionally `LoadUsageSnapshot` and
`TrackerFromSnapshot`, then `ModelCapacityArbiter.SelectForTask`. This is a real
consumer of the Go router. There is no MCP route tool or scheduler dispatch
connection in this slice. The MCP `standards_dogfood_repair_status` tool consumes
the same advisory selection through repair planning, so oversized requests block
repair admission there too. The older `SelectModel` tier-cascade and
`SelectOrthogonalAuditor` helpers still exist, but have no production consumer.
They do not establish that a running agent harness enforces their policies.

## Select a declared task

Run from the Praetor checkout:

```bash
go run ./cmd/standardsctl models route \
  --task=feature_implementation --input-tokens=1000 --output-tokens=500
```

`--config` selects another routing file. `--capabilities` accepts a comma-separated
list of required labels. Every requested label must appear in that model's
`capabilities` list; a missing list grants no capabilities. No model ID, family,
parameter count or historical benchmark score implies eligibility. The existing
catalog has task assignments but no per-model capability declarations, so a
capability-constrained request requires explicit configuration first.

The task must occur in a tier's `target_tasks`. All models in all tiers explicitly
declaring that task are considered. A cheaper model in an undeclared tier remains
ineligible, including a tier named by the legacy `fallback_tier` field. No eligible
candidate is an error, never a silent downgrade or automatic escalation. The same
`target_tasks` label also selects the text register and the optional output budget that
`models route` reports; the register never changes the tier
(see the [text register guide](../guides/text-register.md)).

For eligible candidates the configured estimate is:

```text
cost_per_m_in * input_tokens / 1,000,000
  + cost_per_m_out * output_tokens / 1,000,000
```

Both prices must be explicit finite nonnegative numbers. Explicit zero is allowed;
missing or null rates cannot win as free models. Rates must use a common currency
or unit; no currency conversion is implemented. Token estimates are optional. With
neither given, the route is tier-only: `estimated_cost` is 0 and candidates rank by
the sum of their two per-million rates (`basis` says so), pinned by
`TestTierOnlyRouteRanksByRatesWithoutEstimates`. Each given estimate still drives
cost and the quota checks. Equal ranks resolve by lexicographic model ID, then tier
name, independently of map or list iteration order.

The JSON result includes the exact loaded configuration SHA256, selected model,
task/capabilities, token estimates, configured cost and capacity observation status.
It also includes `register_manifest_sha256`, binding the reported register row to the exact
repository manifest bytes used for resolution.
This work does not update the repository's existing model IDs, task presets or
rates. An existing zero rate is a declaration, not proof that the endpoint exists
or that running it has no resource cost.

## Explicit capacity observations

Without `--usage`, the CLI uses an empty in-process tracker and reports
`capacity_source: "unobserved"`, `capacity_observed: false`, with no recorded
headroom value. `quota_limits_known` says whether both configured quota dimensions
are positive. When both are known, `projected_headroom` describes utilization after
the request, using zero as a provisional starting counter when observations are
absent. These fields do not turn an unobserved selection into a live quota claim.

To use actual observations collected separately:

```bash
go run ./cmd/standardsctl models route \
  --task=feature_implementation --input-tokens=1000 --output-tokens=500 \
  --usage=/path/to/observed-usage.json
```

A snapshot is a JSON object with exactly these fields:

| Field | Required data |
| --- | --- |
| `version` | Integer `1` |
| `captured_at` | Nonzero RFC3339 timestamp of the observations |
| `models` | Object keyed by configured model IDs |
| Each model's `current_rpm` | Explicit nonnegative integer request counter |
| Each model's `current_tpm` | Explicit nonnegative integer token counter |

A model observation may also include nonnegative `total_spend`, nonnegative integer
`error_count`, and RFC3339 `last_429_time`. Those fields must use their exact names.
Duplicate keys, field-name aliases, unknown fields/models, null observations and
missing counters fail. Do not fill absent observations with invented zeroes.
When `--usage` is supplied, unobserved models and models lacking a positive RPM or
TPM limit cannot be selected. Programmatic callers obtain the same behavior with
`TaskRequest.RequireObservedCapacity`.

Admission adds one request and the sum of both token estimates before checking
each known limit against `exhaustion_threshold_percent`. At 80 percent, 7 recorded
requests against a 10 RPM limit admits one more; 8 does not. Similarly, 700 recorded
tokens against a 1,000 TPM limit admits 60 input plus 40 output tokens, but rejects
60 plus 41. Integer additions are checked before mutation, and threshold comparisons
use the exact configured numeric value without rounding large counters through
floating point. Headroom floats are diagnostics only.

A configured zero RPM or TPM limit disables that dimension in provisional
selection; it does not prove an unlimited provider quota. A recorded 429 triggers
the existing 30-second cooldown. Fully exhausted or cooling models remain
ineligible even at an exhaustion threshold of 100 percent. Orthogonal selection
uses the configured threshold and requires positive headroom.

These are historical counter checks. Snapshot timestamps are exposed, but neither
freshness nor rolling-window expiry is inferred. `total_spend` and `error_count`
do not produce a latency, success-rate or error-rate score. Advisory selection uses
one coherent tracker snapshot and does not reserve anything.

## Atomic reservations within a process

The Go API adds `arbiter.ReserveForTask(ctx, request) (*TaskReservation, error)` and
`tracker.FinishReservation(reservation, actual) error`. A successful reservation
selects and charges +1 RPM, estimated input+output TPM, and configured cost under
one tracker lock. It enforces a positive `max_concurrent_same_model`; zero means
reservation concurrency has not been configured and fails. Active reservations
also affect advisory selection and headroom reads. Share the same `LimitTracker`
between dispatchers and keep their routing configuration immutable while in use.

The opaque handle's `Route()` returns a detached description. Every successful
reservation must be finished, including failed or cancelled dispatches; defer the
cleanup in the owning runner and finish only after execution has actually stopped.
Admission cancellation and declined requests write no counters or handles.
Finishing does not require a context, so a cancelled request can still release its
slot. No background task, provider request or expiry timer is started by this API.

Pass `&router.ReservationUsage{Tokens: actualTokens, Cost: actualCost}` when actual
usage is known, including an explicit zero. Completion replaces the estimated
tokens and cost, keeps the one RPM charge, and releases the active slot exactly
once. Pass `nil` when actual usage is unknown: the charge stays conservative and
the slot is released. Do not also call `RecordUsage` for that request; that older
API is for independent consumption. Invalid actual usage leaves the handle active
for correction or a final `nil` completion. Duplicate, copied and foreign handles
are rejected without changing accounting.

Counters created only by estimated charges do not become observations, including
after `nil` completion. `ObservedUsage` returns charged counters plus a boolean
indicating whether supplied or actual usage exists; that boolean does not certify
every charge as measured. A lone 429 event records cooldown, not measured quota
counters. Accounting overflow saturates the affected counter,
blocks subsequent admission for that model, releases the finishing slot and returns
an explicit error. Later completions cannot reclaim saturated accounting. Legacy
`RecordUsage` cannot wrap counters or reopen a blocked model.

One tracker admits at most 1,024 active handles and cannot add new reservation model
identities beyond 1,024 tracked models. Counters are cumulative until a caller
deliberately builds a new tracker from independent observations. No automatic
reset is safe while reservations remain active. Separate processes, trackers or
CLI invocations do not share this state: this is not fleet persistence, provider
availability checking, or scheduler integration. `models route` remains advisory.

## Maintain the catalog with models sync

`praetorctl models sync` merges the built-in seed list into `routing.yaml`. With
discovery on, which is the default, it also adds the models installed on local
Ollama and vLLM endpoints, so a model you pull or serve becomes routable.

```bash
praetorctl models sync                         # seed list plus this machine's local models
praetorctl models sync --discover-local=false  # seed list only; no daemon needed
praetorctl models sync --prune                 # remove flagged retired seed entries
```

Discovery queries every endpoint in `--local-endpoints` (default
`http://localhost:11434,http://localhost:8000`), whatever its port. Each endpoint is
asked for Ollama's `/api/tags` first and, when that path answers 404, for the
OpenAI-compatible `/v1/models` that vLLM serves (`internal/router/discovery.go`). An
endpoint that does not answer is listed as `Endpoint skipped` and the sync goes on
with the others, keeping that endpoint's catalogued entries. A `--prune` run refuses
instead, because it refuses to prune from a partial local inventory (`internal/router/sync.go:386`).
`TestDiscoverLocalModelsIsolatesFailingEndpoints` and `TestSyncCatalogIsolatesFailedDiscovery` pin this.

A new entry's tier comes from the parameter-count tag in its ID (`7b`, `1.5b`, `235b`,
`8x7b`): below 5 billion is `nano`, below 20 `lightweight`, up to 35 `midweight`, and
larger `heavy-frontier`. Only an ID without a size tag falls back to name tags such as
`qwen3` or `haiku` (`ClassifyTier`, pinned by `TestClassifyTierSizeBoundaries`). Its
`family` is the training lineage the name starts with, read after the last `/` of a
hosted path, so `hf.co/<org>/Qwythos-9B-Claude-...` is not `anthropic`; open weights
of a vendor with a hosted API in the catalog (`gemma`, `gpt-oss`, `qwen`) share that
vendor's family, because the orthogonal auditor guards against correlated errors,
which hosting does not change (`DetectFamily`, `TestDetectFamilyMatchesVendorPrefixOnly`).

Each model entry records who owns it in `source`:

| `source` | Written by | What a sync does with it |
| :--- | :--- | :--- |
| `seed` | the seed list in `internal/router/sync.go` | rewrites it in place from the seed list; retired seed entries leave with `--prune` |
| `local` | discovery against a local Ollama or vLLM endpoint | keeps it; a rediscovered ID is never added twice |
| absent | an operator editing the file | keeps it |

Without `--prune`, a sync never removes an entry. Entries the seed list does not own
stay in their tier and order, and tiers the defaults do not define stay with their
descriptions and task labels. An installed model that is also a seed entry keeps its
seed entry instead of appearing twice. If a sync would still lose an entry, which
happens when the seed list drops a model the catalog marks `source: seed`, it writes
nothing and exits with an error that lists the IDs and names `--prune`.

`--prune` removes only flagged seed entries (`source: seed`) that are retired;
hand-declared entries, alias entries and local models are preserved as operator data,
and the command prints each removed ID.

A catalog that does not load, for example with a duplicate ID or an unknown `source`
value, is refused rather than overwritten; repair or delete it first. The write is
bound to the bytes the sync read: `contextopt.ReplaceSnapshot` checks them again
before replacing the file, so an edit made while the sync runs fails it instead of
being lost, apart from a writer that races that final check.

Governance and the default tiers' metadata follow the same ownership rule as model
entries, with or without `--prune`: each `governance` key and each default tier's
`description`, `target_tasks`, `fallback_tier` and `lane` that the file declares is kept, an
explicit `false`, zero or empty list included, and only an undeclared key takes the
built-in default (`internal/router/sync_settings.go`). To return a setting to its
default, delete the key and sync. `internal/router/sync.go:458` preserves unowned tiers
and `internal/router/sync.go:465` preserves hand-declared models across prune, so a declared
fallback to an operator tier is no longer dropped or left dangling by prune (`internal/router/sync_test.go:289`).
The behavior is pinned by `internal/router/sync_test.go` and
`cmd/standardsctl/models_sync_test.go`.

### Nightly catalog check

`.github/workflows/sync-models.yml` runs the offline sync every night and fails when
the result differs from the tracked catalog, which means a seed-list change landed
without a regenerated `routing.yaml`. The job has read-only repository access and
never commits. `main` requires a pull request, signed commits and passing checks with
no bypass actor, so a bot push cannot land there, and this repository does not let
`GITHUB_TOKEN` open pull requests. To clear the failure, run `praetorctl models sync`
and land the result in a pull request.

## Gateway aliases

An OpenAI-compatible gateway can serve router aliases (classes such as light, coding,
reasoning or auto) and refuse the concrete model IDs its key cannot use, so a pinned ID
the catalog lists may fail there. A catalog entry can therefore name an alias instead:

```yaml
gateway:
  address: https://gateway.example.com/v1   # yours; the repository ships none
  key_env: PRAETOR_GATEWAY_API_KEY          # variable holding the bearer key; never stored
tiers:
  lightweight:
    models:
      - {id: gateway-light, family: openai, provider: gateway, alias: light,
         cost_per_m_in: 0, cost_per_m_out: 0}
```

An alias entry needs a `gateway` section, and `id` stays the unique catalog key
(`internal/router/gateway.go`). The `provider` field is optional metadata; the router
sends the alias verbatim as the model name in gateway chat completions and expands
`{target}` to the bare alias in lane commands. A model declaring `provider` without an
alias is refused at config load.

The `key_env` setting must name an environment variable starting with `PRAETOR_GATEWAY_`
followed by at least one character of `[A-Z0-9_]`. A single constant validated at config load
enforces this allow-list and the refusal names the rule (e.g. `GITHUB_TOKEN` is refused).
Nothing in code, defaults or docs names a gateway: the adopter configures its address and aliases.

`models sync` makes one bounded call per alias entry (a one-token chat completion, 10
second deadline) and records `alias_status: answers` or `unanswered` with the gateway's
refusal in `alias_reason`. It does not trust the gateway's model listing, which can list
more than the key may use. A probe that cannot run, such as an unset `key_env` variable,
changes nothing and is reported as `Alias not probed`, as is a transport failure or an HTTP
408, 429 or 5xx answer, which say nothing about the alias; any other refusal is recorded
as `unanswered`. The probe is opt-in (`--probe-aliases`) because it sends the value of
`key_env` as a bearer token to the declared address: the address must be `https` unless
it is a loopback host, and a change to the `gateway` section in a pull request needs the
same review as a change to a CI secret binding. The route then applies three rules:

- An alias entry is a candidate only while its status is `answers`. An `unanswered` or
  never probed alias is skipped and listed in the result's `skipped` array with the
  reason (`alias has not been probed; run models sync --probe-aliases`).
- Alias exclusion is per tier. A tier holding at least one alias entry excludes its pinned
  non-local models, however cheap and whatever the probes said, because the gateway refuses
  concrete IDs; only models from a local runtime (`source: local`) stay, since they never
  pass through the gateway. A tier with no alias entry keeps its pinned models. When none
  of the aliases in an alias-holding tier answers, that tier fails closed with the skipped reasons.
- The harness is told the alias, never a model ID behind it.

`TestRouteNeverFallsBackToPinnedModelWhenGatewayServesAliases`,
`TestRouteExcludesPinnedModelsWhenNoAliasAnswers` and
`TestProbeAliasesRecordsAnswerAndReason` pin these.

## Lanes and outcomes

A lane says how a pick is executed. One `lanes` table in `routing.yaml`, read by the
same loader as the rest, maps a lane name to its harness and headless command, and a
tier or model names its lane (`lane:`; a model's own lane wins):

```yaml
lanes:
  gateway-coding:
    harness: coding-harness
    command: [coding-harness, run, --model, "{target}", --task, "{task}"]
tiers:
  lightweight:
    lane: gateway-coding
```

The command is an argument vector, never a shell line. `{target}` expands to the
alias of an alias entry or the model ID, `{task}` to the label; each stays one
argument whatever it contains, and any other `{name}` is refused at load. The
harness may be a local or gateway model through a headless coding harness, a
free-tier CLI or the frontier agent; Praetor ships no lane, because the command lines
are the adopter's.

`models route --task <label>` returns `lane` with `name`, `harness`, `target` and the
exact `command`. A pick with no lane declared returns `lane_note` instead of a guess.
`TestEveryDeclaredLabelRoutesToAnExecutableLane` routes every declared label.

`praetorctl models outcome --task <label> --target <t> --result ok|fail|timeout
[--lane <name>] [--duration-ms n] [--note text] [identity flags]` appends one
record to `.workingdir/routing/outcomes.jsonl` (`--outcome-log` changes it), a
private JSON Lines log that is never rewritten. It is the measured routing data
the efficiency ledger reads through `router.ReadOutcomes`, which fails on a record
it cannot decode instead of averaging over the rest. The router does not dispatch:
the caller that runs the command records how it ended.

Every outcome record requires a verified run identity:

- `--physical-model <id>`: resolved physical model behind an alias (for example
  `claude-3-7-sonnet-20250219`). When an alias is passed as target, the router
  resolves it through `routing.yaml` or requires an explicit `--physical-model`;
  naming the alias itself as the physical model is refused.
- `--harness <name>` and `--harness-version <version>`: harness that ran the task.
- `--prompt-digest <sha256:...>`: SHA-256 digest of prompt template or brief.
- `--context-digest <sha256:...>`: SHA-256 digest of compiled context.
- `--context-bytes <n>`: byte size of compiled context.
- `--tools <list>`: comma-separated list of tool names available during the run.
- `--rounds <n>` and `--retries <n>`: interaction rounds and prior retries.
- `--cost-estimate <dollars>` and `--actual-cost <dollars>`: pre-dispatch cost
  estimate and post-run measured cost.

An outcome record without mandatory identity fields is refused by `ValidateOutcome`.
Two runs differing only in prompt template produce distinct identity keys (`Key()`).
After recording an outcome, the command computes the estimate-error metric
(`actual_cost - cost_estimate`) and prints it per lane (`estimate-error [<lane>]: ...`).
Passing `--reconcile` prints an aggregated reconciliation table per lane across all
recorded outcomes in the log.

## Catalog freshness

Every entry may carry `preview: true` and an `as_of` date (YYYY-MM-DD) for when its data
was written or confirmed; a model ID containing `preview` counts as preview. `models
sync` lists each preview entry and each entry whose `as_of` is older than
`governance.catalog_max_age_days` (default 180) as `Catalog stale`. An answering alias
probe sets `as_of` to the sync date and a discovered local model gets the sync date. An
entry without `as_of`, and an entry owned by the seed list (`source: seed`), is not
judged on age: the seed list ships inside the binary, so a fixed date would turn the
audit red on that date with no change in the repository. The preview check applies to
every entry, seed included, so the seed prices are only as current as the binary.

`praetorctl audit` runs the same check over `.config/models/routing.yaml`
(`auditModelCatalog`) and fails on any finding; a repository without that file skips it,
saying so, and a file that does not load fails. Under the operator-data rule (operator data
is configured, never deleted), `praetorctl models sync --prune` removes only entries with
`source: seed` that the freshness check flags (for example retired preview models); it never
removes hand-declared or alias entries, and never touches the gateway or lanes sections. A
plain `models sync` refuses to remove flagged seed entries without `--prune`. The audit
remedy names each flagged hand or alias entry for the operator to refresh (`as_of`) or remove
by hand; retired seed entries leave with `models sync --prune --discover-local=false` or by hand. The seed list no longer carries the three
preview models it once did. Tests: `TestAuditModelCatalogFailsStaleAndPreviewEntries`,
`TestAuditModelCatalogWindowBoundary`, `TestSyncProbesAliasesAndReportsStalePreviewEntries`,
`TestSyncPruneRemovesOnlyFlaggedSeedEntries`.

## Bounds and configuration migration

Configuration and snapshot files must be regular files no larger than 1 MiB.
Routing accepts version 1, up to 16 tiers, 64 models per tier, 64 task/capability
labels per list, 256 bytes per name and 1,024 snapshot model entries. Model IDs are
globally unique. Each token estimate is bounded at 1,000,000,000; that is an input
safety bound, not an asserted model context-window limit. Overflowing cost
calculations fail. Reads and task selection accept caller cancellation.

Migration: the shared loader used by `models list` and `models route` now rejects
missing/null prices, unknown fields, duplicate IDs/tags, unsupported versions,
invalid bounds and nonregular input paths. Provide both numeric cost fields for
each model, remove misspelled/unknown keys, and use a regular config file. Existing
shipped routing data and `models sync` output already include both prices. This
shared validation prevents listing one ambiguous file as free while routing treats
it differently. Programmatically constructed task-routing descriptors must set
`CostRatesDeclared` when both configured rates are intentional.

Migration: `praetorctl audit` now fails on preview or stale catalog entries (`auditModelCatalog`).
Retired seed previews must be removed by hand or with `praetorctl models sync --prune --discover-local=false`.
A non-loopback HTTP gateway address is refused; use HTTPS for external gateways. Gateway alias probing
is opt-in via `--probe-aliases`. Gateway `key_env` must begin with the `PRAETOR_GATEWAY_` prefix followed
by at least one character of `[A-Z0-9_]`. Alias exclusion is scoped per tier rather than catalog-wide, so
tiers holding alias entries exclude pinned non-local models and fail closed if none answer, while tiers
without alias entries retain pinned models. `models sync --prune` removes only flagged seed entries
(`source: seed`) that are retired; hand-declared entries, alias entries, local models, and gateway/lanes
configurations are preserved as operator data. Flagged hand or alias entries must be refreshed (`as_of`)
or removed by hand as reported by `praetorctl audit`.

Migration: model entries accept an optional `source` field whose only values are
`seed` and `local`; any other value is rejected. `models sync` now merges instead of
rewriting, and removes entries only with `--prune`. A script that relied on a plain
`models sync` to discard entries must pass `--prune`. A catalog committed with a
duplicate ID, which earlier sync runs could produce by listing an installed seed model
twice, no longer loads; delete the second copy.

Migration: requests that cross a configured RPM/TPM threshold now fail before
selection, including oversized requests against an empty tracker. Supply realistic
token estimates and explicit applicable limits. `--usage` and
`RequireObservedCapacity` additionally require positive limits for both dimensions.
Consumers may read the additive `quota_limits_known` and `projected_headroom`
fields; existing `recorded_headroom` remains the pre-request counter diagnostic.
Dispatch consumers must opt into the reservation API, configure positive concurrency,
and finish every successful handle; calling advisory selection alone does not
enforce concurrency.

## Remaining dispatch and feedback work

Automatic agent dispatch, shared fleet reservations, feeding the
[outcome log](#lanes-and-outcomes) back into selection, quality calibration, retry/escalation policy and
cross-model review orchestration remain unimplemented integrations. The declared
`orthogonal_audit_required` setting does not establish scheduler enforcement.
The concurrency setting is enforced only by callers using the shared tracker
reservation API. The standalone `.config/agent/hooks/pre_agent_dispatch.py`
script has no verified hook registration here; this command does not invoke it.

`models list` displays routing configuration. The separate `models sync` path
merges a built-in catalog and can query configured local Ollama endpoints (see
[Maintain the catalog with models sync](#maintain-the-catalog-with-models-sync)).
It is not called by `models route`. There is no implemented upstream gateway
pricing synchronization in this path, and the catalog it merges into is the routing file
itself (`.config/models/routing.yaml`, the `--config` default), not a separate catalog file.
Future work must connect execution and real observation sources explicitly before
claiming live fanout or evidence-driven escalation.
