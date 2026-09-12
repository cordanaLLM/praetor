# Local dogfood failure triage

`praetorctl dogfood repairs` turns a completed suite report into a private review
plan. It connects actual failed dogfood cases to the offline task router. It does
not launch a coding CLI, call a provider, change source files or publish a repair.

```bash
praetorctl dogfood repairs \
  --report /existing/private/suite-run/report.json \
  --routing-config .config/models/routing.yaml \
  --task ci_debugging --input-tokens 2000 --output-tokens 1000 \
  --max-cost 0.02 --output /existing/private/review-plan
```

The parent output directory must exist. The command creates a new directory with
mode `0700` and `plan.json` with mode `0600`; existing destinations fail. Standard
output contains status, job count, estimated cost and report fingerprint only.
The full private plan retains diagnostic excerpts and explicitly named source
paths, so it belongs outside a public checkout.

## Evidence and bounds

The report must be a completed version 1 verification with 1–8 uniquely named
cases. Planned, unfinished, empty and contradictory reports fail. Successful
transcript cases require complete page evidence with consistent fresh ingestion
and replay counters. Successful public cases require matching pinned inputs and
stable applied verification attempts. These are consistency checks on supplied
local evidence, not a signature or an independent rerun of the original inputs.

The report loader accepts one stable regular file of at most 8 MiB. It rejects
symlinks, special files, non-UTF-8 JSON, unpaired Unicode surrogates, duplicate or
case-folded struct fields, unknown fields, missing required fields and explicit
null required scalars. Optional fields follow the exact representation emitted by
`RunSuite`: omit empty `omitempty` fields. Parsing is bounded to 32 nesting levels
and 262,144 JSON tokens. The reader never opens source, checkout or log paths named
inside the report. A 30-second context bounds each operation; regular filesystem
reads themselves remain subject to the kernel's I/O behavior.

Each failed or context-skipped case produces one job in report order. IDs bind
canonical JSON hashes of the complete report and case, including the immutable
input reference. Replanning the same report gives the same IDs; a new suite run
has a different report hash and therefore different IDs. This is identity within
a retained run, not deduplication of a recurring failure across scheduled runs.
Error excerpts are capped at 2,048 UTF-8 bytes with the complete error hash and
original byte count preserved. All report strings remain under `untrusted_evidence`,
separate from fixed review instructions. Neither an excerpt nor an input path
confers permission to execute or read it.

## Routing and budget

`--task` defaults to `ci_debugging`, which must be declared in the selected routing
configuration. Both token estimates apply to each job and at least one must be
positive. The existing router selects the eligible model with the lowest declared
weighted token cost. Configuration and optional capacity fingerprints are retained
in the plan. This does not validate current prices, provider availability, model
quality or latency; the checked-in catalog is unchanged.

`--max-cost` is required and caps the sum of admitted job estimates, not each job
independently. Explicit zero is allowed. Jobs consuming the remaining ceiling are
`review_required`; subsequent jobs are `blocked_budget`. An absent task candidate
or unavailable supplied capacity produces `unroutable`. Blocked plans are saved,
then the command exits nonzero. No fallback bypasses declared eligibility or the
budget. These estimates do not reserve quota or spend money.

`--usage /private/capacity.json` uses the router's strict capacity snapshot format.
Only explicitly observed models are eligible when this option is supplied.
Observations are operator-supplied; counters are not refreshed by this command.
Without a snapshot, route capacity remains unobserved. A completed, consistent
suite with no failed cases yields `no_failures` and an empty job list; a plan with
admitted jobs yields `ready_for_review`.

A scheduler may call `PlanRepairs` after a failed `RunSuite` and save its returned
plan even when the error is `ErrRepairsBlocked`. Automatic agent dispatch,
worktree repair, behavioral retesting and promotion are later stages.
