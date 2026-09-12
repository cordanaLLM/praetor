# Scheduled local dogfood verification

The local scheduler runs configured public repository and private transcript suites
through the installed `praetorctl`. It retains evidence for every admitted run and
creates local repair plans for failed cases. Plans select eligible models using
declared routing prices and explicit token estimates. They do not execute agents,
apply patches, promote repository stages, or send messages.

## Configure a bounded schedule

Create a private (`0600`) schedule JSON and suite JSON outside the public checkout.
All paths are clean absolute paths. The state directory's parent must exist.
The [suite guide](dogfood-suites.md) describes pinning public repositories and
transcripts. Public cases require `allow_remote: true` explicitly.

```json
{
  "version": 1,
  "runner_binary": "/home/example/.local/bin/praetorctl",
  "suite_config": "/home/example/.config/praetor/local-suite.json",
  "source_root": "/home/example/dev/praetor",
  "state_dir": "/home/example/.local/state/praetor/dogfood-local",
  "allow_remote": true,
  "interval_seconds": 86400,
  "retry_seconds": 3600,
  "max_consecutive_failures": 3,
  "max_runs": 32,
  "max_bytes": 2147483648,
  "repair_policy": {
    "routing_config": "/home/example/dev/praetor/.config/models/routing.yaml",
    "task": "ci_debugging",
    "input_tokens": 8000,
    "output_tokens": 2000,
    "max_cost": 0.1
  }
}
```

Inspect the configuration and run one bounded tick:

```sh
praetorctl dogfood schedule status --config /absolute/schedule.json
praetorctl dogfood schedule run --config /absolute/schedule.json
```

Ticks use a process lock, persisted attempt state, retry cooldown, and a consecutive
failure circuit. Changed build or configuration fingerprints become eligible for
a new run. A failed or interrupted run remains visible. The retained run count and
byte limit are **admission thresholds**, not disk quotas: an admitted suite can grow
beyond the threshold, after which subsequent admission stops. Evidence is never
automatically deleted. Review retained data and choose a new state directory when
the budget is exhausted.

## Install the user timer

Install fresh local binaries with `make dev-install`, then run:

```sh
python3 -B scripts/dev_schedule.py install --config /absolute/schedule.json --activate
python3 -B scripts/dev_schedule.py status
systemctl --user start praetor-dogfood-local.service
journalctl --user -u praetor-dogfood-local.service -n 40 --no-pager
```

The installer validates the schedule and generated units before replacing managed
files. It retains previous units and an installation manifest under
`~/.local/state/praetor/dev-schedules/install-*`, verifies activation, and attempts
rollback if installation fails. An interrupted process leaves backups for review.
Foreign units, overrides, symlinks, and concurrent installers are rejected.

The timer first checks after two minutes, then fifteen minutes after the previous
service finishes. The scheduler independently enforces its daily interval and
hourly retry policy. The oneshot service has a seventeen-minute start deadline; an
active service is not started again. User timers run while the user service manager
is available; the installer does not enable lingering or change system services.

Without `--activate`, installation preserves the previous timer activation state.
To stop future scheduling while retaining units, an active run, and all evidence:

```sh
python3 -B scripts/dev_schedule.py disable
```

Service success means that the tick completed according to scheduler policy; inspect
the schedule report to distinguish a verified suite from a cooldown or other skipped
tick. Model selection is an estimate from the configured catalog. Provider capacity,
current prices, and successful agent execution require later rollout evidence.

The development MCP exposes `standards_dogfood_schedule_status` for the same status
read. Its `config_path` and every embedded read path obey the server root policy;
it never creates schedule state or starts a run. Use an explicitly configured
workstation root when inspecting private configuration outside the source checkout.
