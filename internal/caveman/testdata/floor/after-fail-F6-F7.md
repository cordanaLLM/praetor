## Operational Rules

1. **State ledger discipline (HISS-17).** Agents maintain local `.workingdir` ledger every turn. Stage its contents only when sure. Publish reviewed docs under `docs/` instead. Turn end:
   ```bash
   praetorctl state sync .
   ```

2. **Evasion.** Avoid `--no-verify` or modifying `.git/hooks`. Policy, blockers: [checkpoint workflow](docs/guides/checkpoint-cadence.md).

<!-- praetor:register:start -->
<!-- praetor:register:end -->
