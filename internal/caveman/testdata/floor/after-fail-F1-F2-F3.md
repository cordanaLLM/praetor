## Operational Rules

1. **State ledger discipline.** Agents MUST maintain local ledger every turn. Never stage its contents, force included. Publish reviewed docs under `docs/` instead. Turn end: sync state.

2. **No evasion.** Never attempt `--no-verify` or modifying `.git/hooks`. Policy, blockers: [checkpoint workflow](docs/guides/checkpoint-cadence.md).

<!-- praetor:register:start -->
<!-- praetor:register:end -->
