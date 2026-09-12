# Local repair timer

The verification timer produces retained suite reports. A separate user timer
can consume failed reports and run one bounded patch attempt per tick. Configure
the [repair executor](dogfood-repairs.md) first and verify a direct invocation
against its reviewed source commit, provider, file allowlist, and test packages.

Create a private version 1 queue configuration outside the repository:

```json
{
  "version": 1,
  "runner_binary": "/absolute/path/to/praetorctl",
  "runner_sha256": "<reviewed executable SHA256>",
  "schedule_state_dir": "/absolute/path/to/private/schedule-state",
  "repair_config": "/absolute/path/to/private/repair.json",
  "repair_config_sha256": "<reviewed repair config SHA256>"
}
```

Both configuration files and the schedule directory must be owned by the current
user and inaccessible to other users. Paths must be absolute without symlinks.
Refresh both digests after reviewing a new development binary or repair policy.
A changed digest stops the queue before dispatch.

```bash
python3 -B scripts/dev_repair.py status --config /absolute/path/to/queue.json
python3 -B scripts/dev_repair.py run --config /absolute/path/to/queue.json
python3 -B scripts/dev_repair.py install --config /absolute/path/to/queue.json --activate
systemctl --user status praetor-dogfood-repair-local.timer
```

The installer retains private, content-addressed copies of its two Python
modules, validates the generated units, backs up earlier managed units, and
rolls back a failed installation. Existing foreign units and drop-ins are
refused. It does not enable user lingering or modify system services.

Each tick reads at most 256 directory entries and 64 run directories, then checks
terminal `run-NNNNNN/suite/report.json` failures with the executor's read-only
status operation. Inspection has a 60-second deadline. The first eligible case
with an unconsumed execution key runs; all remaining cases wait for later ticks.
The executor owns the lock and durable attempt ledger, including interrupted
attempts. Repeated suite timestamps do not grant a second provider attempt for
the same source, input and policy identity. The queue never deletes evidence.
Exceeding inventory bounds requires explicit archival or selection of a new
queue directory; it does not silently omit older failures.

The service allows 12 minutes including inspection and shutdown, with a 4 GiB
memory limit and 128 tasks. It starts checking after two minutes and checks again
15 minutes after the previous service stops. The executor separately enforces
request, patch, source, test, and sandbox limits. Public source snippets and
bounded failure metadata go to the configured model; original transcript bodies
are not opened by the repair executor. Gateway cost readback is evidence of the
reported request cost. Declared routing rates are estimates, and gateway-side
retry or fallback behavior is outside this client's one-attempt guarantee.

`scoped_test_verified` means the configured unchanged tests failed before a
patch and passed afterward. It is a retained candidate awaiting review and full
suite replay, not an automatic source merge or confirmation that a private
corpus was repaired. A green baseline consumes the key as `not_reproduced`
without calling the provider. Publication and promotion are later stages.

Disable future ticks while retaining units, attempts, patches and any running job:

```bash
python3 -B scripts/dev_repair.py disable
```
