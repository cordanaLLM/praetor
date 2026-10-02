# Canonical hook policy

`praetor.yml` is consumed through `extends` by Praetor itself. Keep the policy,
`python.sh`, `tool-floors.txt`, `scripts/` and `pre-push/` together: the YAML entry
points intentionally use stable repository paths. The pre-push script job preserves checks when Lefthook's final
file diff is empty. See [the hook guide](../../docs/guides/git-hooks.md) for behavior
and tools.

An adopter can vendor this directory and
`.config/agent/hooks/` from the same reviewed Praetor commit, then
use the following root configuration:

```yaml
min_version: 2.1.14
assert_lefthook_installed: true
extends:
  - .config/lefthook/praetor.yml
```

Record that immutable source commit in the adopting repository's dependency
ledger and upgrade the policy plus scripts together. The generic file/message
checks work in non-Go repositories. Praetor-specific governance and refresh
commands require the `hook-cli` Make target and the matching Praetor CLI source;
the current package is intended for Praetor and repositories carrying those
interfaces. Do not advertise an arbitrary `praetorctl hooks` command: it does not
exist yet.

Lefthook also supports a pinned `remotes` entry with `git_url`, `ref` and `configs`,
but remote YAML does not copy referenced scripts into the consuming repository.
Remote distribution therefore still requires vendoring the matching scripts or
a future packaged CLI adapter.

`praetorctl adopt` does not write this policy. It writes a smaller root `lefthook.yml`
whose language jobs follow the languages it detects, and it keeps any root
configuration that is not one of its own renderings, `--force` included: one that
extends this policy through `extends` or `remotes`, and every other one. Beside a
configuration extending this policy it refreshes neither the vendored scripts nor
`.config/agent/hooks/block_evasion.py`. See
[the lefthook.yml adoption writes](../../docs/guides/git-hooks.md#the-lefthookyml-adoption-writes).

Verified against pinned Lefthook 2.1.14 with real Git and custom agent-job
invocations. The policy requires v2 for `agent-pre-tool`; the native Codex adapter
normalizes failure to blocking exit code 2. Install the tested release with:

```bash
go install github.com/evilmartians/lefthook/v2@v2.1.14
```

See [upstream agent integration](https://lefthook.dev/configuration/ai/) and the
[pinned implementation](https://github.com/evilmartians/lefthook/tree/v2.1.14).

No `stage_fixed` jobs are used. Checking exported index content avoids changing
partially staged hunks while retaining the formatting gate.

`tool-floors.txt` declares the oldest `shellcheck`, `actionlint`, `hadolint` and
`yamllint` the file checks accept, one `tool>=version` line each. `scripts/toolchain.py`
reads a linter's `--version` before it runs and refuses a missing or older one with
the required version; a linter with no staged or pushed file of its type is not
probed. Vendor the file with the policy: without it every commit that needs a
linter is refused. See [linter version floors](../../docs/guides/git-hooks.md#linter-version-floors).

`agent-checkpoint-tool` and `agent-checkpoint-stop` share the bounded checkpoint
evaluator. Add a reviewed `.config/agent/checkpoint.json` for the adopting repo
to enable it; missing policy is explicitly disabled. Configure publication only
for the authorized remote/repository. The native adapter translates due results
into lifecycle feedback; agents then execute reviewed commits and draft PRs
through the normal gates. See the [checkpoint guide](../../docs/guides/checkpoint-cadence.md).

The evaluator and Git jobs are shared across coding agents. Native lifecycle
events remain client-specific and require client approval, reload, and a live
event readback. A vendored policy, compiled agent context, or MCP projection is
not evidence that every client session is enforcing the lifecycle. See the
[coverage guide](../../docs/guides/agent-lifecycle.md) for the current boundary.

The Git metadata gate rejects additions and changes under private `/.workingdir/`,
including forced staging, submodule entries and private content added then removed
within outgoing history. Removing legacy tracked entries is allowed. Adopters
must keep this whole directory ignored and publish reviewed documentation under
`docs/` instead.

## Interpreter

No job names a Python interpreter. Each one starts its hook with
`sh .config/lefthook/python.sh <script> ...`, and `python.sh` runs the interpreter
`PRAETOR_PYTHON` names, or `python3` where the variable is unset or empty. A
host whose Python 3 has another name, such as a stock Windows install, sets the
variable instead of editing the policy. An interpreter the shell cannot start
is reported as a missing dependency with the shell's status, never as a hook
verdict. Hooks that start further Python processes reuse `sys.executable`.
`scripts/toolchain.py` prints what the host provides for every declared tool.
See [the interpreter the hooks run](../../docs/guides/git-hooks.md#the-interpreter-the-hooks-run).

The `hook-cli` and `state-audit` targets are run with GNU Make: the program
`PRAETOR_MAKE` names, or `make` where it is unset. A host without it is refused
with a missing-dependency message before any check runs; no hook skips the build.
See [the make the hooks run](../../docs/guides/git-hooks.md#the-make-the-hooks-run).

## Output policy

The policy sets `output: [execution_out, failure]` and `colors: false`. A hook run
prints what each job printed and, for a failed job, Lefthook's exit status and one
`✗ <job>` line. The version banner, the summary block, the per-job success lines
and every ANSI escape are gone.

The reason is cost. Each run lands in the tool output of the agent that triggered
it, for the main session and every subagent, on every commit, checkout, push and
agent lifecycle event. Measured with Lefthook 2.1.12 before this policy, one
`agent-pre-tool` run printed 1,868 characters, 1,570 of them ANSI escape
sequences, to carry the 25-character `PRAETOR_COMMAND_POLICY_OK` marker. With the
policy the same run prints 27 characters.

Contracts that read this output are unaffected. The native adapters in
`.config/agent/hooks/` look for their markers at the start of a line of job output,
and job output is exactly what `execution_out` keeps. A failing job still prints its own
diagnostic and is named on the `✗` line. `colors: false` also exports
`NO_COLOR=true` to jobs, so tools that honour it stop coloring their output.

To see Lefthook's own reporting while debugging, override the list for one run,
for example `LEFTHOOK_OUTPUT=meta,summary,execution lefthook run pre-commit`. A
personal `lefthook-local.yml` may set `output` or `colors` permanently. Both
override the shared policy because Lefthook loads them after `extends`.
