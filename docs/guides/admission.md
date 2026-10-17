# Admission for heavy local steps

`praetorctl admit` holds a heavy local step, such as a full gate, a race-detector test run or a
large build, until the host has the memory, swap and load headroom it asks for. It then runs the
step in its own memory-limited systemd scope and exits with the step's own status. The policy and
the scope live in `internal/admission/`, the host readings in `internal/sentinel/`, and the
command in `cmd/standardsctl/admit.go`.

Several sessions often share one workstation. When each starts its heavy step as soon as it is
ready, the steps start into a host that is already full: memory goes to swap, and the
out-of-memory daemon kills whatever scope is largest, which can be the editor with every session
in it. Admission makes a step wait for room instead, and the scope makes a step that grows past
its limit die alone.

## Run a step

```sh
praetorctl admit --label verify-all -- make verify-all
```

Everything after `--` is the step. While the host is under a threshold, the command prints one
line per reading and keeps waiting:

```text
19:04:05 admission wait verify-all: available 8.0 GiB < 12.0 GiB
```

Once the host has room, it prints one admission line, runs the step and returns when the step
ends:

```text
19:09:05 admitted verify-all: available 21.4 GiB, swap free 52.0 GiB, load 9.3
```

An agent runs `praetorctl admit` as a background command. Its client then reports the step's end,
and the admission line in the output shows when the step started.

## Thresholds

| Flag | Default | Meaning |
| :--- | :--- | :--- |
| `--min-available-gib` | `12` | least `MemAvailable` the host must report |
| `--min-swap-free-gib` | `15` | least `SwapFree` the host must report |
| `--max-load` | 1.25 per CPU | highest one-minute load average |
| `--wait` | `6h` | longest the step is held; after it the step is refused |
| `--poll` | `1m` | time between readings while held |
| `--timeout` | `6h` | longest the step runs once admitted |

A host exactly at a threshold is admitted. A host that stays under one until `--wait` passes
refuses the step with `still held after ...`, and the step never starts. A reading that fails
refuses the step at once, without waiting: the host could not be judged, so the step is not
admitted.

## Scope limits

| Flag | Default | What systemd does at the limit |
| :--- | :--- | :--- |
| `--memory-high` | `16G` | throttles the step and reclaims its memory |
| `--memory-max` | `24G` | moves the step's pages to swap |
| `--memory-swap-max` | `4G` | past `MemoryMax` plus this, kills the step |

Each limit is a whole number with `K`, `M`, `G` or `T`. The swap limit may also be `0`, which
keeps the step out of swap entirely. Any other value is refused before anything runs. The
properties are described in
[systemd.resource-control](https://www.freedesktop.org/software/systemd/man/latest/systemd.resource-control.html).

The swap limit is part of the containment. With `MemoryMax` alone, a step that grew past it was
swapped out and finished, so a growing step pushed the rest of the host into swap. With
`MemoryHigh` below `MemoryMax`, a step that grew was throttled rather than killed, and ended only
at its deadline. Both cases were measured on 2026-10-17. The defaults leave room for a full gate
on a workstation with 64 GiB; a step that needs less should ask for less.

## Platforms

On Linux with a systemd user manager the step runs in a transient scope:

```sh
systemd-run --user --scope --quiet --collect --expand-environment=no \
  --unit=admit-<label>-<time> \
  --property=MemoryHigh=16G --property=MemoryMax=24G --property=MemorySwapMax=4G -- <command>
```

`--expand-environment=no` keeps systemd from rewriting `$` in the command's arguments. Before each
step the command checks that scopes work by starting an empty one. Where that fails (no user
manager, a container, macOS, Windows), the step runs without a scope, and a line names the reason:

```text
admission verify-all: runs unscoped, <reason>
```

The host readings follow what each platform reports:

| Platform | Memory | Swap | Load |
| :--- | :--- | :--- | :--- |
| Linux | `/proc/meminfo` `MemAvailable` | `/proc/meminfo` `SwapFree` | `/proc/loadavg` |
| Windows | `GlobalMemoryStatusEx` available physical memory | not measured | not measured |
| macOS | not measured | not measured | not measured |

A figure that is not measured is not judged, and the admission line says so. When memory itself is
not measured, the step is admitted with `admitted unjudged`.

## Evidence

The tests in `internal/admission/admission_test.go` and `cmd/standardsctl/admit_test.go` cover the
cases this guide describes:

- `TestRunAdmit_Negative_UnderThresholdRefused`: a step asked to start under a threshold the host
  cannot meet is held, then refused, and never runs.
- `TestWait_Positive_HeldThenAdmitted`: a held step is admitted once the readings clear, with one
  wait line per held reading.
- `TestRun_Positive_ScopeCarriesTheLimit`: inside its scope the step reads its own `memory.max`
  back as the limit it was given.
- `TestRun_Negative_SpikeKillsOnlyTheStep`: a step that tries to hold 1 GiB under a 128M limit with
  no swap is killed, and the test process that started it keeps running.

The two scope tests skip on hosts without user scopes.

## Why not a queue tool

The [admission runner survey](../research/admission-runners.md) compares Pueue, nq, Task Spooler,
systemd-run and GNU parallel. None of them both waits for memory and swap headroom and contains
the step once it runs:

- Pueue, nq and Task Spooler admit by slot count and contain nothing.
- GNU parallel waits for free memory and load, but it is a batch tool for one producer.
- systemd-run contains a step in cgroups, but it starts the step at once.

`praetorctl admit` therefore reads the host through the existing sentinel, waits in a short loop
and uses systemd-run for containment.
