# Local dogfood scheduling

`praetorctl dogfood schedule run --config /absolute/schedule.json` executes at
most one due suite. `status` reads current admission state without creating files
or executing cases. A workstation timer can invoke `run` periodically; the CLI
itself is finite and requires Linux with procfs and advisory file locking.

Create a private JSON configuration (`0600`) with clean absolute paths:

```json
{
  "version": 1,
  "runner_binary": "/home/example/.local/bin/praetorctl",
  "suite_config": "/home/example/.config/praetor/dogfood-suite.json",
  "source_root": "/home/example/dev/praetor",
  "state_dir": "/home/example/.local/state/praetor/dogfood",
  "allow_remote": true,
  "interval_seconds": 86400,
  "retry_seconds": 3600,
  "max_consecutive_failures": 3,
  "max_runs": 32,
  "max_bytes": 2147483648
}
```

`version`, `runner_binary`, `suite_config`, `source_root`, `state_dir`, and
`allow_remote` are required. Numeric limits may be
omitted to use the illustrated defaults. Nulls, duplicate or unknown fields,
incorrect types, relative paths and symlink inputs are rejected. The suite
configuration must also be private. `state_dir` is created with mode `0700`
under an existing parent. Existing state metadata must be private; updating a
read-only state file is refused instead of broadening its permissions.

Limits are 60..2592000 seconds between unchanged successful runs, 60..86400
seconds between retries, 1..3 consecutive failures, 1..1024 retained attempts,
and 1 MiB..16 GiB retained bytes. The byte limit is an admission threshold, not
an in-flight disk quota: one running suite may cross it. Accounting includes all
entries, hidden files, Git objects, caches, input snapshots and partial attempts.
It stops safely at 200000 entries or 20000 entries in a directory. Nothing is
automatically deleted. Reaching either retention limit blocks further runs until
the operator reviews retained evidence and explicitly changes configuration or
selects a new private state directory.

The input fingerprint binds the exact schedule and suite bytes, configured CLI
binary content, source manifest and lock, every file under
`.config/archetypes` (including facets), and optional repair routing/usage
snapshots. The complete bundle snapshot is bounded to 512 entries and 16 MiB.
A run hashes its actual `/proc/self/exe` descriptor and requires it to match
`runner_binary`; replacing the installed pathname cannot misidentify already
running code. Status uses the configured runner, so an MCP server and CLI report
the same fingerprint. Inputs are copied into each attempt before suite execution;
transcript hashes and public commit pins remain explicit and are never updated.

A successful unchanged input waits `interval_seconds` after completion. Failures
wait `retry_seconds`; three failures for unchanged inputs open the circuit.
Changed engine or configuration inputs reset the failure circuit only when a
new run is admitted, after at least `retry_seconds` since the previous start.
A clock rollback delays admission. No reset or force-execution command exists.

An advisory lock prevents overlapping invocations and is released by the kernel
when a process exits. The lock file is retained. Before executing the suite the
scheduler saves and fsyncs the attempt's running state and provisional failure
count. An abandoned running attempt is recorded as interrupted on the next tick;
its partial evidence remains. Corrupt or inconsistent state, missing attempt
sequences, failed snapshot writes and failed persistence all stop execution.

Output distinguishes `due`, `cooldown`, `busy`, `circuit_blocked`,
`resource_blocked`, `verified`, and `failed`. `last_attempt.status` preserves a
previous failure or interruption even during cooldown. Only a newly completed
successful run sets top-level `verified: true`; status never claims new
verification. Read-only status and admission no-ops exit successfully with their
explicit status; execution and metadata errors return nonzero.

An optional `repair_policy` enables local failure triage plans:

```json
{
  "routing_config": "/home/example/dev/praetor/.config/model-routing.json",
  "task": "ci_debugging",
  "input_tokens": 8000,
  "output_tokens": 2000,
  "max_cost": 0.10
}
```

The object is the value of `repair_policy`, with optional `usage_path` for an
explicit capacity observation. All other fields are required. Its files are
snapshotted with the suite. Actual failed suite reports produce plans under
`run-000001/repairs`; nil reports and prevalidation errors cannot invent repair
jobs. Planning uses at most one extra minute of bookkeeping even after suite
cancellation; cancellation cannot silently discard a completed failure report.
Planning or persistence errors preserve the original suite failure. Green
runs skip repair planning. See [local repair plans](dogfood-repairs.md) for
configured-cost and capacity limits. This stage does not call providers, launch
coding agents, commit fixes, publish results, or promote repository stages.
