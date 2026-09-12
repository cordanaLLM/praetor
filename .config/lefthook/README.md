# Canonical hook policy

`praetor.yml` is consumed through `extends` by Praetor itself. Keep the policy and
`scripts/` together: the YAML entry points intentionally use stable repository
paths. See [the hook guide](../../docs/guides/git-hooks.md) for behavior and tools.

An adopter can vendor this directory and
`.config/agent/hooks/` from the same reviewed Praetor commit, then
use the following root configuration:

```yaml
min_version: 2.1.12
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
a future packaged CLI adapter. The existing Go adoption scaffolder is outside
this change and must be updated before fleet-wide automatic rollout.

Verified against pinned Lefthook 2.1.12 with real Git and custom agent-job
invocations. The policy requires v2 for `agent-pre-tool`; the native Codex adapter
normalizes failure to blocking exit code 2. Install the tested release with:

```bash
go install github.com/evilmartians/lefthook/v2@v2.1.12
```

See [upstream agent integration](https://lefthook.dev/configuration/ai/) and the
[pinned implementation](https://github.com/evilmartians/lefthook/tree/v2.1.12).

No `stage_fixed` jobs are used. Checking exported index content avoids changing
partially staged hunks while retaining the formatting gate.
