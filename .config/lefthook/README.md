# Canonical hook policy

`praetor.yml` is consumed through `extends` by Praetor itself. Keep the policy and
`scripts/` together: the YAML entry points intentionally use stable repository
paths. See [the hook guide](../../docs/guides/git-hooks.md) for behavior and tools.

An adopter can vendor this directory and
`.config/agent/hooks/block_evasion.py` from the same reviewed Praetor commit, then
use the following root configuration:

```yaml
min_version: 1.13.6
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

Verified against the installed 1.13.6 binary (`lefthook validate`, real Git-hook
invocations) and the upstream v1.13.6 source, commit
`539f66c92f10e20ed369d769afee1cd6e93d5735`:

- [Run arguments](https://github.com/evilmartians/lefthook/blob/v1.13.6/docs/mdbook/configuration/run.md)
- [Single stdin consumer](https://github.com/evilmartians/lefthook/blob/v1.13.6/docs/mdbook/configuration/use_stdin.md)
- [Extending configuration](https://github.com/evilmartians/lefthook/blob/v1.13.6/docs/mdbook/configuration/extends.md)

No `stage_fixed` jobs are used. Checking exported index content avoids changing
partially staged hunks while retaining the formatting gate.
